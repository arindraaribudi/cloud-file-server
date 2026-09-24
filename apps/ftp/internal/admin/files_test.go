package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/example/cos-ftp-server/internal/audit"
	"github.com/example/cos-ftp-server/internal/db"
)

func TestSanitizePath(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "", false},
		{"/", "", false},
		{"foo", "foo", false},
		{"foo/bar", "foo/bar", false},
		{"/foo/bar/", "foo/bar", false},
		{"foo/./bar", "foo/bar", false},
		{"../etc", "", true},
		{"foo/../../bar", "", true},
		{"foo/../bar", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := sanitizePath(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

// listResp is the decode target for listFiles responses. It uses an anonymous
// inner struct so it does not collide with the production listEntryOut.
type listResp struct {
	Prefix  string `json:"prefix"`
	Entries []struct {
		Name     string    `json:"name"`
		IsDir    bool      `json:"isDir"`
		Size     int64     `json:"size"`
		Modified time.Time `json:"modified"`
	} `json:"entries"`
}

// newListTestAPI builds an API with a nil Pool: listFiles takes the *db.FTPUser
// directly, and only loadUser touches the Pool.
func newListTestAPI(t *testing.T) (*API, *db.FTPUser) {
	t.Helper()
	u := &db.FTPUser{
		ID:         1,
		Username:   "alice",
		RootFolder: "alice",
		COSBucket:  "b",
		COSRegion:  "r",
		Enabled:    true,
	}
	return &API{}, u
}

func TestListFiles_EmptyRoot(t *testing.T) {
	api, u := newListTestAPI(t)
	memFsForTest = afero.NewMemMapFs()
	defer func() { memFsForTest = nil }()

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	api.listFiles(w, req, u)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var resp listResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Prefix != "" || len(resp.Entries) != 0 {
		t.Errorf("expected empty root listing, got %+v", resp)
	}
	// Decoding into a slice makes [] and null indistinguishable, so assert on
	// the raw bytes: the frontend maps over entries and breaks on null.
	if body := w.Body.String(); !strings.Contains(body, `"entries":[]`) {
		t.Errorf("entries must serialize as [] not null, got %s", body)
	}
}

// directListerFs implements ReadDir directly, like fsdriver.COS does. listFiles
// must dispatch to it: afero.ReadDir is a package helper doing Open+Readdir,
// which on COS hits the root placeholder object and returns an empty listing.
// The embedded Fs is deliberately EMPTY, mirroring COS: an Open+Readdir on the
// root placeholder yields nothing, so only the direct ReadDir can produce the
// sentinel.
type directListerFs struct {
	afero.Fs
	entries []os.FileInfo
}

func (d directListerFs) ReadDir(string) ([]os.FileInfo, error) { return d.entries, nil }

func TestListFiles_UsesDirectReadDir(t *testing.T) {
	api, u := newListTestAPI(t)
	side := afero.NewMemMapFs()
	f, _ := side.Create("sentinel-from-readdir")
	_ = f.Close()
	fi, err := side.Stat("sentinel-from-readdir")
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	memFsForTest = directListerFs{Fs: afero.NewMemMapFs(), entries: []os.FileInfo{fi}}
	defer func() { memFsForTest = nil }()

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	api.listFiles(w, req, u)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var resp listResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Entries) != 1 || resp.Entries[0].Name != "sentinel-from-readdir" {
		t.Fatalf("listFiles bypassed the Fs's own ReadDir, got %+v", resp.Entries)
	}
}

// Modified must be UTC on the wire regardless of the stored mtime's zone.
func TestListFiles_ModifiedIsUTC(t *testing.T) {
	api, u := newListTestAPI(t)
	fs := afero.NewMemMapFs()
	f, _ := fs.Create("a.txt")
	_ = f.Close()
	off := time.FixedZone("x", 7*3600)
	if err := fs.Chtimes("a.txt", time.Now().In(off), time.Date(2024, 3, 1, 12, 0, 0, 0, off)); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	memFsForTest = fs
	defer func() { memFsForTest = nil }()

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	api.listFiles(w, req, u)

	var resp listResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %+v", resp.Entries)
	}
	if loc := resp.Entries[0].Modified.Location(); loc != time.UTC {
		t.Errorf("modified location=%v want UTC", loc)
	}
	if !strings.Contains(w.Body.String(), `2024-03-01T05:00:00Z`) {
		t.Errorf("modified must serialize as UTC with Z suffix, got %s", w.Body.String())
	}
}

