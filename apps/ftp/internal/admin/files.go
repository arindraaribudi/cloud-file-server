package admin

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/spf13/afero"

	"github.com/example/cos-ftp-server/internal/audit"
	"github.com/example/cos-ftp-server/internal/db"
	"github.com/example/cos-ftp-server/internal/fsdriver"
)

// Wire codes returned to the frontend in {"code":"..."}. Single source of
// truth: the error sentinels below are built from these same strings.
const (
	codeBadPath  = "bad_path"
	codeNotFound = "not_found"
	codeNotFile  = "not_file"
	codeInternal = "internal"
)

var (
	errBadPath = errors.New(codeBadPath)
	errNotFile = errors.New(codeNotFile)
)

// sanitizePath normalizes a user-supplied path. Empty input or "/" returns the
// empty root path. Returns errBadPath if the path contains any ".." segment,
// including non-escaping ones such as "foo/../bar".
func sanitizePath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", errBadPath
		}
	}
	clean := path.Clean("/" + p) // force rooted, collapse "." / "foo//bar"
	if clean == "/" {
		return "", nil
	}
	return strings.TrimPrefix(clean, "/"), nil
}

// writeJSONError writes a JSON error response: {"error":"...","code":"..."}
func writeJSONError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg, "code": code})
}

// memFsForTest lets tests inject an afero.Fs in place of the live COS
// driver. nil in production.
var memFsForTest afero.Fs

// errNoCOS reports that the server has no COS client configured.
var errNoCOS = errors.New("COS client not configured (server missing COS credentials)")

// userFsFor returns an afero.Fs rooted at the user's COS folder.
func (a *API) userFsFor(u *db.FTPUser) (afero.Fs, error) {
	if memFsForTest != nil {
		return memFsForTest, nil
	}
	if a.COSClient == nil {
		return nil, errNoCOS
	}
	return fsdriver.NewCOS(u.RootFolder, a.COSClient), nil
}

// loadUser parses {userId} from URL and loads the FTPUser.
func (a *API) loadUser(w http.ResponseWriter, r *http.Request) (*db.FTPUser, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "userId"), 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, codeNotFound, "user not found")
		return nil, false
	}
	u, err := db.GetFTPUserByID(r.Context(), a.Pool, id)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, codeNotFound, "user not found")
		return nil, false
	}
	return u, true
}

// readDir prefers an Fs's own ReadDir when it has one. afero.ReadDir is a
// package helper that does Open+Readdir, which bypasses COS.ReadDir — the
// only place the COS prefix List actually happens. Mirrors AuditFS.ReadDir.
func readDir(fs afero.Fs, name string) ([]os.FileInfo, error) {
	if lister, ok := fs.(interface {
		ReadDir(string) ([]os.FileInfo, error)
	}); ok {
		return lister.ReadDir(name)
	}
	return afero.ReadDir(fs, name)
}

// sortEntries sorts dirs first then alpha ascending.
func sortEntries(es []os.FileInfo) {
	sort.Slice(es, func(i, j int) bool {
		if es[i].IsDir() != es[j].IsDir() {
			return es[i].IsDir()
		}
		return es[i].Name() < es[j].Name()
	})
}

type listRespBody struct {
	Prefix  string         `json:"prefix"`
	Entries []listEntryOut `json:"entries"`
}

