package fsdriver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

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

// backendPlugins maps the short per-user backend names to plugin identifiers.
var backendPlugins = map[string]string{
	core.BackendCOS:   PluginCOS,
	core.BackendLocal: PluginLocal,
}

// BackendName returns the short backend name for a plugin identifier
// (the STORAGE_BACKEND value), e.g. PluginLocal -> "local".
func BackendName(pluginID string) (string, bool) {
	for name, id := range backendPlugins {
		if id == pluginID {
			return name, true
		}
	}
	return "", false
}

// NewStorageRouter builds every backend named in cfg.StorageEnabledBackends
// (default: just the STORAGE_BACKEND one) and routes users between them.
// STORAGE_BACKEND stays the default for users without an explicit choice, so
// a deployment that sets neither new variable behaves exactly as before.
// Call Init on the result before use.
func NewStorageRouter(cfg *config.Config, auditLog *audit.Logger, log *slog.Logger) (*core.StorageRouter, error) {
	def, ok := BackendName(cfg.StorageBackend)
	if !ok {
		return nil, fmt.Errorf("unknown STORAGE_BACKEND plugin %q", cfg.StorageBackend)
	}
	enabled := cfg.StorageEnabledBackends
	if len(enabled) == 0 {
		enabled = []string{def}
	}
	backends := make(map[string]core.ObjectStorage, len(enabled))
	for _, name := range enabled {
		id, ok := backendPlugins[name]
		if !ok {
			return nil, fmt.Errorf("unknown storage backend %q in STORAGE_ENABLED_BACKENDS (known: cos, local)", name)
		}
		st, err := NewObjectStorage(id, cfg, auditLog, log)
		if err != nil {
			return nil, err
		}
		backends[name] = st
	}
	if _, ok := backends[def]; !ok {
		return nil, fmt.Errorf("STORAGE_BACKEND default %q must be listed in STORAGE_ENABLED_BACKENDS", def)
	}
	return core.NewStorageRouter(def, backends)
}

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

var (
	_ core.ObjectStorage = (*COSPlugin)(nil)
	_ core.Copier        = (*COSPlugin)(nil)
)

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

// Copy performs a server-side COS copy. Both paths are root-relative to their
// respective user roots; the internal file API has already sanitized them.
func (p *COSPlugin) Copy(ctx context.Context, srcRoot, srcPath, dstRoot, dstPath string) error {
	if p.client == nil {
		return errors.New("cos: client not initialized")
	}
	src := joinObjectKey(srcRoot, srcPath)
	dst := joinObjectKey(dstRoot, dstPath)
	if src == dst {
		return nil
	}
	return p.client.Copy(ctx, src, dst)
}

func joinObjectKey(root, rel string) string {
	root = strings.Trim(root, "/")
	rel = strings.TrimLeft(rel, "/")
	switch {
	case root == "":
		return rel
	case rel == "":
		return root
	default:
		return root + "/" + rel
	}
}

// BackendLocation returns the COS bucket endpoint URL the plugin is
// configured against. Surfaces in the audit log so each row tells you which
// bucket/region it hit without joining cfg.
func (p *COSPlugin) BackendLocation() string {
	return "https://" + p.Bucket + ".cos." + p.Region + ".myqcloud.com"
}

// Client exposes the underlying *cos.Client for the two admin endpoints
// that are COS-specific by design (folder-suggestion listing, root-folder
// placeholder creation on user creation) and are out of scope for this
// refactor. Returns nil until Init has run.
func (p *COSPlugin) Client() *cos.Client { return p.client }

// LocalPlugin is the Object Storage plugin for local disk (dev/test use).
type LocalPlugin struct{ Root string }

var (
	_ core.ObjectStorage = (*LocalPlugin)(nil)
	_ core.Copier        = (*LocalPlugin)(nil)
)

func (p *LocalPlugin) Init(ctx context.Context) error {
	return os.MkdirAll(p.Root, 0o755)
}

func (p *LocalPlugin) Mount(rootFolder string) (afero.Fs, error) {
	dir, err := p.userDir(rootFolder)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return NewLocal(dir), nil
}

// userDir resolves a user's root folder under p.Root and refuses anything that
// would escape it (e.g. "/../other").
func (p *LocalPlugin) userDir(rootFolder string) (string, error) {
	root := filepath.Clean(p.Root)
	dir := filepath.Join(root, filepath.FromSlash(rootFolder))
	if dir != root && !strings.HasPrefix(dir, root+string(filepath.Separator)) {
		return "", fmt.Errorf("local: root folder %q escapes storage root", rootFolder)
	}
	return dir, nil
}

// Copy streams a file between two local roots. The internal file API has
// already sanitized both paths.
func (p *LocalPlugin) Copy(_ context.Context, srcRoot, srcPath, dstRoot, dstPath string) error {
	srcDir, err := p.userDir(srcRoot)
	if err != nil {
		return err
	}
	dstDir, err := p.userDir(dstRoot)
	if err != nil {
		return err
	}
	src := filepath.Join(srcDir, filepath.FromSlash(srcPath))
	dst := filepath.Join(dstDir, filepath.FromSlash(dstPath))
	if src == dst {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// BackendLocation identifies local-disk storage in the audit log.
func (p *LocalPlugin) BackendLocation() string { return "local://" + p.Root }
