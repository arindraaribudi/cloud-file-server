package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/example/cos-ftp-server/internal/audit"
	"github.com/example/cos-ftp-server/internal/auth"
	"github.com/example/cos-ftp-server/internal/db"
)

var adminUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func (a *API) listAdminUsers(w http.ResponseWriter, r *http.Request) {
	users, err := db.ListAdminUsers(r.Context(), a.Pool)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, codeInternal, "internal")
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (a *API) createAdminUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if !decodeAdminBody(w, r, &body) {
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	if !adminUsernamePattern.MatchString(body.Username) {
		writeJSONError(w, http.StatusBadRequest, codeBadRequest, "username must be 1-64 characters (letters, numbers, . _ -)")
		return
	}
	role := normalizeAdminRole(body.Role)
	if role == "" {
		writeJSONError(w, http.StatusBadRequest, codeBadRequest, "role must be admin or readonly")
		return
	}
	if err := auth.ValidatePassword(body.Password); err != nil {
		writeJSONError(w, http.StatusBadRequest, codeBadRequest, err.Error())
		return
	}
	if _, err := db.GetAdminUserByUsername(r.Context(), a.Pool, body.Username); err == nil {
		writeJSONError(w, http.StatusConflict, codeConflict, "username already exists")
		return
	} else if !errors.Is(err, db.ErrNotFound) {
		writeJSONError(w, http.StatusInternalServerError, codeInternal, "internal")
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, codeInternal, "internal")
		return
	}
	if _, err := db.CreateAdminUser(r.Context(), a.Pool, body.Username, hash, role); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeJSONError(w, http.StatusConflict, codeConflict, "username already exists")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, codeInternal, "internal")
		return
	}
	created, err := db.GetAdminUserByUsername(r.Context(), a.Pool, body.Username)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, codeInternal, "internal")
		return
	}
	a.Audit.Log(audit.Event{
		Username: CurrentUser(r), ClientIP: clientIP(r), Action: "ADMIN_CREATE_ADMIN",
		Path: body.Username, Success: true, Detail: map[string]any{"role": role},
	})
	writeJSON(w, http.StatusCreated, created)
}

func (a *API) updateAdminUser(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	var body struct {
		Role string `json:"role"`
	}
	if !decodeAdminBody(w, r, &body) {
		return
	}
	role := normalizeAdminRole(body.Role)
	if role == "" {
		writeJSONError(w, http.StatusBadRequest, codeBadRequest, "role must be admin or readonly")
		return
	}
	existing, err := db.GetAdminUserByUsername(r.Context(), a.Pool, username)
	if errors.Is(err, db.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, codeNotFound, "admin not found")
		return
	} else if err != nil {
		writeJSONError(w, http.StatusInternalServerError, codeInternal, "internal")
		return
	}
	if username == CurrentUser(r) && role != "admin" {
		writeJSONError(w, http.StatusBadRequest, codeBadRequest, "cannot remove your own admin role")
		return
	}
	if isAdminRole(existing.Role) && role != "admin" {
		n, err := db.CountAdminRoleUsers(r.Context(), a.Pool)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, codeInternal, "internal")
			return
		}
		if n <= 1 {
			writeJSONError(w, http.StatusConflict, codeConflict, "at least one admin account is required")
			return
		}
	}
	if err := db.UpdateAdminUserRole(r.Context(), a.Pool, username, role); err != nil {
		writeJSONError(w, http.StatusInternalServerError, codeInternal, "internal")
		return
	}
	updated, err := db.GetAdminUserByUsername(r.Context(), a.Pool, username)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, codeInternal, "internal")
		return
	}
	a.Audit.Log(audit.Event{
		Username: CurrentUser(r), ClientIP: clientIP(r), Action: "ADMIN_UPDATE_ADMIN",
		Path: username, Success: true, Detail: map[string]any{"role": role},
	})
	writeJSON(w, http.StatusOK, updated)
}

func (a *API) resetAdminPassword(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	var body struct {
		Password string `json:"password"`
	}
	if !decodeAdminBody(w, r, &body) {
		return
	}
	if err := auth.ValidatePassword(body.Password); err != nil {
		writeJSONError(w, http.StatusBadRequest, codeBadRequest, err.Error())
		return
	}
	if _, err := db.GetAdminUserByUsername(r.Context(), a.Pool, username); errors.Is(err, db.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, codeNotFound, "admin not found")
		return
	} else if err != nil {
		writeJSONError(w, http.StatusInternalServerError, codeInternal, "internal")
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, codeInternal, "internal")
		return
	}
	if err := db.UpdateAdminUserPassword(r.Context(), a.Pool, username, hash); err != nil {
		writeJSONError(w, http.StatusInternalServerError, codeInternal, "internal")
		return
	}
	a.Audit.Log(audit.Event{
		Username: CurrentUser(r), ClientIP: clientIP(r), Action: "ADMIN_RESET_ADMIN_PASSWORD",
		Path: username, Success: true,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) deleteAdminUser(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	if username == CurrentUser(r) {
		writeJSONError(w, http.StatusBadRequest, codeBadRequest, "cannot delete your own account")
		return
	}
	existing, err := db.GetAdminUserByUsername(r.Context(), a.Pool, username)
	if errors.Is(err, db.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, codeNotFound, "admin not found")
		return
	} else if err != nil {
		writeJSONError(w, http.StatusInternalServerError, codeInternal, "internal")
		return
	}
	if isAdminRole(existing.Role) {
		n, err := db.CountAdminRoleUsers(r.Context(), a.Pool)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, codeInternal, "internal")
			return
		}
		if n <= 1 {
			writeJSONError(w, http.StatusConflict, codeConflict, "at least one admin account is required")
			return
		}
	}
	if err := db.DeleteAdminUser(r.Context(), a.Pool, username); err != nil {
		writeJSONError(w, http.StatusInternalServerError, codeInternal, "internal")
		return
	}
	a.Audit.Log(audit.Event{
		Username: CurrentUser(r), ClientIP: clientIP(r), Action: "ADMIN_DELETE_ADMIN",
		Path: username, Success: true,
	})
	w.WriteHeader(http.StatusNoContent)
}

func decodeAdminBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, codeBadRequest, "invalid request body")
		return false
	}
	return true
}

func normalizeAdminRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "admin", "superadmin":
		return "admin"
	case "readonly":
		return "readonly"
	default:
		return ""
	}
}

func isAdminRole(role string) bool {
	return strings.EqualFold(role, "admin") || strings.EqualFold(role, "SuperAdmin")
}
