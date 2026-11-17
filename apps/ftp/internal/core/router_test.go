package core

import (
	"context"
	"errors"
	"testing"

	"github.com/spf13/afero"

	"github.com/example/cos-ftp-server/internal/db"
)

type fakeStorage struct {
	loc    string
	copies [][4]string
}

func (f *fakeStorage) Init(context.Context) error { return nil }
func (f *fakeStorage) Mount(root string) (afero.Fs, error) {
	return afero.NewBasePathFs(afero.NewMemMapFs(), root), nil
}
func (f *fakeStorage) BackendLocation() string { return f.loc }

// copyingStorage additionally supports server-side copy.
type copyingStorage struct{ fakeStorage }

func (c *copyingStorage) Copy(_ context.Context, sr, sp, dr, dp string) error {
	c.copies = append(c.copies, [4]string{sr, sp, dr, dp})
	return nil
}

func newTestRouter(t *testing.T) (*StorageRouter, *copyingStorage, *fakeStorage) {
	t.Helper()
	cos := &copyingStorage{fakeStorage{loc: "cos://b"}}
	local := &fakeStorage{loc: "local:///d"}
	r, err := NewStorageRouter(BackendCOS, map[string]ObjectStorage{BackendCOS: cos, BackendLocal: local})
	if err != nil {
		t.Fatal(err)
	}
	return r, cos, local
}

func TestNewStorageRouter_Validation(t *testing.T) {
	if _, err := NewStorageRouter("cos", nil); err == nil {
		t.Error("expected error for no backends")
	}
	if _, err := NewStorageRouter("cos", map[string]ObjectStorage{"local": &fakeStorage{}}); err == nil {
		t.Error("expected error when default is not enabled")
	}
}

func TestStorageRouter_ResolveAndEnabled(t *testing.T) {
	r, _, _ := newTestRouter(t)
	if got := r.Resolve(""); got != BackendCOS {
		t.Errorf("Resolve(\"\")=%q, want cos", got)
	}
	if got := r.Resolve("  LOCAL "); got != BackendLocal {
		t.Errorf("Resolve(LOCAL)=%q, want local", got)
	}
	if !r.Has("") || !r.Has("local") || r.Has("s3") {
		t.Error("Has() wrong")
	}
	if got := r.Enabled(); len(got) != 2 || got[0] != "cos" || got[1] != "local" {
		t.Errorf("Enabled()=%v", got)
	}
}

func TestStorageRouter_RoutesPerUser(t *testing.T) {
	r, _, _ := newTestRouter(t)
	cases := []struct {
		backend, wantLoc string
	}{
		{"", "cos://b"}, // empty -> deployment default
		{"cos", "cos://b"},
		{"local", "local:///d"},
	}
	for _, c := range cases {
		u := &db.FTPUser{RootFolder: "/alice", StorageBackend: c.backend}
		if got := r.BackendLocation(u); got != c.wantLoc {
			t.Errorf("backend %q: location=%q, want %q", c.backend, got, c.wantLoc)
		}
		if _, err := r.Mount(u); err != nil {
			t.Errorf("backend %q: Mount: %v", c.backend, err)
		}
	}
}

func TestStorageRouter_DisabledBackend(t *testing.T) {
	r, err := NewStorageRouter("local", map[string]ObjectStorage{"local": &fakeStorage{loc: "l"}})
	if err != nil {
		t.Fatal(err)
	}
	u := &db.FTPUser{RootFolder: "/x", StorageBackend: "cos"}
	if _, err := r.Mount(u); !errors.Is(err, ErrBackendNotEnabled) {
		t.Fatalf("Mount err=%v, want ErrBackendNotEnabled", err)
	}
	if got := r.BackendLocation(u); got != "" {
		t.Errorf("location=%q, want empty", got)
	}
}

func TestStorageRouter_CopyNative(t *testing.T) {
	r, cos, _ := newTestRouter(t)
	ctx := context.Background()
	a := &db.FTPUser{RootFolder: "/a", StorageBackend: "cos"}
	b := &db.FTPUser{RootFolder: "/b"} // default == cos
	l := &db.FTPUser{RootFolder: "/l", StorageBackend: "local"}
	l2 := &db.FTPUser{RootFolder: "/l2", StorageBackend: "local"}

	// same backend with Copier: handled natively
	handled, err := r.CopyNative(ctx, a, b, "x.txt", "y.txt")
	if err != nil || !handled {
		t.Fatalf("cos->cos handled=%v err=%v", handled, err)
	}
	if len(cos.copies) != 1 || cos.copies[0] != [4]string{"/a", "x.txt", "/b", "y.txt"} {
		t.Errorf("copies=%v", cos.copies)
	}
	// cross backend: NOT handled, caller must stream (see docs/cross-backend-copy.md)
	for _, pair := range [][2]*db.FTPUser{{a, l}, {l, a}} {
		handled, err = r.CopyNative(ctx, pair[0], pair[1], "x", "y")
		if err != nil || handled {
			t.Errorf("cross-backend handled=%v err=%v, want false,nil", handled, err)
		}
	}
	// same backend but no Copier: not handled
	handled, err = r.CopyNative(ctx, l, l2, "x", "y")
	if err != nil || handled {
		t.Errorf("local->local without Copier handled=%v err=%v", handled, err)
	}
	if len(cos.copies) != 1 {
		t.Errorf("unexpected extra native copies: %v", cos.copies)
	}
}
