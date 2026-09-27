package fsdriver

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/afero"

	"github.com/example/cos-ftp-server/internal/audit"
	"github.com/example/cos-ftp-server/internal/config"
	"github.com/example/cos-ftp-server/internal/core"
	"github.com/example/cos-ftp-server/internal/cos"
)

// Plugin identifiers — value of STORAGE_BACKEND. Each maps to one
// core.ObjectStorage implementation.
const (
	PluginCOS   = "cos.objectstorage.plugin"
	PluginLocal = "local.objectstorage.plugin"
	// future: "aws-s3.objectstorage.plugin", "gcs.objectstorage.plugin"
)

// NewObjectStorage builds the plugin named by id, or an error if id
// doesn't match a known plugin. fsdriver owns the known-plugin set.
func NewObjectStorage(id string, cfg *config.Config, auditLog *audit.Logger, log *slog.Logger) (core.ObjectStorage, error) {
	switch id {
	case PluginCOS:
		return &COSPlugin{
			Bucket:             cfg.COSBucket,
			Region:             cfg.COSRegion,
			StaticSecretID:     cfg.COSStaticSecretID,
			StaticSecretKey:    cfg.COSStaticSecretKey,
			StaticSessionToken: cfg.COSStaticSessionToken,
			STSRefreshRatio:    cfg.STSRefreshRatio,
			Audit:              auditLog,
			Logger:             log,
		}, nil
	case PluginLocal:
		return &LocalPlugin{Root: cfg.StorageLocalRoot}, nil
	default:
		return nil, fmt.Errorf("unknown STORAGE_BACKEND plugin %q", id)
	}
}

// COSPlugin is the Object Storage plugin for Tencent COS. It owns the
// credential chain (TKE Pod Identity -> static AK/SK -> hard fail) and
// mounts a per-user COS-backed afero.Fs.
type COSPlugin struct {
	Bucket, Region                                      string
	StaticSecretID, StaticSecretKey, StaticSessionToken string
	STSRefreshRatio                                     float64
	Audit                                               *audit.Logger
	Logger                                              *slog.Logger

	client *cos.Client
}

var _ core.ObjectStorage = (*COSPlugin)(nil)

// Init builds and validates the credential chain, wires the audit hook for
// credential refresh events, and connects the COS client. Moved here from
// cmd/ftp-server/main.go verbatim.
func (p *COSPlugin) Init(ctx context.Context) error {
	chain, err := cos.NewChainFromEnv(ctx, p.StaticSecretID, p.StaticSecretKey, p.StaticSessionToken, p.STSRefreshRatio)
	if err != nil {
		return err
	}
	chain.OnRefresh = func(src string, ok bool, refreshErr error) {
		attrs := []any{"bucket", p.Bucket, "region", p.Region, "source", src}
		if ok {
			p.Logger.Info("cred refresh ok", attrs...)
		} else {
			p.Logger.Warn("cred refresh failed", append(attrs, "err", refreshErr)...)
		}
		detail := map[string]any{"bucket": p.Bucket, "region": p.Region}
		if refreshErr != nil {
			detail["err"] = refreshErr.Error()
		}
		p.Audit.Log(audit.Event{
			Action:  "CRED_REFRESH",
			Source:  src,
			Success: ok,
			Detail:  detail,
		})
	}
	if _, src, err := chain.Get(ctx); err != nil {
		return fmt.Errorf("cos: credential validation failed at startup: %w", err)
	} else {
		p.Logger.Info("cos: credential chain validated", "source", src)
	}
	if claims, err := cos.WebIdentityClaims(); err != nil {
		p.Logger.Warn("cos: could not decode web-identity token claims", "err", err)
	} else {
		p.Logger.Info("cos: web-identity token claims", "sub", claims["sub"], "iss", claims["iss"], "role_arn", os.Getenv("TKE_ROLE_ARN"))
	}
	p.Logger.Info("cos: credential chain ready", "tke_pod_identity", cos.HasTKEPodIdentity(), "static_fallback", chain.HasStatic())
	p.client = cos.NewClientWithChain(p.Bucket, p.Region, chain)
	p.Logger.Info("cos: client connected", "bucket", p.Bucket, "region", p.Region)
	return nil
}

func (p *COSPlugin) Mount(rootFolder string) (afero.Fs, error) {
	return NewCOS(rootFolder, p.client), nil
}

// Client exposes the underlying *cos.Client for the two admin endpoints
// that are COS-specific by design (folder-suggestion listing, root-folder
// placeholder creation on user creation) and are out of scope for this
// refactor. Returns nil until Init has run.
func (p *COSPlugin) Client() *cos.Client { return p.client }

// LocalPlugin is the Object Storage plugin for local disk (dev/test use).
type LocalPlugin struct{ Root string }

var _ core.ObjectStorage = (*LocalPlugin)(nil)

func (p *LocalPlugin) Init(ctx context.Context) error {
	return os.MkdirAll(p.Root, 0o755)
}

func (p *LocalPlugin) Mount(rootFolder string) (afero.Fs, error) {
	dir := filepath.Join(p.Root, rootFolder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return NewLocal(dir), nil
}
