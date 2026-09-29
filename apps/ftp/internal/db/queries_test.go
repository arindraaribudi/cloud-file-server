package db

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestFTPUserCRUD(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM ftp_users WHERE username = 'tester'`)

	u := &FTPUser{
		Username:     "tester",
		PasswordHash: "$2a$12$....hashed....",
		RootFolder:   "/tester",
		COSBucket:    "bucket-1",
		Enabled:      true,
	}
	id, err := CreateFTPUser(ctx, pool, u)
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("id zero")
	}
	got, err := GetFTPUserByUsername(ctx, pool, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if got.RootFolder != "/tester" {
		t.Errorf("RootFolder=%q", got.RootFolder)
	}
	got.AllowActiveMode = true
	got.UpdatedAt = time.Now()
	if err := UpdateFTPUser(ctx, pool, got); err != nil {
		t.Fatal(err)
	}
	got2, _ := GetFTPUserByUsername(ctx, pool, "tester")
	if !got2.AllowActiveMode {
		t.Error("AllowActiveMode not persisted")
	}
	if err := SoftDeleteFTPUser(ctx, pool, id); err != nil {
		t.Fatal(err)
	}
}

func TestGetFTPUserByID(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM ftp_users WHERE username = 'getbyid-test'`)

	u := &FTPUser{
		Username:     "getbyid-test",
		PasswordHash: "x",
		RootFolder:   "getbyid-test",
		COSBucket:    "b",
		COSRegion:    "r",
		Enabled:      true,
	}
	id, err := CreateFTPUser(ctx, pool, u)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := GetFTPUserByID(ctx, pool, id)
	if err != nil {
		t.Fatalf("GetFTPUserByID: %v", err)
	}
	if got.ID != id || got.Username != "getbyid-test" || got.RootFolder != "getbyid-test" {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	_, err = GetFTPUserByID(ctx, pool, 999999)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for missing id, got %v", err)
	}

	if err := SoftDeleteFTPUser(ctx, pool, id); err != nil {
		t.Fatalf("SoftDeleteFTPUser: %v", err)
	}
	_, err = GetFTPUserByID(ctx, pool, id)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for soft-deleted id, got %v", err)
	}
}

func TestAdminUserCreate(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip()
	}
	pool, _ := New(context.Background(), dsn)
	defer pool.Close()
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM admin_users WHERE username = 'admin-test'`)
	id, err := CreateAdminUser(ctx, pool, "admin-test", "$2a$12$....", "SuperAdmin")
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("id zero")
	}
	got, err := GetAdminUserByUsername(ctx, pool, "admin-test")
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != "SuperAdmin" {
		t.Errorf("Role=%q", got.Role)
	}
}

func TestFTPUserProtocolFlagsDefaults(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM ftp_users WHERE username = 'protocol-flags-test'`)

	u := &FTPUser{
		Username:     "protocol-flags-test",
		PasswordHash: "x",
		RootFolder:   "/protocol-flags-test",
		COSBucket:    "b",
		Enabled:      true,
		FTPEnabled:   true,
	}
	id, err := CreateFTPUser(ctx, pool, u)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = SoftDeleteFTPUser(ctx, pool, id) }()

	got, err := GetFTPUserByUsername(ctx, pool, "protocol-flags-test")
	if err != nil {
		t.Fatal(err)
	}
	if !got.FTPEnabled {
		t.Error("expected FTPEnabled=true")
	}
	if got.SFTPEnabled {
		t.Error("expected SFTPEnabled=false by default")
	}
}

func TestSFTPPublicKeyRoundTrip(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM ftp_users WHERE username = 'sftp-key-test'`)

	u := &FTPUser{
		Username:     "sftp-key-test",
		PasswordHash: "x",
		RootFolder:   "/sftp-key-test",
		COSBucket:    "b",
		Enabled:      true,
		SFTPEnabled:  true,
	}
	id, err := CreateFTPUser(ctx, pool, u)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = SoftDeleteFTPUser(ctx, pool, id) }()

	got, err := GetFTPUserPublicKey(ctx, pool, "sftp-key-test")
	if err != nil {
		t.Fatal(err)
	}
	if got.SFTPPublicKey != "" {
		t.Errorf("expected empty key initially, got %q", got.SFTPPublicKey)
	}

	const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleNotARealKeyPadding0000 test@example"
	if err := SetFTPUserSFTPKey(ctx, pool, id, key); err != nil {
		t.Fatal(err)
	}
	got2, err := GetFTPUserPublicKey(ctx, pool, "sftp-key-test")
	if err != nil {
		t.Fatal(err)
	}
	if got2.SFTPPublicKey != key {
		t.Errorf("SFTPPublicKey=%q, want %q", got2.SFTPPublicKey, key)
	}

	if err := SetFTPUserSFTPKey(ctx, pool, id, ""); err != nil {
		t.Fatal(err)
	}
	got3, err := GetFTPUserPublicKey(ctx, pool, "sftp-key-test")
	if err != nil {
		t.Fatal(err)
	}
	if got3.SFTPPublicKey != "" {
		t.Errorf("expected cleared key, got %q", got3.SFTPPublicKey)
	}
}
