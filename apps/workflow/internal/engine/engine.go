package engine

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/example/cos-workflow/internal/action"
	"github.com/example/cos-workflow/internal/components"
	"github.com/example/cos-workflow/internal/store"
)

type Config struct {
	Workers          int
	PollInterval     time.Duration
	MaxAttempts      int
	ComponentTimeout time.Duration
	RunTimeout       time.Duration
	RetentionDays    int
}

type Engine struct {
	Store    *store.Store
	Registry *components.Registry
	Actions  *action.Executor
	Config   Config
	Logger   *slog.Logger
}

func New(st *store.Store, reg *components.Registry, actions *action.Executor, cfg Config, log *slog.Logger) *Engine {
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.ComponentTimeout <= 0 {
		cfg.ComponentTimeout = 60 * time.Second
	}
	if cfg.RunTimeout <= 0 {
		cfg.RunTimeout = 10 * time.Minute
	}
	if log == nil {
		log = slog.Default()
	}
	return &Engine{Store: st, Registry: reg, Actions: actions, Config: cfg, Logger: log}
}

func (e *Engine) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); e.dispatchLoop(ctx) }()
	wg.Add(1)
	go func() { defer wg.Done(); e.reaperLoop(ctx) }()

	host, _ := os.Hostname()
	for i := 0; i < e.Config.Workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e.workerLoop(ctx, fmt.Sprintf("%s-%d", host, i))
		}(i)
	}
	wg.Wait()
}

func (e *Engine) dispatchLoop(ctx context.Context) {
	ticker := time.NewTicker(e.Config.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			events, runs, err := e.dispatchBatch(ctx)
			if err != nil {
				e.Logger.Error("workflow dispatch failed", "err", err)
				continue
			}
			if events > 0 {
				e.Logger.Info("workflow events dispatched", "events", events, "runs", runs)
			}
		}
	}
}

func (e *Engine) reaperLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	lastCleanup := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := e.Store.ReapExpiredLocks(ctx); err != nil {
				e.Logger.Error("workflow lock reaper failed", "err", err)
			} else if n > 0 {
				e.Logger.Warn("workflow expired locks requeued", "count", n)
			}
			if n, err := e.Store.CancelDisabledRuns(ctx); err != nil {
				e.Logger.Error("workflow disabled-run cleanup failed", "err", err)
			} else if n > 0 {
				e.Logger.Info("workflow queued runs canceled", "count", n)
			}
			if e.Config.RetentionDays > 0 && time.Since(lastCleanup) >= 24*time.Hour {
				cutoff := time.Now().Add(-time.Duration(e.Config.RetentionDays) * 24 * time.Hour)
				if n, err := e.Store.CleanupRuns(ctx, cutoff); err != nil {
					e.Logger.Error("workflow run retention failed", "err", err)
				} else {
					lastCleanup = time.Now()
					if n > 0 {
						e.Logger.Info("workflow old runs deleted", "count", n)
					}
				}
			}
		}
	}
}

func (e *Engine) workerLoop(ctx context.Context, workerID string) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := e.executeOne(ctx, workerID); err != nil {
			e.Logger.Error("workflow execution failed", "worker", workerID, "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}
