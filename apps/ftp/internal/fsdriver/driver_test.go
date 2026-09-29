package fsdriver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"
)

// openOnlyFs is an afero.Fs whose only listings come through its own ReadDir,
// not via Open+Readdir. Mirrors COS readOnlyFile.Readdir returning nil — the
// exact shape that made FTP LIST return empty for blank folders.
type openOnlyFs struct {
	afero.Fs
	entries map[string][]os.FileInfo
}

func (f *openOnlyFs) Stat(name string) (os.FileInfo, error) {
	if _, ok := f.entries[name]; ok {
		return &fileInfo{name: name, mode: os.ModeDir | 0o755, isDir: true}, nil
	}
	return f.Fs.Stat(name)
}

func (f *openOnlyFs) ReadDir(name string) ([]os.FileInfo, error) {
	if list, ok := f.entries[name]; ok {
		return list, nil
	}
	return nil, os.ErrNotExist
}

func TestLocalDriverListsFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := NewLocal(dir)
	f, err := d.Open("/")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	infos, err := f.Readdir(-1)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Name() != "hello.txt" {
		t.Fatalf("got %+v", infos)
	}
}

// TestAuditFS_OpenDirectorySurfacesEntries reproduces the FTP blank-folder
// bug: inner Fs lists only via ReadDir (like COS) and Open+Readdir returns
// empty, AuditFS.Open must still surface directory entries. This is the
// contract ftpserverlib relies on for LIST.
func TestAuditFS_OpenDirectorySurfacesEntries(t *testing.T) {
	inner := &openOnlyFs{
		Fs: afero.NewMemMapFs(),
		entries: map[string][]os.FileInfo{
			"/": {
				&fileInfo{name: "blank", mode: os.ModeDir | 0o755, isDir: true},
				&fileInfo{name: "real.txt", mode: 0o644},
			},
		},
	}
	a := NewAuditFS(inner, nil, "u", nil, "ftp", "test", "/")

	f, err := a.Open("/")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	infos, err := f.Readdir(-1)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(infos))
	}
	names := map[string]bool{}
	for _, fi := range infos {
		names[fi.Name()] = true
	}
	if !names["blank"] {
		t.Fatal("blank folder entry missing — bug not fixed")
	}
	if !names["real.txt"] {
		t.Fatal("file entry missing")
	}
	// Find the blank entry and verify IsDir — the entry FTP was dropping.
	for _, fi := range infos {
		if fi.Name() == "blank" && !fi.IsDir() {
			t.Fatal("blank folder entry not flagged as dir")
		}
	}
}

// TestAuditFS_OpenFileKeepsDownloadPath ensures file opens (non-dir) keep
// the existing auditFile DOWNLOAD path; only directories route through dirFile.
func TestAuditFS_OpenFileKeepsDownloadPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := NewAuditFS(NewLocal(dir), nil, "u", nil, "ftp", "test", "/")
	f, err := a.Open("/f.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, ok := f.(*dirFile); ok {
		t.Fatal("file open should not return dirFile")
	}
	if _, ok := f.(*auditFile); !ok {
		t.Fatalf("file open should return auditFile, got %T", f)
	}
}
