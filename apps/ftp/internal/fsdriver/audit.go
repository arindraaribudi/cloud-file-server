package fsdriver

import (
	"io"
	"net"
	"os"

	"github.com/google/uuid"
	"github.com/spf13/afero"

	"github.com/example/cos-ftp-server/internal/audit"
)

// AuditFS wraps a per-user ClientDriver so every file command (upload,
// download, delete, rename, mkdir) is recorded in the audit trail. All
// events for one FTP/SFTP connection share a session ID, client IP,
// connection type, and root folder.
type AuditFS struct {
	afero.Fs
	audit           *audit.Logger
	username        string
	clientIP        net.IP
	connectionType  string // "ftp" or "sftp"
	backendLocation string // "cos" or "local"
	rootFolder      string
	session         uuid.UUID
}

func NewAuditFS(fs afero.Fs, log *audit.Logger, username string, clientIP net.IP, connectionType, backendLocation, rootFolder string) *AuditFS {
	return &AuditFS{Fs: fs, audit: log, username: username, clientIP: clientIP, connectionType: connectionType, backendLocation: backendLocation, rootFolder: rootFolder, session: uuid.New()}
}

func (a *AuditFS) log(action, path string, bytes int64, err error) {
	a.audit.Log(audit.Event{
		Username: a.username, ClientIP: a.clientIP, SessionID: a.session,
		Action: action, Path: path, Bytes: bytes, Success: err == nil,
		ConnectionType: a.connectionType, BackendLocation: a.backendLocation, RootFolder: a.rootFolder,
	})
}

func (a *AuditFS) Open(name string) (afero.File, error) {
	// ftpserverlib's LIST handler does Open+Readdir on the file handle, not
	// Fs.ReadDir. For directory paths, return a dirFile that exposes the
	// underlying Fs.ReadDir (which surfaces empty-folder placeholders on
	// COS); file paths keep the auditFile DOWNLOAD path.
	if info, err := a.Stat(name); err == nil && info.IsDir() {
		entries, err := a.listDir(name)
		a.log("LIST", name, 0, err)
		if err != nil {
			return nil, err
		}
		return &dirFile{name: name, entries: entries}, nil
	}
	f, err := a.Fs.Open(name)
	if err != nil {
		a.log("DOWNLOAD", name, 0, err)
		return nil, err
	}
	return &auditFile{File: f, onClose: func(n int64) { a.log("DOWNLOAD", name, n, nil) }}, nil
}

func (a *AuditFS) Create(name string) (afero.File, error) {
	f, err := a.Fs.Create(name)
	if err != nil {
		a.log("UPLOAD", name, 0, err)
		return nil, err
	}
	return &auditFile{File: f, onClose: func(n int64) { a.log("UPLOAD", name, n, nil) }}, nil
}

func (a *AuditFS) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	f, err := a.Fs.OpenFile(name, flag, perm)
	action := "DOWNLOAD"
	if flag&(os.O_WRONLY|os.O_RDWR) != 0 {
		action = "UPLOAD"
	}
	if err != nil {
		a.log(action, name, 0, err)
		return nil, err
	}
	return &auditFile{File: f, onClose: func(n int64) { a.log(action, name, n, nil) }}, nil
}

// ReadDir handles the FTP LIST command, logged as its own "LIST" action
// separate from DOWNLOAD/UPLOAD. Used directly by SFTP and the admin web;
// FTP goes through Open+Readdir instead and is routed via dirFile in Open.
func (a *AuditFS) ReadDir(name string) ([]os.FileInfo, error) {
	entries, err := a.listDir(name)
	a.log("LIST", name, 0, err)
	return entries, err
}

// listDir prefers the inner Fs's own ReadDir when exposed (e.g. COS, which
// lists by prefix and surfaces empty-folder placeholders), otherwise falls
// back to Open+Readdir like ftpserverlib itself would.
func (a *AuditFS) listDir(name string) ([]os.FileInfo, error) {
	if lister, ok := a.Fs.(interface {
		ReadDir(string) ([]os.FileInfo, error)
	}); ok {
		return lister.ReadDir(name)
	}
	return afero.ReadDir(a.Fs, name)
}

