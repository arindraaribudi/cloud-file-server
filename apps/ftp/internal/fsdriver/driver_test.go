package fsdriver

import (
	"os"
	"path/filepath"
	"testing"
)

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
	defer f.Close()
	infos, err := f.Readdir(-1)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Name() != "hello.txt" {
		t.Fatalf("got %+v", infos)
	}
}
