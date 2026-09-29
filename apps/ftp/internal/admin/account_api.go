package admin

import (
	"encoding/json"
	"net/http"

	"github.com/example/cos-ftp-server/internal/audit"
)

// session confirms the caller's cookie is still valid and reports who they are.
func (a *API) session(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"username":            CurrentUser(r),
		"role":                CurrentRole(r),
		"email":               CurrentEmail(r),
		"first_name":          CurrentFirstName(r),
		"last_name":           CurrentLastName(r),
		"ftp_enabled":         a.FTPEnabled,
		"ftp_address":         a.FTPAddress,
		"ftp_public_address":  a.FTPPublicAddress,
		"sftp_enabled":        a.SFTPEnabled,
		"sftp_address":        a.SFTPAddress,
		"sftp_public_address": a.SFTPPublicAddress,
		"cos_address":         "https://" + a.COSBucket + ".cos." + a.COSRegion + ".myqcloud.com",
	})
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	a.Audit.Log(audit.Event{Username: CurrentUser(r), ClientIP: clientIP(r), Action: "ADMIN_LOGOUT", Success: true})
	if c, err := r.Cookie("session"); err == nil {
		a.Sessions.Revoke(c.Value)
	}
	http.SetCookie(w, a.Sessions.ClearCookie())
	w.WriteHeader(http.StatusNoContent)
}
