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

func TestNewStorageRouter_DefaultOnly(t *testing.T) {
	cfg := &config.Config{StorageBackend: PluginLocal, StorageLocalRoot: t.TempDir()}
	r, err := NewStorageRouter(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Enabled(); len(got) != 1 || got[0] != "local" || r.Default() != "local" {
		t.Errorf("enabled=%v default=%q", got, r.Default())
	}
}

func TestNewStorageRouter_BothBackends(t *testing.T) {
	cfg := &config.Config{
		StorageBackend:         PluginCOS,
		StorageEnabledBackends: []string{"cos", "local"},
		StorageLocalRoot:       t.TempDir(),
		COSBucket:              "b", COSRegion: "r",
	}
	r, err := NewStorageRouter(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Enabled(); len(got) != 2 || r.Default() != "cos" {
		t.Errorf("enabled=%v default=%q", got, r.Default())
	}
	if b, err := r.Backend("local"); err != nil {
		t.Fatal(err)
	} else if _, ok := b.(*LocalPlugin); !ok {
		t.Errorf("local backend is %T", b)
	}
}

func TestNewStorageRouter_Errors(t *testing.T) {
	cases := map[string]*config.Config{
		"unknown default": {StorageBackend: "bogus.objectstorage.plugin"},
		"unknown enabled": {StorageBackend: PluginLocal, StorageEnabledBackends: []string{"local", "s3"}},
		"default missing": {StorageBackend: PluginCOS, StorageEnabledBackends: []string{"local"}},
	}
	for name, cfg := range cases {
		if _, err := NewStorageRouter(cfg, nil, nil); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestLocalPlugin_Mount_RejectsEscape(t *testing.T) {
	dir := t.TempDir()
	p := &LocalPlugin{Root: dir}
	for _, bad := range []string{"/../evil", "../evil", "/a/../../evil"} {
		if _, err := p.Mount(bad); err == nil {
			t.Errorf("Mount(%q) succeeded, want escape error", bad)
		}
	}
	if _, err := p.Mount("/ok/sub"); err != nil {
		t.Errorf("Mount(/ok/sub): %v", err)
	}
}
