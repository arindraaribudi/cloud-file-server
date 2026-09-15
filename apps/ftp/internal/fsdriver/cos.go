package fsdriver

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/afero"

	"github.com/example/cos-ftp-server/internal/cos"
)

// COS implements afero.Fs (and therefore ftpserver.ClientDriver) against a
// Tencent COS bucket. Folders are virtual: a folder exists iff ≥1 child
// key has the prefix. MKD/RMD are no-ops.
type COS struct {
	root string
	c    *cos.Client
}

// Compile-time check: satisfies afero.Fs.
var _ afero.Fs = (*COS)(nil)

// NewCOS builds a virtual-folder COS driver rooted at rootPrefix (e.g. "alice" or "/alice").
// Leading and trailing slashes are stripped so the resulting keys never contain "//".
// The root placeholder is created eagerly so Stat("/") works before any explicit MKD.
func NewCOS(rootPrefix string, c *cos.Client) afero.Fs {
	r := strings.Trim(rootPrefix, "/")
	v := &COS{root: r, c: c}
	_ = v.c.Put(context.Background(), r+"/", bytes.NewReader(nil), 0) // ponytail: best-effort; exists-already is fine
	return v
}

func (v *COS) Name() string { return "COS(" + v.root + ")" }

func (v *COS) fullKey(name string) string {
	name = strings.TrimLeft(name, "/")
	return v.root + "/" + name
}

// Open returns a read-only file handle.
// ReadDir lists entries directly via the COS List API (not via Open+Readdir)
// so folder placeholders never leak into the listing.
func (v *COS) ReadDir(name string) ([]os.FileInfo, error) {
	ctx := context.Background()
	prefix := v.fullKey(name)
	entries, err := v.c.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]os.FileInfo, 0, len(entries))
	for _, e := range entries {
		out = append(out, &fileInfo{name: e.Name, size: e.Size, mode: fileMode(e.IsDir), modTime: e.ModifyTime, isDir: e.IsDir})
	}
	return out, nil
}

func fileMode(isDir bool) os.FileMode {
	if isDir {
		return os.ModeDir | 0o755
	}
	return 0o644
}

func (v *COS) Open(name string) (afero.File, error) {
	key := v.fullKey(name)
	ctx := context.Background()
	rc, err := v.c.Get(ctx, key, 0, 0)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	return newReadOnlyFile(bytes.NewReader(data), v.fullName(name), int64(len(data))), nil
}

// Create returns a write-only handle. We buffer in memory because v0.x of the
// COS SDK requires a Reader + known size for simple PUT. Real impl would use
// multipart upload for files > 20 MB; deferred to a follow-up task per plan §12.
func (v *COS) Create(name string) (afero.File, error) {
	return &memWriteFile{buf: &bytes.Buffer{}, finalKey: v.fullKey(name), c: v.c}, nil
}

func (v *COS) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if flag&os.O_CREATE != 0 {
		return v.Create(name)
	}
	return v.Open(name)
}

func (v *COS) Remove(name string) error {
	ctx := context.Background()
	return v.c.Delete(ctx, v.fullKey(name))
}

func (v *COS) Rename(oldname, newname string) error {
	ctx := context.Background()
	if err := v.c.Copy(ctx, v.fullKey(oldname), v.fullKey(newname)); err != nil {
		return err
	}
	return v.c.Delete(ctx, v.fullKey(oldname))
}

func (v *COS) MkdirAll(path string, perm os.FileMode) error { return v.Mkdir(path, perm) }
func (v *COS) RemoveAll(path string) error                  { return v.Remove(path) }

// Mkdir creates a 0-byte placeholder object at "<key>/" so that HEAD on the folder
// succeeds. COS has no real folders — placeholders are the convention.
func (v *COS) Mkdir(path string, perm os.FileMode) error {
	return v.c.Put(context.Background(), v.fullKey(path)+"/", bytes.NewReader(nil), 0)
}

func (v *COS) Stat(name string) (os.FileInfo, error) {
	ctx := context.Background()
	key := v.fullKey(name)
	e, err := v.c.Head(ctx, key)
	if err != nil {
		// Folder placeholders live at "<key>/" — fall back to that.
		if e2, err2 := v.c.Head(ctx, key+"/"); err2 == nil {
			key = key + "/"
			e = e2
			err = nil
		}
	}
	if err != nil {
		return nil, err
	}
	isDir := strings.HasSuffix(key, "/")
	mode := os.FileMode(0o644)
	if isDir {
		mode = os.ModeDir | 0o755
	}
	return &fileInfo{name: v.fullName(name), size: e.Size, mode: mode, modTime: e.ModifyTime, isDir: isDir}, nil
}

