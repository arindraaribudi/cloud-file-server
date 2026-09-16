package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	ftpserverlib "github.com/fclairamb/ftpserverlib"
	"github.com/example/cos-ftp-server/internal/admin"
	"github.com/example/cos-ftp-server/internal/audit"
	"github.com/example/cos-ftp-server/internal/auth"
	"github.com/example/cos-ftp-server/internal/config"
	"github.com/example/cos-ftp-server/internal/cos"
	"github.com/example/cos-ftp-server/internal/db"
	"github.com/example/cos-ftp-server/internal/fsdriver"
	"github.com/example/cos-ftp-server/internal/ftpserver"
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
		return fmt.Errorf("db: %w", err)
	}
	defer pool.Close()

	log.Info("db: checking migrations")
	if err := db.Migrate(cfg.DatabaseURL); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	log.Info("db: migrations up to date")

	if cfg.SeedEnabled {
		log.Info("seed: enabled, applying")
		if user, pass := os.Getenv("FTP_SEED_USER"), os.Getenv("FTP_SEED_PASS"); user != "" && pass != "" {
			hash, hashErr := auth.HashPassword(pass)
			if hashErr != nil {
				return fmt.Errorf("seed: hash password: %w", hashErr)
			}
			if _, err := db.CreateAdminUser(ctx, pool, user, hash, "SuperAdmin"); err != nil {
				log.Warn("seed: admin user bootstrap failed (may already exist)", "user", user, "err", err)
			} else {
				log.Info("seed: admin user bootstrapped", "user", user)
			}
		}
		if err := db.Seed(ctx, pool); err != nil {
			return fmt.Errorf("seed: %w", err)
		}
		log.Info("seed: applied")
	} else {
		log.Info("seed: skipped (FTP_SEED not true)")
	}

	auditLog := audit.New(pool, log)
	defer func() { _ = auditLog.Close(context.Background()) }()

	chain, err := cos.NewChainFromEnv(ctx, cfg.COSStaticSecretID, cfg.COSStaticSecretKey, cfg.COSStaticSessionToken, cfg.STSRefreshRatio)
	if err != nil {
		return err
	}
	log.Info("cos: credential chain ready", "tke_pod_identity", cos.HasTKEPodIdentity(), "static_fallback", chain.HasStatic())
	client := cos.NewClientWithChain(cfg.COSBucket, cfg.COSRegion, chain)
	log.Info("cos: client connected", "bucket", cfg.COSBucket, "region", cfg.COSRegion)

	srv := &ftpserver.Server{
		Addr:             cfg.FTPListen,
		PublicHost:       cfg.FTPPublicIP,
		PassivePortRange: [2]int{cfg.PassivePortRange.Start, cfg.PassivePortRange.End},
		IdleTimeout:      cfg.IdleTimeout,
		NewDriver: func(u *db.FTPUser) (ftpserverlib.ClientDriver, error) {
			log.Info("ftp: user connected", "username", u.Username, "bucket", cfg.COSBucket, "region", cfg.COSRegion, "root_prefix", u.RootFolder)
			auditLog.Log(audit.Event{
				Username: u.Username,
				Action:   "LOGIN",
				Success:  true,
				Detail:   map[string]any{"bucket": cfg.COSBucket, "region": cfg.COSRegion, "root_prefix": u.RootFolder},
			})
			return fsdriver.NewAuditFS(fsdriver.NewCOS(u.RootFolder, client), auditLog, u.Username), nil
		},
		Authenticator: &ftpserver.DBAuthenticator{
			Pool:    pool,
			Lockout: auth.NewLockout(cfg.AuthLockoutLimit, cfg.AuthLockoutWindow),
			Audit:   auditLog,
		},
		Audit:  auditLog,
		Logger: log,
	}
	if err := srv.Start(ctx); err != nil {
		return err
	}

	// Admin API + metrics on cfg.AdminListen (default :8080).
	adminClient := cos.NewClientWithChain(cfg.COSBucket, cfg.COSRegion, chain)
	adminAPI := admin.New(pool, cfg.AdminCookieSecure, cfg.COSBucket, cfg.COSRegion, adminClient, cfg.FTPDefaultRootPrefix, auditLog)
	if cfg.OIDCIssuerURL != "" {
		oidcCtx, cancelOIDC := context.WithTimeout(ctx, 10*time.Second)
		err := adminAPI.ConfigureOIDC(oidcCtx, admin.OIDCConfig{
			IssuerURL:        cfg.OIDCIssuerURL,
			ClientID:         cfg.OIDCClientID,
			ClientSecret:     cfg.OIDCClientSecret,
			PublicURL:        cfg.PublicURL,
			AdminGroup:       cfg.OIDCAdminGroup,
			ReadonlyGroup:    cfg.OIDCReadonlyGroup,
			FrontendRedirect: cfg.OIDCFrontendRedirectURL,
		})
		cancelOIDC()
		if err != nil {
			log.Error("oidc setup failed; SSO login disabled", "err", err)
		}
	}
	adminSrv := &http.Server{
		Addr:              cfg.AdminListen,
		Handler:           adminAPI.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	adminErrCh := make(chan error, 1)
	go func() {
		log.Info("admin starting", "addr", cfg.AdminListen)
		if err := adminSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			adminErrCh <- err
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	_ = adminSrv.Shutdown(shutdownCtx)
	select {
	case err := <-adminErrCh:
		if err != nil {
			return fmt.Errorf("admin: %w", err)
		}
	default:
	}
	return srv.Stop()
}
