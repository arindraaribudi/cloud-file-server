package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/example/cos-ftp-server/internal/core"
	"github.com/example/cos-ftp-server/internal/db"
	"github.com/example/cos-ftp-server/internal/fsdriver"
)

// twoBackendRouter wires two LocalPlugins under different roots as stand-ins
// for the "cos" and "local" backends so the per-user routing can be exercised
// end to end without a COS bucket.
func twoBackendRouter(t *testing.T, def string) (r *core.StorageRouter, cosDir, localDir string) {
	t.Helper()
	cosDir, localDir = t.TempDir(), t.TempDir()
	r, err := core.NewStorageRouter(def, map[string]core.ObjectStorage{
		core.BackendCOS:   &fsdriver.LocalPlugin{Root: cosDir},
		core.BackendLocal: &fsdriver.LocalPlugin{Root: localDir},
	})
	if err != nil {
		t.Fatal(err)
	}
	return r, cosDir, localDir
}

func testPool(t *testing.T) (*API, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := db.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return &API{Pool: pool}, context.Background()
}

func postJSON(h http.Handler, method, url string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, url, bytes.NewReader(b))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestCreateUser_StorageBackend(t *testing.T) {
	api, ctx := testPool(t)
	store, cosDir, localDir := twoBackendRouter(t, core.BackendCOS)
	api.Storage = store
	api.COSBucket, api.COSRegion = "bkt", "reg"

	r := chi.NewRouter()
	r.Post("/api/v1/users", api.createUser)
	r.Patch("/api/v1/users/{username}", api.updateUser)

	for _, n := range []string{"sb-local", "sb-default", "sb-bad", "sb-escape"} {
		_, _ = api.Pool.Exec(ctx, `DELETE FROM ftp_users WHERE username=$1`, n)
	}

	create := func(name, backend, root string) *httptest.ResponseRecorder {
		return postJSON(r, "POST", "/api/v1/users", map[string]any{
			"username": name, "root_folder": root, "password": "Passw0rd1",
			"enabled": true, "storage_backend": backend,
		})
	}

	// explicit local
	w := create("sb-local", "local", "/sb-local")
	if w.Code != http.StatusOK {
		t.Fatalf("local create: %d %s", w.Code, w.Body)
	}
	var local db.FTPUser
	_ = json.Unmarshal(w.Body.Bytes(), &local)
	defer func() { _ = db.SoftDeleteFTPUser(ctx, api.Pool, local.ID) }()
	if local.StorageBackend != "local" {
		t.Errorf("response backend=%q, want local", local.StorageBackend)
	}
	if local.COSBucket != "" {
		t.Errorf("local user got cos_bucket %q, want empty", local.COSBucket)
	}
	if _, err := os.Stat(filepath.Join(localDir, "sb-local")); err != nil {
		t.Errorf("local root folder not created on local backend: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cosDir, "sb-local")); err == nil {
		t.Error("local user's folder leaked onto the cos backend")
	}

	// omitted -> deployment default (cos), stored explicitly, bucket recorded
	w = create("sb-default", "", "/sb-default")
	if w.Code != http.StatusOK {
		t.Fatalf("default create: %d %s", w.Code, w.Body)
	}
	var def db.FTPUser
	_ = json.Unmarshal(w.Body.Bytes(), &def)
	defer func() { _ = db.SoftDeleteFTPUser(ctx, api.Pool, def.ID) }()
	if def.StorageBackend != "cos" || def.COSBucket != "bkt" {
		t.Errorf("default user backend=%q bucket=%q", def.StorageBackend, def.COSBucket)
	}
	stored, err := db.GetFTPUserByUsername(ctx, api.Pool, "sb-default")
	if err != nil || stored.StorageBackend != "cos" {
		t.Errorf("stored backend=%q err=%v, want explicit cos", stored.StorageBackend, err)
	}

	// unknown backend and path escape are rejected, no row created
	if w = create("sb-bad", "s3", "/sb-bad"); w.Code != http.StatusBadRequest {
		t.Errorf("unknown backend: %d, want 400", w.Code)
	}
	if w = create("sb-escape", "local", "/../escape"); w.Code != http.StatusBadRequest {
		t.Errorf("escaping root: %d, want 400", w.Code)
	}
	for _, n := range []string{"sb-bad", "sb-escape"} {
		if _, err := db.GetFTPUserByUsername(ctx, api.Pool, n); err == nil {
			t.Errorf("%s was created despite rejection", n)
		}
	}

	// backend is immutable after creation
	w = postJSON(r, "PATCH", "/api/v1/users/sb-local", map[string]any{
		"root_folder": "/sb-local", "enabled": true, "storage_backend": "cos",
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("changing backend: %d, want 400", w.Code)
	}
	// same backend value, or omitted, is fine
	for _, b := range []string{"local", ""} {
		w = postJSON(r, "PATCH", "/api/v1/users/sb-local", map[string]any{
			"root_folder": "/sb-local", "enabled": true, "storage_backend": b,
		})
		if w.Code != http.StatusOK {
			t.Errorf("update with backend %q: %d %s", b, w.Code, w.Body)
		}
	}
}

func TestLegacyUserFollowsDeploymentDefault(t *testing.T) {
	api, ctx := testPool(t)
	// A row created without storage_backend (pre-migration shape) must resolve
	// to whatever the deployment default is.
	_, _ = api.Pool.Exec(ctx, `DELETE FROM ftp_users WHERE username='sb-legacy'`)
	id, err := db.CreateFTPUser(ctx, api.Pool, &db.FTPUser{Username: "sb-legacy", PasswordHash: "x", RootFolder: "/sb-legacy", COSBucket: "b", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.SoftDeleteFTPUser(ctx, api.Pool, id) }()
	u, err := db.GetFTPUserByUsername(ctx, api.Pool, "sb-legacy")
	if err != nil || u.StorageBackend != "" {
		t.Fatalf("raw backend=%q err=%v, want empty", u.StorageBackend, err)
	}
	store, _, localDir := twoBackendRouter(t, core.BackendLocal)
	if got := store.Resolve(u.StorageBackend); got != "local" {
		t.Errorf("resolved=%q, want local", got)
	}
	api.Storage = store
	if _, err := api.userFsFor(u); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(localDir, "sb-legacy")); err != nil {
		t.Errorf("legacy user not mounted on default backend: %v", err)
	}
}

// Cross-backend workflow copy must still work (streamed), and same-backend
// copy must take the native path. See docs/cross-backend-copy.md.
func TestInternalCopy_CrossBackend(t *testing.T) {
	api, ctx := testPool(t)
	store, cosDir, localDir := twoBackendRouter(t, core.BackendCOS)
	api.Storage = store
	api.WorkflowServiceToken = "secret"

	mk := func(name, backend string) {
		_, _ = api.Pool.Exec(ctx, `DELETE FROM ftp_users WHERE username=$1`, name)
		id, err := db.CreateFTPUser(ctx, api.Pool, &db.FTPUser{Username: name, PasswordHash: "x", RootFolder: "/" + name, COSBucket: "b", Enabled: true, StorageBackend: backend})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.SoftDeleteFTPUser(ctx, api.Pool, id) })
	}
	mk("cp-cos", "cos")
	mk("cp-cos2", "cos")
	mk("cp-local", "local")

	if err := os.MkdirAll(filepath.Join(cosDir, "cp-cos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cosDir, "cp-cos", "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	router := internalTestRouter(api, http.MethodPost, "/internal/v1/files/copy", api.internalCopy)
	do := func(target string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]any{
			"source_username": "cp-cos", "source_path": "a.txt",
			"target_username": target, "target_path": "out/a.txt",
		})
		req := httptest.NewRequest(http.MethodPost, "/internal/v1/files/copy", bytes.NewReader(b))
		req.Header.Set("X-Workflow-Token", "secret")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	// cos -> local (cross-backend, streamed)
	if w := do("cp-local"); w.Code != http.StatusOK {
		t.Fatalf("cross-backend copy: %d %s", w.Code, w.Body)
	}
	got, err := os.ReadFile(filepath.Join(localDir, "cp-local", "out", "a.txt"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("cross-backend result %q err=%v", got, err)
	}
	// cos -> cos (same backend, native Copier)
	if w := do("cp-cos2"); w.Code != http.StatusOK {
		t.Fatalf("same-backend copy: %d %s", w.Code, w.Body)
	}
	got, err = os.ReadFile(filepath.Join(cosDir, "cp-cos2", "out", "a.txt"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("same-backend result %q err=%v", got, err)
	}
}