func (a *AuditFS) Remove(name string) error {
	err := a.Fs.Remove(name)
	a.log("DELETE", name, 0, err)
	return err
}

func (a *AuditFS) RemoveAll(path string) error {
	err := a.Fs.RemoveAll(path)
	a.log("DELETE", path, 0, err)
	return err
}

func (a *AuditFS) Rename(oldname, newname string) error {
	err := a.Fs.Rename(oldname, newname)
	a.log("RENAME", oldname+" -> "+newname, 0, err)
	return err
}

func (a *AuditFS) Mkdir(name string, perm os.FileMode) error {
	err := a.Fs.Mkdir(name, perm)
	a.log("MKDIR", name, 0, err)
	return err
}

func (a *AuditFS) MkdirAll(path string, perm os.FileMode) error {
	err := a.Fs.MkdirAll(path, perm)
	a.log("MKDIR", path, 0, err)
	return err
}

// auditFile counts bytes transferred through Read/Write and fires onClose
// once, so upload/download size lands in the audit event's Bytes field.
type auditFile struct {
	afero.File
	n       int64
	onClose func(bytes int64)
	closed  bool
}

func (f *auditFile) Read(p []byte) (int, error) {
	n, err := f.File.Read(p)
	f.n += int64(n)
	return n, err
}

func (f *auditFile) Write(p []byte) (int, error) {
	n, err := f.File.Write(p)
	f.n += int64(n)
	return n, err
}

func (f *auditFile) Close() error {
	err := f.File.Close()
	if !f.closed {
		f.closed = true
		f.onClose(f.n)
	}
	return err
}

// dirFile is a read-only afero.File backed by a pre-fetched directory listing.
// ftpserverlib drives FTP LIST through Open(name) → f.Readdir(-1) on the
// returned handle, never through Fs.ReadDir; without this wrapper COS-backed
// directories whose only contents are folder placeholders come back empty
// because readOnlyFile.Readdir returns nil. Readdir/Readdirnames honor the
// same count semantics as os.File.Readdir (count<=0 drains the rest).
type dirFile struct {
	name    string
	entries []os.FileInfo
	offset  int
	closed  bool
}

func (d *dirFile) Read([]byte) (int, error)                { return 0, io.EOF }
func (d *dirFile) ReadAt([]byte, int64) (int, error)       { return 0, io.EOF }
func (d *dirFile) Seek(int64, int) (int64, error)          { return 0, nil }
func (d *dirFile) Close() error                            { d.closed = true; return nil }
func (d *dirFile) Sync() error                             { return nil }
func (d *dirFile) Truncate(int64) error                    { return os.ErrInvalid }
func (d *dirFile) Write([]byte) (int, error)               { return 0, os.ErrInvalid }
func (d *dirFile) WriteAt([]byte, int64) (int, error)      { return 0, os.ErrInvalid }
func (d *dirFile) WriteString(string) (int, error)         { return 0, os.ErrInvalid }

func (d *dirFile) Name() string { return d.name }

func (d *dirFile) Stat() (os.FileInfo, error) {
	return &fileInfo{name: d.name, mode: os.ModeDir | 0o755, isDir: true}, nil
}

func (d *dirFile) Readdir(count int) ([]os.FileInfo, error) {
	if d.offset >= len(d.entries) {
		if count <= 0 {
			return nil, nil
		}
		return nil, io.EOF
	}
	if count <= 0 {
		out := d.entries[d.offset:]
		d.offset = len(d.entries)
		return out, nil
	}
	end := d.offset + count
	if end > len(d.entries) {
		end = len(d.entries)
	}
	out := d.entries[d.offset:end]
	d.offset = end
	if d.offset >= len(d.entries) {
		return out, io.EOF
	}
	return out, nil
}

func (d *dirFile) Readdirnames(count int) ([]string, error) {
	infos, err := d.Readdir(count)
	if err != nil || infos == nil {
		return nil, err
	}
	out := make([]string, len(infos))
	for i, fi := range infos {
		out[i] = fi.Name()
	}
	return out, nil
}
