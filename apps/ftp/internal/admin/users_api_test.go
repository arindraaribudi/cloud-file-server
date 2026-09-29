package admin

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/ssh"

	"github.com/example/cos-ftp-server/internal/db"
)

func TestSetUserSFTPKey(t *testing.T) {
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
	_, _ = pool.Exec(ctx, `DELETE FROM ftp_users WHERE username = 'sftp-key-api-test'`)

	u := &db.FTPUser{Username: "sftp-key-api-test", PasswordHash: "x", RootFolder: "/sftp-key-api-test", COSBucket: "b", Enabled: true}
	id, err := db.CreateFTPUser(ctx, pool, u)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	defer func() { _ = db.SoftDeleteFTPUser(ctx, pool, id) }()

	api := &API{Pool: pool}
	r := chi.NewRouter()
	r.Post("/api/v1/users/{username}/sftp-key", api.setUserSFTPKey)

	_, pub, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	validKey := string(ssh.MarshalAuthorizedKey(sshPub))

	body, _ := json.Marshal(map[string]string{"public_key": validKey})
	req := httptest.NewRequest("POST", "/api/v1/users/sftp-key-api-test/sftp-key", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("valid key: status %d body %s", w.Code, w.Body.String())
	}
	got, err := db.GetFTPUserPublicKey(ctx, pool, "sftp-key-api-test")
	if err != nil {
		t.Fatal(err)
	}
	if got.SFTPPublicKey == "" {
		t.Fatal("expected key to be stored")
	}

	body, _ = json.Marshal(map[string]string{"public_key": "not-a-key"})
	req = httptest.NewRequest("POST", "/api/v1/users/sftp-key-api-test/sftp-key", bytes.NewReader(body))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid key: expected 400, got %d body %s", w.Code, w.Body.String())
	}

	body, _ = json.Marshal(map[string]string{"public_key": ""})
	req = httptest.NewRequest("POST", "/api/v1/users/sftp-key-api-test/sftp-key", bytes.NewReader(body))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("clear key: status %d body %s", w.Code, w.Body.String())
	}
	got2, err := db.GetFTPUserPublicKey(ctx, pool, "sftp-key-api-test")
	if err != nil {
		t.Fatal(err)
	}
	if got2.SFTPPublicKey != "" {
		t.Errorf("expected cleared key, got %q", got2.SFTPPublicKey)
	}
}

func TestCreateUser_ProtocolFlagsDefaults(t *testing.T) {
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
	_, _ = pool.Exec(ctx, `DELETE FROM ftp_users WHERE username = 'create-flags-test'`)

	api := &API{Pool: pool}
	r := chi.NewRouter()
	r.Post("/api/v1/users", api.createUser)

	body, _ := json.Marshal(map[string]any{
		"username": "create-flags-test", "root_folder": "/create-flags-test",
		"password": "Passw0rd1", "enabled": true,
	})
	req := httptest.NewRequest("POST", "/api/v1/users", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var created db.FTPUser
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.SoftDeleteFTPUser(ctx, pool, created.ID) }()
	if !created.FTPEnabled {
		t.Error("expected FTPEnabled default true when omitted")
	}
	if created.SFTPEnabled {
		t.Error("expected SFTPEnabled default false when omitted")
	}
}

func TestUpdateUser_ProtocolFlagsPreservedWhenOmitted(t *testing.T) {
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
	_, _ = pool.Exec(ctx, `DELETE FROM ftp_users WHERE username = 'update-flags-test'`)

	u := &db.FTPUser{Username: "update-flags-test", PasswordHash: "x", RootFolder: "/update-flags-test",
		COSBucket: "b", Enabled: true, FTPEnabled: true, SFTPEnabled: true}
	id, err := db.CreateFTPUser(ctx, pool, u)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.SoftDeleteFTPUser(ctx, pool, id) }()

	api := &API{Pool: pool}
	r := chi.NewRouter()
	r.Patch("/api/v1/users/{username}", api.updateUser)

	body, _ := json.Marshal(map[string]any{"root_folder": "/update-flags-test", "enabled": true})
	req := httptest.NewRequest("PATCH", "/api/v1/users/update-flags-test", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var updated db.FTPUser
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if !updated.SFTPEnabled {
		t.Error("expected SFTPEnabled preserved as true when omitted from the PATCH body")
	}
}
