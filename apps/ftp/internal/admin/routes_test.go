package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/spf13/afero"

	"github.com/example/cos-ftp-server/internal/db"
)

// TestFileRoutes_UserIDWiring proves {userId} flows from the URL through
// chi.URLParam, loadUser, and db.GetFTPUserByID to the file handlers — the
// unit tests in files_test.go call listFiles/downloadFile directly and never
// exercise that chain. Covers both the list route and the download route.
// Requires a real Postgres (TEST_DATABASE_URL), mirroring the skip idiom
// used by internal/db's tests.
func TestFileRoutes_UserIDWiring(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := db.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM ftp_users WHERE username = 'routes-wiring-test'`)

	u := &db.FTPUser{
		Username:     "routes-wiring-test",
		PasswordHash: "x",
		RootFolder:   "routes-wiring-test",
		COSBucket:    "b",
		COSRegion:    "r",
		Enabled:      true,
	}
	id, err := db.CreateFTPUser(ctx, pool, u)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	defer func() { _ = db.SoftDeleteFTPUser(ctx, pool, id) }()

	fs := afero.NewMemMapFs()
	f, _ := fs.Create("hello.txt")
	_, _ = f.WriteString("hi")
	_ = f.Close()
	memFsForTest = fs
	defer func() { memFsForTest = nil }()

	api := &API{Pool: pool}
	r := chi.NewRouter()
	r.Get("/api/v1/files/{userId}", api.listFilesRoute)
	r.Get("/api/v1/files/{userId}/download", api.downloadFileRoute)

	// Known user ID: chi.URLParam extraction + loadUser + listFiles all wired.
	req := httptest.NewRequest("GET", "/api/v1/files/"+strconv.FormatInt(id, 10), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("known user: status %d body %s", w.Code, w.Body.String())
	}
	var resp listRespBody
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Entries) != 1 || resp.Entries[0].Name != "hello.txt" {
		t.Errorf("expected hello.txt listed, got %+v", resp.Entries)
	}

	// Nonexistent user ID: loadUser's db lookup fails -> 404, proving the
	// path param is actually read (not just accepted and ignored).
	req = httptest.NewRequest("GET", "/api/v1/files/999999999", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("nonexistent user: expected 404, got %d body %s", w.Code, w.Body.String())
	}

	// Known user ID: chi.URLParam extraction + loadUser + downloadFile all wired.
	req = httptest.NewRequest("GET", "/api/v1/files/"+strconv.FormatInt(id, 10)+"/download?path=hello.txt", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("known user download: status %d body %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Disposition") == "" {
		t.Error("expected Content-Disposition header on download")
	}
	if w.Body.String() != "hi" {
		t.Errorf("expected downloaded body %q, got %q", "hi", w.Body.String())
	}

	// Nonexistent user ID: loadUser's db lookup fails -> 404, proving the
	// download route also reads chi.URLParam and doesn't just always succeed.
	req = httptest.NewRequest("GET", "/api/v1/files/999999999/download?path=whatever", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("nonexistent user download: expected 404, got %d body %s", w.Code, w.Body.String())
	}
}
