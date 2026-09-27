package fsdriver

import (
	"io"
	"os"
	"testing"

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
