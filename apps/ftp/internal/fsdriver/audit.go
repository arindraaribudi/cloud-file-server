package fsdriver

import (
	"os"

	"github.com/google/uuid"
	"github.com/spf13/afero"

	"github.com/example/cos-ftp-server/internal/audit"
)

// AuditFS wraps a per-user ClientDriver so every file command (upload,
// download, delete, rename, mkdir) is recorded in the audit trail. All
// events for one FTP connection share a session ID.
type AuditFS struct {
	afero.Fs
	audit    *audit.Logger
	username string
	session  uuid.UUID
}

func NewAuditFS(fs afero.Fs, log *audit.Logger, username string) *AuditFS {
	return &AuditFS{Fs: fs, audit: log, username: username, session: uuid.New()}
}

func (a *AuditFS) log(action, path string, bytes int64, err error) {
	a.audit.Log(audit.Event{
		Username: a.username, SessionID: a.session,
		Action: action, Path: path, Bytes: bytes, Success: err == nil,
	})
}

func (a *AuditFS) Open(name string) (afero.File, error) {
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
// separate from DOWNLOAD/UPLOAD. Fs implementations that provide a direct
// listing (e.g. COS, which lists by prefix instead of opening a virtual
// directory) are called directly; otherwise fall back to Open+Readdir like
// ftpserverlib itself would.
func (a *AuditFS) ReadDir(name string) ([]os.FileInfo, error) {
	var (
		entries []os.FileInfo
		err     error
	)
	if lister, ok := a.Fs.(interface {
		ReadDir(string) ([]os.FileInfo, error)
	}); ok {
		entries, err = lister.ReadDir(name)
	} else {
		entries, err = afero.ReadDir(a.Fs, name)
	}
	a.log("LIST", name, 0, err)
	return entries, err
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
