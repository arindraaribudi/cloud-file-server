package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/ssh"

	"github.com/example/cos-ftp-server/internal/audit"
	"github.com/example/cos-ftp-server/internal/auth"
	"github.com/example/cos-ftp-server/internal/core"
	"github.com/example/cos-ftp-server/internal/db"
)

// normalizeRootFolder canonicalizes a root folder path to "/seg/seg" form:
// leading slash, no trailing slash, no empty/duplicate segments.
func normalizeRootFolder(s string) string {
	s = strings.Trim(s, "/")
	return "/" + s
}

// resolveBackend fills u.StorageBackend with the effective backend so API
// responses never expose the "" (deployment default) placeholder.
func (a *API) resolveBackend(u *db.FTPUser) {
	if a.Storage != nil && u != nil {
		u.StorageBackend = a.Storage.Resolve(u.StorageBackend)
	}
}

// hasDotDotSegment reports whether any path segment is ".." — such root
// folders could escape a local storage root and are never legitimate.
func hasDotDotSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username    string `json:"username"`
		RootFolder  string `json:"root_folder"`
		Password    string `json:"password"`
		Enabled     bool   `json:"enabled"`
		FTPEnabled  *bool  `json:"ftp_enabled"`
		SFTPEnabled *bool  `json:"sftp_enabled"`
		// StorageBackend selects where this user's files live ("cos" or
		// "local"). Empty picks the deployment default. Immutable afterwards.
		StorageBackend string `json:"storage_backend"`
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
	if hasDotDotSegment(body.RootFolder) {
		http.Error(w, "root_folder must not contain '..'", http.StatusBadRequest)
		return
	}
	body.RootFolder = normalizeRootFolder(body.RootFolder)
	if err := auth.ValidatePassword(body.Password); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	backend := strings.ToLower(strings.TrimSpace(body.StorageBackend))
	if a.Storage != nil {
		backend = a.Storage.Resolve(backend)
		if !a.Storage.Has(backend) {
			http.Error(w, "storage_backend must be one of: "+strings.Join(a.Storage.Enabled(), ", "), http.StatusBadRequest)
			return
		}
	} else if backend == "" {
		backend = core.BackendCOS
	}

	// Ensure the COS folder placeholder exists BEFORE creating the DB row.
	// PUT is idempotent (overwrite of an empty object) — on any failure (bad
	// credentials, missing bucket, perms) we bail and the user is NOT created.
	// Only COS users need it; local folders are created on first mount.
	if backend == core.BackendCOS && a.COSClient != nil {
		key := strings.TrimPrefix(body.RootFolder, "/") + "/"
		if err := a.COSClient.Put(r.Context(), key, bytes.NewReader(nil), 0); err != nil {
			http.Error(w, friendlyCOSErr("create root folder", err), http.StatusBadGateway)
			return
		}
	}

	// Non-COS backends have no placeholder object; mounting creates (and so
	// validates) the folder, failing before the DB row exists.
	if backend != core.BackendCOS && a.Storage != nil {
		if _, err := a.Storage.Mount(&db.FTPUser{RootFolder: body.RootFolder, StorageBackend: backend}); err != nil {
			http.Error(w, "create root folder: "+err.Error(), http.StatusBadGateway)
			return
		}
	}

	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	ftpEnabled := true
	if body.FTPEnabled != nil {
		ftpEnabled = *body.FTPEnabled
	}
	sftpEnabled := false
	if body.SFTPEnabled != nil {
		sftpEnabled = *body.SFTPEnabled
	}
	u := &db.FTPUser{
		Username:       body.Username,
		PasswordHash:   hash,
		RootFolder:     body.RootFolder,
		Enabled:        body.Enabled,
		MaxSessions:    5,
		FTPEnabled:     ftpEnabled,
		SFTPEnabled:    sftpEnabled,
		StorageBackend: backend,
	}
	// cos_bucket is NOT NULL; only COS users carry bucket/region.
	if backend == core.BackendCOS {
		u.COSBucket = a.COSBucket
		u.COSRegion = a.COSRegion
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
	a.resolveBackend(created)
	a.Audit.Log(audit.Event{Username: CurrentUser(r), ClientIP: clientIP(r), Action: "ADMIN_CREATE_USER", Path: created.Username, Success: true, Detail: map[string]any{"storage_backend": created.StorageBackend}})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(created)
}

func (a *API) updateUser(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	var body struct {
		RootFolder  string `json:"root_folder"`
		Enabled     bool   `json:"enabled"`
		FTPEnabled  *bool  `json:"ftp_enabled"`
		SFTPEnabled *bool  `json:"sftp_enabled"`
		// StorageBackend is accepted only to be rejected if it differs: the
		// backend is fixed at creation because changing it would orphan data.
		StorageBackend string `json:"storage_backend"`
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
	if hasDotDotSegment(body.RootFolder) {
		http.Error(w, "root_folder must not contain '..'", http.StatusBadRequest)
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
	if want := strings.ToLower(strings.TrimSpace(body.StorageBackend)); want != "" && a.Storage != nil &&
		a.Storage.Resolve(want) != a.Storage.Resolve(existing.StorageBackend) {
		http.Error(w, "storage_backend cannot be changed after the user is created", http.StatusBadRequest)
		return
	}
	existing.RootFolder = body.RootFolder
	existing.Enabled = body.Enabled
	if body.FTPEnabled != nil {
		existing.FTPEnabled = *body.FTPEnabled
	}
	if body.SFTPEnabled != nil {
		existing.SFTPEnabled = *body.SFTPEnabled
	}
	if err := db.UpdateFTPUser(r.Context(), a.Pool, existing); err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	updated, err := db.GetFTPUserByUsername(r.Context(), a.Pool, username)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	a.resolveBackend(updated)
	a.Audit.Log(audit.Event{Username: CurrentUser(r), ClientIP: clientIP(r), Action: "ADMIN_UPDATE_USER", Path: username, Success: true})
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
	a.Audit.Log(audit.Event{Username: CurrentUser(r), ClientIP: clientIP(r), Action: "ADMIN_RESET_PASSWORD", Path: username, Success: true})
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
	a.Audit.Log(audit.Event{Username: CurrentUser(r), ClientIP: clientIP(r), Action: "ADMIN_DELETE_USER", Path: username, Success: true})
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) setUserSFTPKey(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	var body struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	body.PublicKey = strings.TrimSpace(body.PublicKey)
	if body.PublicKey != "" {
		if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(body.PublicKey)); err != nil {
			http.Error(w, "invalid public key: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	existing, err := db.GetFTPUserByUsername(r.Context(), a.Pool, username)
	if errors.Is(err, db.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	if err := db.SetFTPUserSFTPKey(r.Context(), a.Pool, existing.ID, body.PublicKey); err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	a.Audit.Log(audit.Event{Username: CurrentUser(r), ClientIP: clientIP(r), Action: "ADMIN_SET_SFTP_KEY", Path: username, Success: true})
	w.WriteHeader(http.StatusNoContent)
}