func (v *COS) Chmod(name string, mode os.FileMode) error         { return nil }
func (v *COS) Chown(name string, uid, gid int) error             { return nil }
func (v *COS) Chtimes(name string, atime, mtime time.Time) error { return nil }

func (v *COS) fullName(name string) string {
	name = strings.TrimLeft(name, "/")
	if name == "" {
		return v.root + "/"
	}
	return name
}

// fileInfo is a tiny os.FileInfo impl so we don't depend on a missing
// afero.NewFileInfo helper (none in afero v1.11.0).
type fileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
	isDir   bool
}

func (f *fileInfo) Name() string       { return f.name }
func (f *fileInfo) Size() int64        { return f.size }
func (f *fileInfo) Mode() os.FileMode  { return f.mode }
func (f *fileInfo) ModTime() time.Time { return f.modTime }
func (f *fileInfo) IsDir() bool        { return f.isDir }
func (f *fileInfo) Sys() interface{}   { return nil }

// readOnlyFile wraps bytes.Reader into an afero.File.
type readOnlyFile struct {
	r      *bytes.Reader
	name   string
	size   int64
	closed bool
}

func newReadOnlyFile(r *bytes.Reader, name string, size int64) *readOnlyFile {
	return &readOnlyFile{r: r, name: name, size: size}
}

func (f *readOnlyFile) Read(p []byte) (int, error)         { return f.r.Read(p) }
func (f *readOnlyFile) ReadAt(p []byte, off int64) (int, error) { return f.r.ReadAt(p, off) }
func (f *readOnlyFile) Seek(off int64, whence int) (int64, error) { return f.r.Seek(off, whence) }
func (f *readOnlyFile) Close() error                       { f.closed = true; return nil }
func (f *readOnlyFile) Name() string                       { return f.name }
func (f *readOnlyFile) Stat() (os.FileInfo, error) {
	return &fileInfo{name: f.name, size: f.size, mode: 0o644}, nil
}
func (f *readOnlyFile) Sync() error                          { return nil }
func (f *readOnlyFile) Truncate(size int64) error            { return os.ErrInvalid }
func (f *readOnlyFile) Readdir(count int) ([]os.FileInfo, error) { return nil, nil }
func (f *readOnlyFile) Readdirnames(n int) ([]string, error) { return nil, nil }
func (f *readOnlyFile) Write(p []byte) (int, error)         { return 0, os.ErrInvalid }
func (f *readOnlyFile) WriteAt(p []byte, off int64) (int, error) { return 0, os.ErrInvalid }
func (f *readOnlyFile) WriteString(s string) (int, error)   { return 0, os.ErrInvalid }

// memWriteFile is a tiny in-memory buffer that flushes to COS on Close.
type memWriteFile struct {
	buf      *bytes.Buffer
	finalKey string
	c        *cos.Client
	closed   bool
}

func (m *memWriteFile) Read(p []byte) (int, error) { return 0, os.ErrInvalid }
func (m *memWriteFile) ReadAt(p []byte, off int64) (int, error) {
	return bytes.NewReader(m.buf.Bytes()).ReadAt(p, off)
}
func (m *memWriteFile) Write(p []byte) (int, error)  { return m.buf.Write(p) }
func (m *memWriteFile) WriteAt(p []byte, off int64) (int, error) {
	if off != int64(m.buf.Len()) {
		return 0, os.ErrInvalid
	}
	return m.buf.Write(p)
}
func (m *memWriteFile) WriteString(s string) (int, error) { return m.buf.WriteString(s) }
func (m *memWriteFile) Seek(offset int64, whence int) (int64, error) {
	return int64(m.buf.Len()), nil
}
func (m *memWriteFile) Close() error {
	if m.closed {
		return nil
	}
	m.closed = true
	return m.c.Put(context.Background(), m.finalKey, bytes.NewReader(m.buf.Bytes()), int64(m.buf.Len()))
}
func (m *memWriteFile) Name() string { return m.finalKey }
func (m *memWriteFile) Stat() (os.FileInfo, error) {
	return nil, os.ErrInvalid
}
func (m *memWriteFile) Sync() error { return nil }
func (m *memWriteFile) Truncate(size int64) error {
	m.buf.Reset()
	return nil
}
func (m *memWriteFile) Readdir(count int) ([]os.FileInfo, error) { return nil, nil }
func (m *memWriteFile) Readdirnames(count int) ([]string, error)  { return nil, nil }
