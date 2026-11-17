package admin

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"

	"github.com/go-chi/chi/v5"
	"github.com/spf13/afero"

	"github.com/example/cos-ftp-server/internal/audit"
	"github.com/example/cos-ftp-server/internal/db"
)

const (
	codeUnauthorized = "unauthorized"
	codeForbidden    = "forbidden"
	codeConflict     = "conflict"
	codeBadRequest   = "bad_request"
	codeUnavailable  = "unavailable"

	maxInternalBodyBytes = 1 << 20
	maxFallbackCopyBytes = 256 << 20
)

var errCopyTooLarge = errors.New("file too large for in-process copy")

// RequireServiceToken protects the internal file-operations API. Only the
// workflow service (or another trusted service) can call these routes.
func (a *API) RequireServiceToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := a.WorkflowServiceToken
		if want == "" {
			writeJSONError(w, http.StatusServiceUnavailable, codeUnavailable, "workflow service token not configured")
			return
		}
		got := r.Header.Get("X-Workflow-Token")
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			writeJSONError(w, http.StatusUnauthorized, codeUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *API) internalFileRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(a.RequireServiceToken)
		r.Post("/internal/v1/files/rename", a.internalRename)
		r.Post("/internal/v1/files/remove", a.internalRemove)
		r.Post("/internal/v1/files/copy", a.internalCopy)
		r.Get("/internal/v1/files/stat", a.internalStat)
	})
}

type internalRenameRequest struct {
	Username  string `json:"username"`
	From      string `json:"from"`
	To        string `json:"to"`
	Overwrite bool   `json:"overwrite"`
}

type internalRemoveRequest struct {
	Username  string `json:"username"`
	Path      string `json:"path"`
	MissingOK bool   `json:"missing_ok"`
}

type internalCopyRequest struct {
	SourceUsername string `json:"source_username"`
	SourcePath     string `json:"source_path"`
	TargetUsername string `json:"target_username"`
	TargetPath     string `json:"target_path"`
	Overwrite      bool   `json:"overwrite"`
}

func (a *API) internalRename(w http.ResponseWriter, r *http.Request) {
	var req internalRenameRequest
	if !decodeInternalJSON(w, r, &req) {
		return
	}
	from, ok := sanitizeInternalPath(req.From)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, codeBadRequest, "invalid from path")
		return
	}
	to, ok := sanitizeInternalPath(req.To)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, codeBadRequest, "invalid to path")
		return
	}
	if from == to {
		writeJSONError(w, http.StatusBadRequest, codeBadRequest, "from and to are the same")
		return
	}
	u, ok := a.loadUserByUsername(w, r, req.Username)
	if !ok {
		return
	}
	fs, err := a.userFsFor(u)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, codeUnavailable, "storage unavailable")
		return
	}
	info, err := fs.Stat(from)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, codeNotFound, "file not found")
		return
	}
	if info.IsDir() {
		writeJSONError(w, http.StatusBadRequest, codeNotFile, "path is a directory")
		return
	}
	if !req.Overwrite {
		if _, err := fs.Stat(to); err == nil {
			writeJSONError(w, http.StatusConflict, codeConflict, "destination exists")
			return
		}
	}
	if err := fs.Rename(from, to); err != nil {
		a.auditWorkflow(r, "WORKFLOW_RENAME", u, to, false, map[string]any{"from": from, "to": to, "err": err.Error()})
		writeJSONError(w, http.StatusInternalServerError, codeInternal, "rename failed")
		return
	}
	a.auditWorkflow(r, "WORKFLOW_RENAME", u, to, true, map[string]any{"from": from, "to": to})
	writeJSON(w, http.StatusOK, map[string]string{"path": to})
}

