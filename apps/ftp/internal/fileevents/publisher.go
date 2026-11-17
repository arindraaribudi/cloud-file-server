package fileevents

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/afero"
)

// Publisher writes durable file events for the workflow service. Insertion is
// synchronous and best-effort: a failed insert is logged but never fails the
// file operation that produced the event.
type Publisher struct {
	pool         *pgxpool.Pool
	log          *slog.Logger
	mimeMode     string
	mimeMaxBytes int64
}

func NewPublisher(pool *pgxpool.Pool, log *slog.Logger, mimeMode string, mimeMaxBytes int64) *Publisher {
	if mimeMaxBytes <= 0 {
		mimeMaxBytes = DefaultMIMESniffMaxBytes
	}
	return &Publisher{pool: pool, log: log, mimeMode: normalizeMode(mimeMode), mimeMaxBytes: mimeMaxBytes}
}

// DetectMIME uses the publisher's configured mode and cap. A nil publisher
// still returns a usable extension-based classification.
func (p *Publisher) DetectMIME(fs afero.Fs, name string, size int64) (string, string) {
	if p == nil {
		return DetectMIME(fs, name, size, MIMEFallback, DefaultMIMESniffMaxBytes)
	}
	return DetectMIME(fs, name, size, p.mimeMode, p.mimeMaxBytes)
}

// Publish inserts one event. It is safe to call on a nil publisher.
func (p *Publisher) Publish(ctx context.Context, e Event) {
	if p == nil || p.pool == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if e.EventTime.IsZero() {
		e.EventTime = time.Now().UTC()
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var userID any
	if e.UserID != 0 {
		userID = e.UserID
	}
	var oldPath any
	if e.OldPath != "" {
		oldPath = e.OldPath
	}
	var sessionID any
	if e.SessionID != uuid.Nil {
		sessionID = e.SessionID
	}

	_, err := p.pool.Exec(ctx, `
		INSERT INTO file_events
		(event_time, event_type, user_id, username, root_folder, connection_type,
		 backend_location, path, old_path, is_dir, size, mime, mime_source,
		 overwrite, session_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		e.EventTime, e.EventType, userID, e.Username, e.RootFolder, e.ConnectionType,
		e.BackendLocation, e.Path, oldPath, e.IsDir, e.Size, e.MIME, e.MIMESource,
		e.Overwrite, sessionID)
	if err != nil {
		p.logger().Warn("file event insert failed",
			"event_type", e.EventType, "username", e.Username, "path", e.Path, "err", err)
	}
}

// RunRetention deletes dispatched events older than days, in batches. It runs
// once immediately and then every six hours until ctx is cancelled.
func (p *Publisher) RunRetention(ctx context.Context, days int) {
	if p == nil || p.pool == nil || days <= 0 {
		return
	}
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	cleanup := func() {
		cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
		for {
			n, err := p.deleteBatch(ctx, cutoff)
			if err != nil {
				p.logger().Warn("file event retention failed", "err", err)
				return
			}
			if n == 0 {
				return
			}
			if ctx.Err() != nil {
				return
			}
		}
	}

	cleanup()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanup()
		}
	}
}

func (p *Publisher) deleteBatch(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := p.pool.Exec(ctx, `
		DELETE FROM file_events
		WHERE id IN (
			SELECT id FROM file_events
			WHERE dispatched_at IS NOT NULL AND dispatched_at < $1
			ORDER BY id
			LIMIT 1000
		)`, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (p *Publisher) logger() *slog.Logger {
	if p != nil && p.log != nil {
		return p.log
	}
	return slog.Default()
}