type listEntryOut struct {
	Name     string    `json:"name"`
	IsDir    bool      `json:"isDir"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

// listFiles lists one directory level of the user's folder. The caller supplies
// the already-loaded user; wiring from {userId} lives in the router.
func (a *API) listFiles(w http.ResponseWriter, r *http.Request, u *db.FTPUser) {
	rel, err := sanitizePath(r.URL.Query().Get("prefix"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, codeBadPath, "invalid prefix")
		return
	}
	fs, err := a.userFsFor(u)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, codeInternal, err.Error())
		return
	}
	infos, err := readDir(fs, rel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSONError(w, http.StatusNotFound, codeNotFound, "folder not found")
			return
		}
		// Never surface err.Error() to the client: COS errors can carry
		// bucket names, hostnames and credential hints.
		writeJSONError(w, http.StatusInternalServerError, codeInternal, "storage error")
		return
	}
	sortEntries(infos)
	out := make([]listEntryOut, 0, len(infos))
	for _, fi := range infos {
		out = append(out, listEntryOut{
			Name:     fi.Name(),
			IsDir:    fi.IsDir(),
			Size:     fi.Size(),
			Modified: fi.ModTime().UTC(),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(listRespBody{Prefix: rel, Entries: out})
}

// downloadFile streams one file from the user's folder.
//
// IMPORTANT: every error response MUST be written BEFORE the first body byte.
// Once io.Copy starts the status and headers are committed — calling
// writeJSONError after that logs "superfluous response.WriteHeader call" and
// appends JSON onto a truncated file, producing a corrupt download. On a
// mid-stream failure, just return.
func (a *API) downloadFile(w http.ResponseWriter, r *http.Request, u *db.FTPUser) {
	rel, err := sanitizePath(r.URL.Query().Get("path"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, codeBadPath, "invalid path")
		return
	}
	fs, err := a.userFsFor(u)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, codeInternal, err.Error())
		return
	}
	info, err := fs.Stat(rel)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, codeNotFound, "file not found")
		return
	}
	if info.IsDir() {
		writeJSONError(w, http.StatusBadRequest, codeNotFile, "path is a directory")
		return
	}
	f, err := fs.Open(rel)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, codeNotFound, "file not found")
		return
	}
	defer f.Close()

	name := info.Name()
	// Always attachment — no inline mode. Content-Type is extension-derived
	// (mimeByName) with no CSP/nosniff in this codebase, so inline rendering
	// of an attacker-uploaded .html/.svg would execute same-origin.
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Content-Type", mimeByName(name))
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))

	// Past this point the response is committed — no writeJSONError.
	if _, err := io.Copy(w, f); err != nil {
		return // client aborted mid-stream; no audit event
	}

	a.emitDownloadAudit(r, u, rel, info.Size())
}

// auditHookForTest lets tests observe emitDownloadAudit calls without a real
// audit.Logger. nil in production.
var auditHookForTest func(audit.Event)

// emitDownloadAudit logs a FILE_DOWNLOAD event. Path is prefixed with the
// target username so it's visible in the audit list/CSV export, which don't
// surface Detail.
func (a *API) emitDownloadAudit(r *http.Request, target *db.FTPUser, path string, size int64) {
	e := audit.Event{
		Username: CurrentUser(r),
		ClientIP: clientIP(r),
		Action:   "FILE_DOWNLOAD",
		Path:     target.Username + ":" + path,
		Bytes:    size,
		Success:  true,
		Detail: map[string]any{
			"target_user_id":  target.ID,
			"target_username": target.Username,
		},
	}
	if auditHookForTest != nil {
		auditHookForTest(e)
		return
	}
	a.Audit.Log(e)
}

func clientIP(r *http.Request) net.IP {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if ip := net.ParseIP(strings.TrimSpace(strings.Split(v, ",")[0])); ip != nil {
			return ip
		}
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return net.ParseIP(host)
}

func mimeByName(name string) string {
	if t := mime.TypeByExtension(strings.ToLower(path.Ext(name))); t != "" {
		return t
	}
	return "application/octet-stream"
}

func (a *API) listFilesRoute(w http.ResponseWriter, r *http.Request) {
	u, ok := a.loadUser(w, r)
	if !ok {
		return
	}
	a.listFiles(w, r, u)
}

func (a *API) downloadFileRoute(w http.ResponseWriter, r *http.Request) {
	u, ok := a.loadUser(w, r)
	if !ok {
		return
	}
	a.downloadFile(w, r, u)
}