func TestListFiles_MixedDirAndFile(t *testing.T) {
	api, u := newListTestAPI(t)
	fs := afero.NewMemMapFs()
	// "zzz" sorts AFTER "aaa.txt" alphabetically, so dirs-first is the only
	// thing that can put it at index 0. Same-name-order fixtures pass even
	// with sortEntries removed.
	_ = fs.MkdirAll("zzz", 0o755)
	f, _ := fs.Create("aaa.txt")
	_, _ = f.WriteString("hi")
	_ = f.Close()
	memFsForTest = fs
	defer func() { memFsForTest = nil }()

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	api.listFiles(w, req, u)

	var resp listResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body=%q)", err, w.Body.String())
	}
	if len(resp.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(resp.Entries), resp.Entries)
	}
	if !resp.Entries[0].IsDir || resp.Entries[0].Name != "zzz" {
		t.Errorf("first entry should be dir 'zzz', got %+v", resp.Entries[0])
	}
	if resp.Entries[1].IsDir || resp.Entries[1].Name != "aaa.txt" || resp.Entries[1].Size != 2 {
		t.Errorf("second entry should be file 'aaa.txt' size 2, got %+v", resp.Entries[1])
	}
}

func TestListFiles_BadPrefix(t *testing.T) {
	api, u := newListTestAPI(t)
	req := httptest.NewRequest("GET", "/?prefix=../etc", nil)
	w := httptest.NewRecorder()
	api.listFiles(w, req, u)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// Regression: a nil COSClient must fail loudly, not serve an empty listing.
func TestListFiles_NoCOSClient(t *testing.T) {
	api, u := newListTestAPI(t)
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	api.listFiles(w, req, u)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

// A missing folder must be 404, not 500: the frontend renders not_found as
// "not found" with a link back, while internal means a storage outage.
func TestListFiles_MissingDir(t *testing.T) {
	api, u := newListTestAPI(t)
	memFsForTest = afero.NewMemMapFs()
	defer func() { memFsForTest = nil }()

	req := httptest.NewRequest("GET", "/?prefix=nope", nil)
	w := httptest.NewRecorder()
	api.listFiles(w, req, u)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d body %s", w.Code, w.Body.String())
	}
}

func TestDownloadFile_Success(t *testing.T) {
	api, u := newListTestAPI(t)
	fs := afero.NewMemMapFs()
	f, _ := fs.Create("hello.txt")
	_, _ = f.WriteString("hello world")
	_ = f.Close()
	memFsForTest = fs
	defer func() { memFsForTest = nil }()

	req := httptest.NewRequest("GET", "/?path=hello.txt", nil)
	w := httptest.NewRecorder()
	api.downloadFile(w, req, u)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Disposition"); !strings.Contains(got, "attachment") || !strings.Contains(got, "hello.txt") {
		t.Errorf("bad Content-Disposition: %q", got)
	}
	if w.Body.String() != "hello world" {
		t.Errorf("body mismatch: %q", w.Body.String())
	}
}

func TestDownloadFile_ContentLengthMatchesBody(t *testing.T) {
	api, u := newListTestAPI(t)
	fs := afero.NewMemMapFs()
	f, _ := fs.Create("hello.txt")
	_, _ = f.WriteString("hello world")
	_ = f.Close()
	memFsForTest = fs
	defer func() { memFsForTest = nil }()

	req := httptest.NewRequest("GET", "/?path=hello.txt", nil)
	w := httptest.NewRecorder()
	api.downloadFile(w, req, u)

	wantLen := strconv.Itoa(w.Body.Len())
	if got := w.Header().Get("Content-Length"); got != wantLen {
		t.Errorf("Content-Length=%q want %q", got, wantLen)
	}
}

