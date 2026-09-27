package fsdriver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/example/cos-ftp-server/internal/config"
)

func TestNewObjectStorage_UnknownPlugin(t *testing.T) {
	_, err := NewObjectStorage("bogus.objectstorage.plugin", &config.Config{}, nil, nil)
	if err == nil {
		t.Fatal("expected error for unknown plugin id")
	}
}

func TestNewObjectStorage_Local(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewObjectStorage(PluginLocal, &config.Config{StorageLocalRoot: dir}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := storage.(*LocalPlugin); !ok {
		t.Fatalf("got %T, want *LocalPlugin", storage)
	}
}

func TestLocalPlugin_Mount_CreatesUserDir(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewObjectStorage(PluginLocal, &config.Config{StorageLocalRoot: dir}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Mount("someuser"); err != nil {
		t.Fatalf("Mount returned error: %v", err)
	}
	userDir := filepath.Join(dir, "someuser")
	info, err := os.Stat(userDir)
	if err != nil {
		t.Fatalf("expected user dir to exist, stat failed: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected %s to be a directory", userDir)
	}
}

func TestNewObjectStorage_COS(t *testing.T) {
	storage, err := NewObjectStorage(PluginCOS, &config.Config{COSBucket: "b", COSRegion: "r"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := storage.(*COSPlugin); !ok {
		t.Fatalf("got %T, want *COSPlugin", storage)
	}
}
