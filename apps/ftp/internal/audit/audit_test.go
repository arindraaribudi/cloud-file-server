package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/example/cos-ftp-server/internal/db"
)

func TestLoggerWritesRows(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := db.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	l := New(pool, log)
	defer l.Close(context.Background())

	ctx := context.Background()
	uniq := fmt.Sprintf("test-%d", time.Now().UnixNano())
	_, _ = pool.Exec(ctx, `DELETE FROM audit_events WHERE username = $1`, uniq)

	e := Event{
		EventTime: time.Now().UTC(),
		Username:  uniq,
		Action:    "TEST_ACTION",
		Success:   true,
		Detail:    map[string]any{"k": "v"},
	}
	l.Log(e)

	if err := l.Close(ctx); err != nil {
		t.Fatal(err)
	}

	var count int
	err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_events WHERE username=$1`, uniq).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 audit row, got %d", count)
	}

	_, _ = pool.Exec(ctx, `DELETE FROM audit_events WHERE username = $1`, uniq)
}

func TestEncodeEvent(t *testing.T) {
	e := Event{
		EventTime: time.Now().UTC(),
		Username:  "alice",
		ClientIP:  net.ParseIP("10.0.0.1"),
		Action:    "LOGIN",
		Success:   true,
		Detail:    map[string]any{"cipher": "TLS_AES_128_GCM_SHA256"},
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"action":"LOGIN"`) {
		t.Errorf("missing action: %s", b)
	}
}
