package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/example/cos-ftp-server/internal/audit"
	"github.com/example/cos-ftp-server/internal/auth"
	"github.com/example/cos-ftp-server/internal/db"
)

// normalizeRootFolder canonicalizes a root folder path to "/seg/seg" form:
// leading slash, no trailing slash, no empty/duplicate segments.
func normalizeRootFolder(s string) string {
	s = strings.Trim(s, "/")
	return "/" + s
}

func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username   string `json:"username"`
		RootFolder string `json:"root_folder"`
		Password   string `json:"password"`
		Enabled    bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	body.RootFolder = strings.TrimSpace(body.RootFolder)
	if body.Username == "" || body.RootFolder == "" {
		http.Error(w, "username and root_folder are required", http.StatusBadRequest)
		return
	}
	body.RootFolder = normalizeRootFolder(body.RootFolder)
	if err := auth.ValidatePassword(body.Password); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Ensure the COS folder placeholder exists BEFORE creating the DB row.
	// PUT is idempotent (overwrite of an empty object) — on any failure (bad
	// credentials, missing bucket, perms) we bail and the user is NOT created.
	if a.COSClient != nil {
		key := strings.TrimPrefix(body.RootFolder, "/") + "/"
		if err := a.COSClient.Put(r.Context(), key, bytes.NewReader(nil), 0); err != nil {
			http.Error(w, friendlyCOSErr("create root folder", err), http.StatusBadGateway)
			return
		}
	}

	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	u := &db.FTPUser{
		Username:     body.Username,
		PasswordHash: hash,
		RootFolder:   body.RootFolder,
		COSBucket:    a.COSBucket,
		COSRegion:    a.COSRegion,
		Enabled:      body.Enabled,
		MaxSessions:  5,
	}
	if _, err := db.CreateFTPUser(r.Context(), a.Pool, u); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			http.Error(w, "username already exists", http.StatusConflict)
			return
		}
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	created, err := db.GetFTPUserByUsername(r.Context(), a.Pool, body.Username)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	a.Audit.Log(audit.Event{Username: CurrentUser(r), Action: "ADMIN_CREATE_USER", Path: created.Username, Success: true})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(created)
}

func (a *API) updateUser(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	var body struct {
		RootFolder string `json:"root_folder"`
		Enabled    bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	body.RootFolder = strings.TrimSpace(body.RootFolder)
	if body.RootFolder == "" {
		http.Error(w, "root_folder is required", http.StatusBadRequest)
		return
	}
	body.RootFolder = normalizeRootFolder(body.RootFolder)
	existing, err := db.GetFTPUserByUsername(r.Context(), a.Pool, username)
	if errors.Is(err, db.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	existing.RootFolder = body.RootFolder
	existing.Enabled = body.Enabled
	if err := db.UpdateFTPUser(r.Context(), a.Pool, existing); err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	updated, err := db.GetFTPUserByUsername(r.Context(), a.Pool, username)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	a.Audit.Log(audit.Event{Username: CurrentUser(r), Action: "ADMIN_UPDATE_USER", Path: username, Success: true})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(updated)
}

func (a *API) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := auth.ValidatePassword(body.Password); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	existing, err := db.GetFTPUserByUsername(r.Context(), a.Pool, username)
	if errors.Is(err, db.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	if err := db.SetFTPUserPassword(r.Context(), a.Pool, existing.ID, hash); err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	a.Audit.Log(audit.Event{Username: CurrentUser(r), Action: "ADMIN_RESET_PASSWORD", Path: username, Success: true})
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) deleteUser(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	existing, err := db.GetFTPUserByUsername(r.Context(), a.Pool, username)
	if errors.Is(err, db.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	if err := db.SoftDeleteFTPUser(r.Context(), a.Pool, existing.ID); err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	a.Audit.Log(audit.Event{Username: CurrentUser(r), Action: "ADMIN_DELETE_USER", Path: username, Success: true})
	w.WriteHeader(http.StatusNoContent)
}
