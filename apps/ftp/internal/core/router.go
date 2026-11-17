package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/afero"

	"github.com/example/cos-ftp-server/internal/db"
)

// Short, user-facing storage backend names. These are what ftp_users.
// storage_backend stores and what the admin API/UI exchange; they are mapped
// to plugin identifiers (STORAGE_BACKEND) by fsdriver.
const (
	BackendCOS   = "cos"
	BackendLocal = "local"
)

// ErrBackendNotEnabled is returned when a user's backend is not one the
// deployment has enabled (STORAGE_ENABLED_BACKENDS).
var ErrBackendNotEnabled = errors.New("storage backend not enabled")

// UserStorage resolves the storage backend for a specific user. It is what
// the protocol touchpoints and the admin API consume instead of a single
// global ObjectStorage.
type UserStorage interface {
	// Mount returns the user's filesystem view on their backend.
	Mount(u *db.FTPUser) (afero.Fs, error)
	// BackendLocation identifies where the user's data lives (audit log).
	BackendLocation(u *db.FTPUser) string
	// Resolve maps a stored backend name to an effective one ("" -> default).
	Resolve(name string) string
	// Default is the backend assigned to users that don't choose one.
	Default() string
	// Enabled lists the backends users may be assigned to, sorted.
	Enabled() []string
	// Has reports whether name (after Resolve) is an enabled backend.
	Has(name string) bool
	// CopyNative attempts a server-side copy between two users. handled is
	// false when no native path exists (different backends, or a backend
	// without Copier support); the caller must then stream the bytes itself.
	CopyNative(ctx context.Context, src, dst *db.FTPUser, srcPath, dstPath string) (handled bool, err error)
}

// StorageRouter is the UserStorage implementation: a set of initialised
// ObjectStorage plugins keyed by short backend name plus a default.
type StorageRouter struct {
	backends   map[string]ObjectStorage
	defaultKey string
}

var _ UserStorage = (*StorageRouter)(nil)

// NewStorageRouter builds a router. defaultKey must be a key of backends.
func NewStorageRouter(defaultKey string, backends map[string]ObjectStorage) (*StorageRouter, error) {
	defaultKey = strings.ToLower(strings.TrimSpace(defaultKey))
	if len(backends) == 0 {
		return nil, errors.New("storage router: no backends")
	}
	if _, ok := backends[defaultKey]; !ok {
		return nil, fmt.Errorf("storage router: default backend %q is not enabled", defaultKey)
	}
	m := make(map[string]ObjectStorage, len(backends))
	for k, v := range backends {
		m[strings.ToLower(k)] = v
	}
	return &StorageRouter{backends: m, defaultKey: defaultKey}, nil
}

// Init initialises every backend. The first failure aborts startup so a
// misconfigured backend is never silently unavailable.
func (r *StorageRouter) Init(ctx context.Context) error {
	for _, name := range r.Enabled() {
		if err := r.backends[name].Init(ctx); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

// Backend returns the plugin for a (possibly empty) backend name.
func (r *StorageRouter) Backend(name string) (ObjectStorage, error) {
	key := r.Resolve(name)
	b, ok := r.backends[key]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrBackendNotEnabled, key)
	}
	return b, nil
}

func (r *StorageRouter) forUser(u *db.FTPUser) (ObjectStorage, error) {
	if u == nil {
		return nil, errors.New("storage router: nil user")
	}
	return r.Backend(u.StorageBackend)
}

func (r *StorageRouter) Mount(u *db.FTPUser) (afero.Fs, error) {
	b, err := r.forUser(u)
	if err != nil {
		return nil, err
	}
	return b.Mount(u.RootFolder)
}

func (r *StorageRouter) BackendLocation(u *db.FTPUser) string {
	b, err := r.forUser(u)
	if err != nil {
		return ""
	}
	return b.BackendLocation()
}

func (r *StorageRouter) Resolve(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return r.defaultKey
	}
	return name
}

func (r *StorageRouter) Default() string { return r.defaultKey }

func (r *StorageRouter) Enabled() []string {
	out := make([]string, 0, len(r.backends))
	for k := range r.backends {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (r *StorageRouter) Has(name string) bool {
	_, ok := r.backends[r.Resolve(name)]
	return ok
}

// CopyNative uses the backend's server-side copy only when both users live on
// the same backend. Cross-backend copies return handled=false.
// See docs/cross-backend-copy.md.
func (r *StorageRouter) CopyNative(ctx context.Context, src, dst *db.FTPUser, srcPath, dstPath string) (bool, error) {
	if src == nil || dst == nil {
		return false, errors.New("storage router: nil user")
	}
	if r.Resolve(src.StorageBackend) != r.Resolve(dst.StorageBackend) {
		return false, nil
	}
	b, err := r.forUser(src)
	if err != nil {
		return false, err
	}
	copier, ok := b.(Copier)
	if !ok {
		return false, nil
	}
	return true, copier.Copy(ctx, src.RootFolder, srcPath, dst.RootFolder, dstPath)
}
