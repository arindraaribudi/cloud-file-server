package fsdriver

import (
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/afero"
)

// Local implements afero.Fs (which ftpserver.ClientDriver requires)
// by wrapping an afero.OsFs rooted at a configurable base directory.
// All paths passed to the interface methods are confined to Root.
type Local struct {
	fs   afero.Fs
	root string
}

func NewLocal(root string) *Local {
	r := filepath.Clean(root)
	return &Local{fs: afero.NewBasePathFs(afero.NewOsFs(), r), root: r}
}

// Compile-time check: Local satisfies afero.Fs (and therefore ftpserver.ClientDriver).
var _ afero.Fs = (*Local)(nil)

// afero.Fs methods ------------------------------------------------

func (l *Local) Name() string                         { return "Local(" + l.root + ")" }
func (l *Local) Open(name string) (afero.File, error) { return l.fs.Open(name) }
func (l *Local) Create(name string) (afero.File, error) {
	return l.fs.Create(name)
}
func (l *Local) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	return l.fs.OpenFile(name, flag, perm)
}

// ReadRange reads up to length bytes at offset. fileevents uses it for MIME
// sniffing.
func (l *Local) ReadRange(name string, offset, length int64) ([]byte, error) {
	if length <= 0 {
		return nil, nil
	}
	f, err := l.fs.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(f, length))
}
func (l *Local) Remove(name string) error             { return l.fs.Remove(name) }
func (l *Local) Rename(oldname, newname string) error { return l.fs.Rename(oldname, newname) }
func (l *Local) Mkdir(name string, perm os.FileMode) error {
	return l.fs.Mkdir(name, perm)
}
func (l *Local) MkdirAll(path string, perm os.FileMode) error {
	return l.fs.MkdirAll(path, perm)
}
func (l *Local) RemoveAll(path string) error { return l.fs.RemoveAll(path) }
func (l *Local) Stat(name string) (os.FileInfo, error) {
	return l.fs.Stat(name)
}
func (l *Local) Chmod(name string, mode os.FileMode) error {
	return l.fs.Chmod(name, mode)
}
func (l *Local) Chown(name string, uid, gid int) error {
	return l.fs.Chown(name, uid, gid)
}
func (l *Local) Chtimes(name string, atime, mtime time.Time) error {
	return l.fs.Chtimes(name, atime, mtime)
}

// Root returns the confined base directory.
func (l *Local) Root() string { return l.root }
