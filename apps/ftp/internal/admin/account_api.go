package admin

import (
	"encoding/json"
	"net/http"

	"github.com/example/cos-ftp-server/internal/audit"
	"github.com/example/cos-ftp-server/internal/core"
)

// session confirms the caller's cookie is still valid and reports who they are.
func (a *API) session(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	backends, def := []string{}, ""
	cosAddress := "https://" + a.COSBucket + ".cos." + a.COSRegion + ".myqcloud.com"
	if a.Storage != nil {
		backends, def = a.Storage.Enabled(), a.Storage.Default()
		if !a.Storage.Has(core.BackendCOS) {
			cosAddress = ""
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"storage_backends":        backends,
		"default_storage_backend": def,
		"username":                CurrentUser(r),
		"role":                    CurrentRole(r),
		"email":                   CurrentEmail(r),
		"first_name":              CurrentFirstName(r),
		"last_name":               CurrentLastName(r),
		"ftp_enabled":             a.FTPEnabled,
		"ftp_address":             a.FTPAddress,
		"ftp_public_address":      a.FTPPublicAddress,
		"sftp_enabled":            a.SFTPEnabled,
		"sftp_address":            a.SFTPAddress,
		"sftp_public_address":     a.SFTPPublicAddress,
		"cos_address":             cosAddress,
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
