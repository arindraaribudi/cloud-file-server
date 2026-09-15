package db

import (
	"context"
	"os"
	"testing"
)

func TestNewRequiresURL(t *testing.T) {
	if _, err := New(context.Background(), ""); err == nil {
		t.Fatal("expected error")
	}
}

func TestNewOpensPool(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
}