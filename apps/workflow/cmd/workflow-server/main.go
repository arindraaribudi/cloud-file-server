package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/example/cos-workflow/internal/action"
	"github.com/example/cos-workflow/internal/api"
	"github.com/example/cos-workflow/internal/components"
	"github.com/example/cos-workflow/internal/config"
	"github.com/example/cos-workflow/internal/db"
	"github.com/example/cos-workflow/internal/engine"
	"github.com/example/cos-workflow/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pool, err := db.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := db.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	log.Info("workflow: waiting for file_events table")
	if err := db.WaitForTable(ctx, pool, "public.file_events", 500*time.Millisecond); err != nil {
		return err
	}

	reg := components.NewRegistry()
	st := store.New(pool)
	files := action.NewFileClient(cfg.FileAPIURL, cfg.ServiceToken, 30*time.Second)
	notifier := action.NewNotifier(action.NotifierConfig{
		AllowHTTP: cfg.WebhookAllowHTTP, AllowPrivate: cfg.WebhookAllowPrivate,
		SMTPHost: cfg.SMTPHost, SMTPPort: cfg.SMTPPort, SMTPUser: cfg.SMTPUser,
		SMTPPass: cfg.SMTPPass, SMTPFrom: cfg.SMTPFrom, SMTPTLS: cfg.SMTPTLS,
	})
	actions := action.NewExecutor(files, notifier)
	eng := engine.New(st, reg, actions, engine.Config{
		Workers: cfg.Workers, PollInterval: cfg.PollInterval, MaxAttempts: cfg.MaxAttempts,
		ComponentTimeout: cfg.ComponentTimeout, RunTimeout: cfg.RunTimeout, RetentionDays: cfg.RetentionDays,
	}, log)
	go eng.Run(ctx)
	log.Info("workflow engine started", "workers", cfg.Workers)

	apiSrv := api.New(st, reg, cfg.ServiceToken)
	apiSrv.Engine = eng
	handler := apiSrv.Routes()

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("workflow: listening", "addr", cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
