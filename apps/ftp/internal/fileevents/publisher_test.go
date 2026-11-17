package fileevents

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/example/cos-ftp-server/internal/db"
)

func TestPublisherPublishIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	if err := db.Migrate(dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()
	pool, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	username := "fileevent-test-" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM file_events WHERE username = $1", username)
	})

	p := NewPublisher(pool, slog.Default(), MIMEFallback, 1<<20)
	session := uuid.New()
	p.Publish(ctx, Event{
		EventType:       EventUpload,
		UserID:          1,
		Username:        username,
		RootFolder:      "/" + username,
		ConnectionType:  "ftp",
		BackendLocation: "test",
		Path:            "invoice.txt",
		Size:            5,
		MIME:            "text/plain",
		MIMESource:      MIMESourceExtension,
		Overwrite:       true,
		SessionID:       session,
	})

	var (
		eventType, uname, root, conn, backend, path, mime, mimeSource string
		userID                                                        int64
		size                                                          int64
		overwrite                                                     bool
		gotSession                                                    uuid.UUID
		dispatchedAt                                                  *time.Time
	)
	err = pool.QueryRow(ctx, `
		SELECT event_type, user_id, username, root_folder, connection_type,
		       backend_location, path, size, mime, mime_source, overwrite,
		       session_id, dispatched_at
		FROM file_events WHERE username = $1 ORDER BY id DESC LIMIT 1`, username).
		Scan(&eventType, &userID, &uname, &root, &conn, &backend, &path,
			&size, &mime, &mimeSource, &overwrite, &gotSession, &dispatchedAt)
	if err != nil {
		t.Fatal(err)
	}
	if eventType != EventUpload || userID != 1 || uname != username || root != "/"+username {
		t.Errorf("identity mismatch: %q %d %q %q", eventType, userID, uname, root)
	}
	if conn != "ftp" || backend != "test" || path != "invoice.txt" {
		t.Errorf("connection/path mismatch: %q %q %q", conn, backend, path)
	}
	if size != 5 || mime != "text/plain" || mimeSource != MIMESourceExtension || !overwrite {
		t.Errorf("metadata mismatch: size=%d mime=%q src=%q overwrite=%v", size, mime, mimeSource, overwrite)
	}
	if gotSession != session {
		t.Errorf("session=%s, want %s", gotSession, session)
	}
	if dispatchedAt != nil {
		t.Errorf("dispatched_at=%v, want nil", dispatchedAt)
	}
}