func (a *API) internalRemove(w http.ResponseWriter, r *http.Request) {
	var req internalRemoveRequest
	if !decodeInternalJSON(w, r, &req) {
		return
	}
	p, ok := sanitizeInternalPath(req.Path)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, codeBadRequest, "invalid path")
		return
	}
	u, ok := a.loadUserByUsername(w, r, req.Username)
	if !ok {
		return
	}
	fs, err := a.userFsFor(u)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, codeUnavailable, "storage unavailable")
		return
	}
	info, err := fs.Stat(p)
	if err != nil {
		if req.MissingOK {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSONError(w, http.StatusNotFound, codeNotFound, "file not found")
		return
	}
	if info.IsDir() {
		writeJSONError(w, http.StatusBadRequest, codeNotFile, "path is a directory")
		return
	}
	if err := fs.Remove(p); err != nil {
		a.auditWorkflow(r, "WORKFLOW_DELETE", u, p, false, map[string]any{"err": err.Error()})
		writeJSONError(w, http.StatusInternalServerError, codeInternal, "remove failed")
		return
	}
	a.auditWorkflow(r, "WORKFLOW_DELETE", u, p, true, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) internalCopy(w http.ResponseWriter, r *http.Request) {
	var req internalCopyRequest
	if !decodeInternalJSON(w, r, &req) {
		return
	}
	srcPath, ok := sanitizeInternalPath(req.SourcePath)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, codeBadRequest, "invalid source_path")
		return
	}
	dstPath, ok := sanitizeInternalPath(req.TargetPath)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, codeBadRequest, "invalid target_path")
		return
	}
	srcUser, ok := a.loadUserByUsername(w, r, req.SourceUsername)
	if !ok {
		return
	}
	dstUser := srcUser
	if req.TargetUsername != "" && req.TargetUsername != srcUser.Username {
		var ok bool
		dstUser, ok = a.loadUserByUsername(w, r, req.TargetUsername)
		if !ok {
			return
		}
	}
	if srcUser.ID == dstUser.ID && srcPath == dstPath {
		writeJSONError(w, http.StatusBadRequest, codeBadRequest, "source and target are the same")
		return
	}
	srcFS, err := a.userFsFor(srcUser)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, codeUnavailable, "storage unavailable")
		return
	}
	info, err := srcFS.Stat(srcPath)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, codeNotFound, "source file not found")
		return
	}
	if info.IsDir() {
		writeJSONError(w, http.StatusBadRequest, codeNotFile, "source path is a directory")
		return
	}
	dstFS, err := a.userFsFor(dstUser)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, codeUnavailable, "storage unavailable")
		return
	}
	if !req.Overwrite {
		if _, err := dstFS.Stat(dstPath); err == nil {
			writeJSONError(w, http.StatusConflict, codeConflict, "destination exists")
			return
		}
	}

	// Same-backend copies use the backend's server-side copy. Anything else
	// (different backends, or a backend without Copier) streams through the
	// process via copyAfero. NOTE: the streaming path is size-capped and not
	// atomic — see docs/cross-backend-copy.md.
	handled := false
	if a.Storage != nil {
		handled, err = a.Storage.CopyNative(r.Context(), srcUser, dstUser, srcPath, dstPath)
	}
	if !handled && err == nil {
		err = copyAfero(srcFS, dstFS, srcPath, dstPath, info.Size())
	}
	if err != nil {
		status := http.StatusInternalServerError
		code := codeInternal
		if errors.Is(err, errCopyTooLarge) {
			status = http.StatusRequestEntityTooLarge
			code = codeBadRequest
		}
		a.auditWorkflow(r, "WORKFLOW_COPY", dstUser, dstPath, false, map[string]any{
			"source_username": srcUser.Username, "source_path": srcPath, "target_path": dstPath, "err": err.Error(),
		})
		writeJSONError(w, status, code, "copy failed")
		return
	}
	a.auditWorkflow(r, "WORKFLOW_COPY", dstUser, dstPath, true, map[string]any{
		"source_username": srcUser.Username, "source_path": srcPath, "target_path": dstPath,
	})
	writeJSON(w, http.StatusOK, map[string]string{"path": dstPath})
}

func (a *API) internalStat(w http.ResponseWriter, r *http.Request) {
	username := r.URL.Query().Get("username")
	p, ok := sanitizeInternalPath(r.URL.Query().Get("path"))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, codeBadRequest, "invalid path")
		return
	}
	u, ok := a.loadUserByUsername(w, r, username)
	if !ok {
		return
	}
	fs, err := a.userFsFor(u)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, codeUnavailable, "storage unavailable")
		return
	}
	info, err := fs.Stat(p)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, codeNotFound, "file not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"exists":   true,
		"is_dir":   info.IsDir(),
		"size":     info.Size(),
		"modified": info.ModTime().UTC(),
	})
}

func (a *API) loadUserByUsername(w http.ResponseWriter, r *http.Request, username string) (*db.FTPUser, bool) {
	if username == "" {
		writeJSONError(w, http.StatusBadRequest, codeBadRequest, "username required")
		return nil, false
	}
	u, err := db.GetFTPUserByUsername(r.Context(), a.Pool, username)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, codeNotFound, "user not found")
		return nil, false
	}
	return u, true
}

func decodeInternalJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxInternalBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, codeBadRequest, "invalid request body")
		return false
	}
	return true
}

func sanitizeInternalPath(raw string) (string, bool) {
	p, err := sanitizePath(raw)
	if err != nil || p == "" {
		return "", false
	}
	return p, true
}

func copyAfero(srcFS, dstFS afero.Fs, srcPath, dstPath string, size int64) error {
	if size > maxFallbackCopyBytes {
		return errCopyTooLarge
	}
	if dir := path.Dir(dstPath); dir != "." {
		if err := dstFS.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	in, err := srcFS.Open(srcPath)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := dstFS.Create(dstPath)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func (a *API) auditWorkflow(r *http.Request, action string, target *db.FTPUser, filePath string, success bool, detail map[string]any) {
	if detail == nil {
		detail = map[string]any{}
	}
	for key, header := range map[string]string{
		"workflow_id":  "X-Workflow-Id",
		"run_id":       "X-Workflow-Run-Id",
		"component_id": "X-Workflow-Component-Id",
	} {
		if v := r.Header.Get(header); v != "" {
			detail[key] = v
		}
	}
	backend := ""
	if a.Storage != nil {
		backend = a.Storage.BackendLocation(target)
	}
	a.Audit.Log(audit.Event{
		Username:        target.Username,
		ClientIP:        clientIP(r),
		Action:          action,
		EventType:       "workflow",
		ConnectionType:  "workflow",
		BackendLocation: backend,
		RootFolder:      target.RootFolder,
		Path:            filePath,
		Success:         success,
		Detail:          detail,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
