package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Event struct {
	EventTime time.Time      `json:"event_time"`
	Username  string         `json:"username"`
	ClientIP  net.IP         `json:"client_ip"`
	SessionID uuid.UUID      `json:"session_id"`
	Action    string         `json:"action"`
	Path      string         `json:"path"`
	Bytes     int64          `json:"bytes"`
	Success   bool           `json:"success"`
	Source    string         `json:"source"` // for CRED_REFRESH only
	Detail    map[string]any `json:"detail"`
}

type Logger struct {
	pool   *pgxpool.Pool
	ch     chan Event
	wg     sync.WaitGroup
	log    *slog.Logger
	closed atomic.Bool
}

func New(pool *pgxpool.Pool, log *slog.Logger) *Logger {
	l := &Logger{
		pool: pool,
		ch:   make(chan Event, 1024),
		log:  log,
	}
	l.wg.Add(1)
	go l.run()
	return l
}

func (l *Logger) Log(e Event) {
	if l == nil || l.closed.Load() {
		return
	}
	if e.EventTime.IsZero() {
		e.EventTime = time.Now().UTC()
	}
	if e.SessionID == uuid.Nil {
		e.SessionID = uuid.New()
	}
	select {
	case l.ch <- e:
	default:
		l.log.Warn("audit channel full, dropping event", "action", e.Action, "username", e.Username)
	}
}

func (l *Logger) run() {
	defer l.wg.Done()
	const flushInterval = 500 * time.Millisecond
	const batchSize = 100
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	buf := make([]Event, 0, batchSize)
	flush := func() {
		if len(buf) == 0 {
			return
		}
		if err := l.writeBatch(context.Background(), buf); err != nil {
			l.log.Error("audit batch write failed", "err", err, "size", len(buf))
		}
		buf = buf[:0]
	}
	for {
		select {
		case e, ok := <-l.ch:
			if !ok {
				flush()
				return
			}
			buf = append(buf, e)
			if len(buf) >= batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (l *Logger) writeBatch(ctx context.Context, batch []Event) error {
	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, e := range batch {
		detail, err := json.Marshal(e.Detail)
		if err != nil {
			l.log.Warn("audit detail marshal failed, skipping row", "err", err, "action", e.Action)
			detail = []byte("null")
		}
		var ip any
		if e.ClientIP != nil {
			ip = e.ClientIP.String()
		}
		var src any
		if e.Source != "" {
			src = e.Source
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO audit_events
			(event_time, username, client_ip, session_id, action, path, bytes, success, source, detail)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			e.EventTime, e.Username, ip, e.SessionID, e.Action, e.Path, e.Bytes, e.Success, src, detail)
		if err != nil {
			return fmt.Errorf("insert audit: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func (l *Logger) Close(ctx context.Context) error {
	if !l.closed.CompareAndSwap(false, true) {
		return nil
	}
	close(l.ch)
	done := make(chan struct{})
	go func() { l.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