func TestDownloadFile_DirectoryRejected(t *testing.T) {
	api, u := newListTestAPI(t)
	fs := afero.NewMemMapFs()
	_ = fs.MkdirAll("docs", 0o755)
	memFsForTest = fs
	defer func() { memFsForTest = nil }()

	req := httptest.NewRequest("GET", "/?path=docs", nil)
	w := httptest.NewRecorder()
	api.downloadFile(w, req, u)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestDownloadFile_TraversalRejected(t *testing.T) {
	api, u := newListTestAPI(t)
	req := httptest.NewRequest("GET", "/?path=../etc/passwd", nil)
	w := httptest.NewRecorder()
	api.downloadFile(w, req, u)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestDownloadFile_NotFound(t *testing.T) {
	api, u := newListTestAPI(t)
	memFsForTest = afero.NewMemMapFs()
	defer func() { memFsForTest = nil }()

	req := httptest.NewRequest("GET", "/?path=nope.txt", nil)
	w := httptest.NewRecorder()
	api.downloadFile(w, req, u)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestDownloadFile_AuditsOnSuccess(t *testing.T) {
	api, u := newListTestAPI(t)
	fs := afero.NewMemMapFs()
	f, _ := fs.Create("hello.txt")
	_, _ = f.WriteString("hello world")
	_ = f.Close()
	memFsForTest = fs
	defer func() { memFsForTest = nil }()

	var got *audit.Event
	auditHookForTest = func(e audit.Event) { got = &e }
	defer func() { auditHookForTest = nil }()

	req := httptest.NewRequest("GET", "/?path=hello.txt", nil)
	w := httptest.NewRecorder()
	api.downloadFile(w, req, u)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if got == nil {
		t.Fatal("expected audit event to fire, got none")
	}
	if got.Path != "alice:hello.txt" || got.Bytes != int64(len("hello world")) || !got.Success {
		t.Errorf("audit event mismatch: %+v", got)
	}
	if got.Detail["target_user_id"] != u.ID {
		t.Errorf("audit event missing target_user_id: %+v", got.Detail)
	}
}

func TestDownloadFile_NoAuditOnNotFound(t *testing.T) {
	api, u := newListTestAPI(t)
	memFsForTest = afero.NewMemMapFs()
	defer func() { memFsForTest = nil }()

	fired := false
	auditHookForTest = func(audit.Event) { fired = true }
	defer func() { auditHookForTest = nil }()

	req := httptest.NewRequest("GET", "/?path=nope.txt", nil)
	w := httptest.NewRecorder()
	api.downloadFile(w, req, u)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d", w.Code)
	}
	if fired {
		t.Error("audit should not fire on 404")
	}
}

func TestDownloadFile_NoAuditOnTraversal(t *testing.T) {
	api, u := newListTestAPI(t)

	fired := false
	auditHookForTest = func(audit.Event) { fired = true }
	defer func() { auditHookForTest = nil }()

	req := httptest.NewRequest("GET", "/?path=../etc/passwd", nil)
	w := httptest.NewRecorder()
	api.downloadFile(w, req, u)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
	if fired {
		t.Error("audit should not fire on rejected traversal")
	}
}

func TestDownloadFile_NoCOSClient(t *testing.T) {
	api, u := newListTestAPI(t)
	req := httptest.NewRequest("GET", "/?path=hello.txt", nil)
	w := httptest.NewRecorder()
	api.downloadFile(w, req, u)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestWriteJSONError(t *testing.T) {
	tests := []struct {
		name   string
		status int
		code   string
		msg    string
	}{
		{"plain", http.StatusNotFound, codeNotFound, "user not found"},
		{"quotes", http.StatusBadRequest, codeBadPath, `he said "hi"`},
		// %q would emit \x7f here, which is not valid JSON.
		{"control byte", http.StatusInternalServerError, codeInternal, "bad\x7fbyte"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeJSONError(rec, tc.status, tc.code, tc.msg)

			if rec.Code != tc.status {
				t.Errorf("status=%d want %d", rec.Code, tc.status)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type=%q want application/json", ct)
			}

			var got map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("body is not valid JSON: %v (body=%q)", err, rec.Body.String())
			}
			if got["error"] != tc.msg {
				t.Errorf("error=%q want %q", got["error"], tc.msg)
			}
			if got["code"] != tc.code {
				t.Errorf("code=%q want %q", got["code"], tc.code)
			}
		})
	}
}
