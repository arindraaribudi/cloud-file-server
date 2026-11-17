package admin

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/example/cos-ftp-server/internal/audit"
	"github.com/example/cos-ftp-server/internal/auth"
	"github.com/example/cos-ftp-server/internal/db"
)

// authConfig is public so the login page can decide whether to show SSO,
// local password login, or both.
func (a *API) authConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{
		"local_login": a.LocalLogin,
		"oidc":        a.oidc != nil,
	})
}

func (a *API) localLogin(w http.ResponseWriter, r *http.Request) {
	if !a.LocalLogin {
		writeJSONError(w, http.StatusNotFound, codeNotFound, "local login is disabled")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, codeBadRequest, "invalid request body")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		writeJSONError(w, http.StatusBadRequest, codeBadRequest, "username and password are required")
		return
	}
	if a.LoginLockout != nil && !a.LoginLockout.Allow(req.Username) {
		writeJSONError(w, http.StatusTooManyRequests, "rate_limited", "too many failed attempts")
		return
	}

	u, err := db.GetAdminUserByUsername(r.Context(), a.Pool, req.Username)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		// A database problem is not a wrong password: report it as such and do
		// not count it toward the lockout, or an outage would lock everyone out.
		slog.Error("local login: admin lookup failed", "err", err)
		writeJSONError(w, http.StatusServiceUnavailable, codeUnavailable, "login is temporarily unavailable")
		return
	}
	if err != nil || !auth.VerifyPassword(u.PasswordHash, req.Password) {
		if a.LoginLockout != nil {
			a.LoginLockout.RecordFailure(req.Username)
		}
		a.Audit.Log(audit.Event{
			Username: req.Username, ClientIP: clientIP(r), Action: "ADMIN_LOGIN",
			Success: false, EventType: "admin",
			Detail: map[string]any{"method": "local", "reason": "invalid_credentials"},
		})
		writeJSONError(w, http.StatusUnauthorized, codeUnauthorized, "invalid username or password")
		return
	}

	role := "admin"
	if strings.EqualFold(u.Role, "readonly") {
		role = "readonly"
	}
	cookie, err := a.Sessions.Issue(u.Username, role, "local", "", "", "")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}
	if a.LoginLockout != nil {
		a.LoginLockout.Reset(req.Username)
	}
	a.Audit.Log(audit.Event{
		Username: u.Username, ClientIP: clientIP(r), Action: "ADMIN_LOGIN",
		Success: true, EventType: "admin",
		Detail: map[string]any{"method": "local"},
	})
	http.SetCookie(w, cookie)
	writeJSON(w, http.StatusOK, map[string]string{"username": u.Username, "role": role})
}
