package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	ftpserverlib "github.com/fclairamb/ftpserverlib"
	"github.com/example/cos-ftp-server/internal/admin"
	"github.com/example/cos-ftp-server/internal/audit"
	"github.com/example/cos-ftp-server/internal/auth"
	"github.com/example/cos-ftp-server/internal/config"
	"github.com/example/cos-ftp-server/internal/core"
	"github.com/example/cos-ftp-server/internal/cos"
	"github.com/example/cos-ftp-server/internal/db"
	"github.com/example/cos-ftp-server/internal/fsdriver"
	"github.com/example/cos-ftp-server/internal/ftpserver"
	"github.com/example/cos-ftp-server/internal/sftpserver"
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

	log.Info("protocols: startup",
		"ftp_enabled", cfg.FTPEnabled, "ftp_listen", cfg.FTPListen,
		"sftp_enabled", cfg.SFTPEnabled, "sftp_listen", cfg.SFTPListen,
	)

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

	storage, err := fsdriver.NewObjectStorage(cfg.StorageBackend, cfg, auditLog, log)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	if err := storage.Init(ctx); err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	var cosClient *cos.Client
	if cp, ok := storage.(*fsdriver.COSPlugin); ok {
		cosClient = cp.Client()
	}

	srv := &ftpserver.Server{
		Addr:             cfg.FTPListen,
		PublicHost:       cfg.FTPPublicIP,
		PassivePortRange: [2]int{cfg.PassivePortRange.Start, cfg.PassivePortRange.End},
		IdleTimeout:      cfg.IdleTimeout,
		ProxyProtocol:    cfg.FTPProxyProtocol,
		NewDriver: func(u *db.FTPUser, clientIP string) (ftpserverlib.ClientDriver, error) {
			fs, err := storage.Mount(u.RootFolder)
			if err != nil {
				return nil, fmt.Errorf("mount: %w", err)
			}
			log.Info("ftp: user connected", "username", u.Username, "client_ip", clientIP, "bucket", cfg.COSBucket, "region", cfg.COSRegion, "root_prefix", u.RootFolder)
			auditLog.Log(audit.Event{
				Username: u.Username,
				ClientIP: net.ParseIP(clientIP),
				Action:   "LOGIN",
				Success:  true,
				Detail:   map[string]any{"bucket": cfg.COSBucket, "region": cfg.COSRegion, "root_prefix": u.RootFolder},
			})
			return fsdriver.NewAuditFS(fs, auditLog, u.Username, net.ParseIP(clientIP)), nil
		},
		Authenticator: &core.DBAuthenticator{
			Pool:    pool,
			Lockout: auth.NewLockout(cfg.AuthLockoutLimit, cfg.AuthLockoutWindow),
			Audit:   auditLog,
		},
		Audit:  auditLog,
		Logger: log,
	}
	if cfg.FTPTLSCert != "" && cfg.FTPTLSKey != "" {
		srv.TLS = &ftpserver.TLSConfig{
			CertFile: cfg.FTPTLSCert,
			KeyFile:  cfg.FTPTLSKey,
		}
	}
	if cfg.FTPEnabled {
		if err := srv.Start(ctx); err != nil {
			return err
		}
	}
	if srv.TLS != nil {
		if cert, err := tls.LoadX509KeyPair(srv.TLS.CertFile, srv.TLS.KeyFile); err == nil && len(cert.Certificate) > 0 {
			if leaf, err := x509.ParseCertificate(cert.Certificate[0]); err == nil {
				days := int(time.Until(leaf.NotAfter).Hours() / 24)
				fmt.Printf("FTP TLS active: subject=%s issuer=%s expires=%s days_left=%d\n",
					leaf.Subject, leaf.Issuer, leaf.NotAfter.UTC().Format(time.RFC3339), days)
			}
		}
	}

	var sftpSrv *sftpserver.Server
	if cfg.SFTPEnabled {
		hostKey, err := sftpserver.LoadOrGenerateHostKey(cfg.SFTPHostKey, log)
		if err != nil {
			return fmt.Errorf("sftp: host key: %w", err)
		}
		sftpSrv = &sftpserver.Server{
			Addr:          cfg.SFTPListen,
			HostKeySigner: hostKey,
			// Same *core.DBAuthenticator instance srv.Authenticator uses
			// (constructed above, in the ftpserver.Server{} literal) —
			// per the design, brute-force lockout is shared across both
			// protocols for the same username.
			Authenticator: srv.Authenticator,
			LookupUser: func(ctx context.Context, username string) (*db.FTPUser, error) {
				return db.GetFTPUserPublicKey(ctx, pool, username)
			},
			Storage: storage,
			Audit:   auditLog,
			Logger:  log,
		}
		if err := sftpSrv.Start(ctx); err != nil {
			return fmt.Errorf("sftp: %w", err)
		}
		log.Info("sftp: listening", "addr", cfg.SFTPListen)
	}

	// Admin API + metrics on cfg.AdminListen (default :8080).
	adminAPI := admin.New(pool, cfg.AdminCookieSecure, cfg.COSBucket, cfg.COSRegion, cosClient, storage, cfg.FTPDefaultRootPrefix, auditLog)
	if cfg.FTPPublicIP != "" {
		adminAPI.FTPAddress = cfg.FTPPublicIP + cfg.FTPListen
		adminAPI.SFTPAddress = cfg.FTPPublicIP + cfg.SFTPListen
	}
	if u, err := url.Parse(cfg.PublicURL); err == nil && u.Hostname() != "" {
		adminAPI.FTPPublicAddress = u.Hostname() + cfg.FTPListen
		adminAPI.SFTPPublicAddress = u.Hostname() + cfg.SFTPListen
	}
	adminAPI.FTPEnabled = cfg.FTPEnabled
	adminAPI.SFTPEnabled = cfg.SFTPEnabled
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
	if sftpSrv != nil {
		_ = sftpSrv.Stop()
	}
	return srv.Stop()
}
