package fsdriver

import (
	"io"
	"os"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"github.com/spf13/afero"
)

func TestSFTPHandlers_WriteReadRoundtrip(t *testing.T) {
	fs := afero.NewMemMapFs()
	h := NewSFTPHandlers(fs)

	w, err := h.FilePut.Filewrite(&sftp.Request{Method: "Put", Filepath: "/hello.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteAt([]byte("hello"), 0); err != nil {
		t.Fatal(err)
	}
	if closer, ok := w.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			t.Fatal(err)
		}
	}

	r, err := h.FileGet.Fileread(&sftp.Request{Method: "Get", Filepath: "/hello.txt"})
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := r.ReadAt(buf, 0); err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if string(buf) != "hello" {
		t.Fatalf("got %q, want %q", buf, "hello")
	}
	if closer, ok := r.(io.Closer); ok {
		_ = closer.Close()
	}
}

func TestSFTPHandlers_MkdirRenameRemove(t *testing.T) {
	fs := afero.NewMemMapFs()
	h := NewSFTPHandlers(fs)

	if err := h.FileCmd.Filecmd(&sftp.Request{Method: "Mkdir", Filepath: "/dir"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Stat("/dir"); err != nil {
		t.Fatalf("expected /dir to exist: %v", err)
	}

	if err := afero.WriteFile(fs, "/dir/a.txt", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.FileCmd.Filecmd(&sftp.Request{Method: "Rename", Filepath: "/dir/a.txt", Target: "/dir/b.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Stat("/dir/b.txt"); err != nil {
		t.Fatalf("expected renamed file: %v", err)
	}

	if err := h.FileCmd.Filecmd(&sftp.Request{Method: "Remove", Filepath: "/dir/b.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Stat("/dir/b.txt"); err == nil {
		t.Fatal("expected file to be removed")
	}
}

func TestSFTPHandlers_ListDirectory(t *testing.T) {
	fs := afero.NewMemMapFs()
	h := NewSFTPHandlers(fs)
	if err := afero.WriteFile(fs, "/a.txt", []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, "/b.txt", []byte("2"), 0o644); err != nil {
		t.Fatal(err)
	}

	lister, err := h.FileList.Filelist(&sftp.Request{Method: "List", Filepath: "/"})
	if err != nil {
		t.Fatal(err)
	}
	dst := make([]os.FileInfo, 10)
	n, err := lister.ListAt(dst, 0)
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 entries, got %d", n)
	}
}

// stubListFs wraps afero.Fs but exposes its own ReadDir that returns a
// canned entry set, independent of what Open+Readdir would yield. Models
// prefix-listing backends (e.g. COS) where the Fs implements ReadDir but
// Open on a folder returns an empty placeholder file that Readdir reads
// as zero entries — the exact failure mode that previously made SFTP
// listings diverge from FTP.
type stubListFs struct {
	afero.Fs
	entries map[string][]os.FileInfo
}

func (s *stubListFs) ReadDir(name string) ([]os.FileInfo, error) {
	return s.entries[name], nil
}

// stubFileInfo is a minimal os.FileInfo for tests. afero v1.11.0 has no
// exported NewFileInfo helper, so build one locally.
type stubFileInfo struct {
	name string
	size int64
	mode os.FileMode
}

func (f *stubFileInfo) Name() string       { return f.name }
func (f *stubFileInfo) Size() int64        { return f.size }
func (f *stubFileInfo) Mode() os.FileMode  { return f.mode }
func (f *stubFileInfo) ModTime() time.Time { return time.Time{} }
func (f *stubFileInfo) IsDir() bool        { return f.mode.IsDir() }
func (f *stubFileInfo) Sys() interface{}   { return nil }

func TestSFTPHandlers_ListDirectory_UsesFsReadDir(t *testing.T) {
	fs := afero.NewMemMapFs()
	want := []os.FileInfo{
		&stubFileInfo{name: "via-custom-readdir", mode: 0o644},
		&stubFileInfo{name: "another-entry", mode: 0o644},
	}
	stub := &stubListFs{Fs: fs, entries: map[string][]os.FileInfo{"/": want}}
	h := NewSFTPHandlers(stub)

	lister, err := h.FileList.Filelist(&sftp.Request{Method: "List", Filepath: "/"})
	if err != nil {
		t.Fatal(err)
	}
	dst := make([]os.FileInfo, 10)
	n, err := lister.ListAt(dst, 0)
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if n != len(want) {
		t.Fatalf("expected %d entries from Fs.ReadDir, got %d", len(want), n)
	}
	for i, fi := range want {
		if got := dst[i].Name(); got != fi.Name() {
			t.Fatalf("entry %d: got %q, want %q", i, got, fi.Name())
		}
	}
}
