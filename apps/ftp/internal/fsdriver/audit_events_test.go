package fsdriver

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/spf13/afero"

	"github.com/example/cos-ftp-server/internal/fileevents"
)

type fakeEventSink struct {
	events []fileevents.Event
}

func (f *fakeEventSink) Publish(_ context.Context, e fileevents.Event) {
	f.events = append(f.events, e)
}

func (f *fakeEventSink) DetectMIME(fs afero.Fs, name string, size int64) (string, string) {
	return fileevents.DetectMIME(fs, name, size, fileevents.MIMEFallback, fileevents.DefaultMIMESniffMaxBytes)
}

func newAuditFSWithSink(t *testing.T, inner afero.Fs, sink *fakeEventSink) *AuditFS {
	t.Helper()
	return NewAuditFS(inner, nil, "alice", nil, "ftp", "test", "/alice", AuditOptions{UserID: 42, Events: sink})
}

func mustWriteInner(t *testing.T, fs afero.Fs, name string, data []byte) {
	t.Helper()
	if err := afero.WriteFile(fs, name, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAuditFSPublishesUploadEvent(t *testing.T) {
	inner := afero.NewMemMapFs()
	sink := &fakeEventSink{}
	a := newAuditFSWithSink(t, inner, sink)

	f, err := a.Create("/invoice.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if len(sink.events) != 1 {
		t.Fatalf("events=%d, want 1", len(sink.events))
	}
	e := sink.events[0]
	if e.EventType != fileevents.EventUpload {
		t.Errorf("EventType=%q", e.EventType)
	}
	if e.Path != "invoice.txt" {
		t.Errorf("Path=%q, want invoice.txt", e.Path)
	}
	if e.Size != 5 {
		t.Errorf("Size=%d, want 5", e.Size)
	}
	if e.MIME != "text/plain" || e.MIMESource != fileevents.MIMESourceExtension {
		t.Errorf("MIME=%q/%q", e.MIME, e.MIMESource)
	}
	if e.UserID != 42 || e.Username != "alice" || e.RootFolder != "/alice" {
		t.Errorf("identity=%+v", e)
	}
	if e.SessionID == uuid.Nil {
		t.Error("SessionID is zero")
	}
	if e.Overwrite {
		t.Error("Overwrite=true for a new file")
	}
}

func TestAuditFSUploadOverwriteFlag(t *testing.T) {
	inner := afero.NewMemMapFs()
	mustWriteInner(t, inner, "invoice.txt", []byte("old"))
	sink := &fakeEventSink{}
	a := newAuditFSWithSink(t, inner, sink)

	f, err := a.Create("invoice.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("new"))
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 1 || !sink.events[0].Overwrite {
		t.Fatalf("events=%+v, want one overwrite event", sink.events)
	}
}

type failWriteFS struct{ afero.Fs }

func (f failWriteFS) Create(name string) (afero.File, error) {
	file, err := f.Fs.Create(name)
	if err != nil {
		return nil, err
	}
	return &failWriteFile{File: file}, nil
}

type failWriteFile struct{ afero.File }

func (f *failWriteFile) Write([]byte) (int, error) {
	return 0, errors.New("disk full")
}

func TestAuditFSFailedWriteDoesNotPublish(t *testing.T) {
	inner := failWriteFS{Fs: afero.NewMemMapFs()}
	sink := &fakeEventSink{}
	a := newAuditFSWithSink(t, inner, sink)

	f, err := a.Create("broken.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("data")); err == nil {
		t.Fatal("expected write error")
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 0 {
		t.Fatalf("events=%+v, want none", sink.events)
	}
}

func TestAuditFSPublishesRemoveEvent(t *testing.T) {
	inner := afero.NewMemMapFs()
	mustWriteInner(t, inner, "old.txt", []byte("hello"))
	sink := &fakeEventSink{}
	a := newAuditFSWithSink(t, inner, sink)

	if err := a.Remove("old.txt"); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 1 {
		t.Fatalf("events=%d, want 1", len(sink.events))
	}
	e := sink.events[0]
	if e.EventType != fileevents.EventRemove || e.Path != "old.txt" || e.IsDir {
		t.Errorf("event=%+v", e)
	}
	if e.Size != 5 || e.MIME != "text/plain" {
		t.Errorf("size/mime=%d/%q", e.Size, e.MIME)
	}
}

func TestAuditFSPublishesRenameEvent(t *testing.T) {
	inner := afero.NewMemMapFs()
	mustWriteInner(t, inner, "a.txt", []byte("hello"))
	sink := &fakeEventSink{}
	a := newAuditFSWithSink(t, inner, sink)

	if err := a.Rename("a.txt", "b.txt"); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 1 {
		t.Fatalf("events=%d, want 1", len(sink.events))
	}
	e := sink.events[0]
	if e.EventType != fileevents.EventRename {
		t.Errorf("EventType=%q", e.EventType)
	}
	if e.OldPath != "a.txt" || e.Path != "b.txt" {
		t.Errorf("old/new=%q/%q", e.OldPath, e.Path)
	}
	if e.Size != 5 || e.MIME != "text/plain" || e.Overwrite {
		t.Errorf("event=%+v", e)
	}
}

func TestAuditFSPublishesRemoveAllDirectory(t *testing.T) {
	inner := afero.NewMemMapFs()
	if err := inner.MkdirAll("archive", 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteInner(t, inner, "archive/f.txt", []byte("x"))
	sink := &fakeEventSink{}
	a := newAuditFSWithSink(t, inner, sink)

	if err := a.RemoveAll("archive"); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 1 {
		t.Fatalf("events=%d, want 1", len(sink.events))
	}
	e := sink.events[0]
	if e.EventType != fileevents.EventRemove || e.Path != "archive" || !e.IsDir {
		t.Errorf("event=%+v", e)
	}
}

func TestAuditFSNoSinkStillWorks(t *testing.T) {
	inner := afero.NewMemMapFs()
	a := NewAuditFS(inner, nil, "u", nil, "ftp", "test", "/", AuditOptions{})

	f, err := a.Create("x.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("x"))
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.Rename("x.txt", "y.txt"); err != nil {
		t.Fatal(err)
	}
	if err := a.Remove("y.txt"); err != nil {
		t.Fatal(err)
	}
}

func TestAuditFSMissingRemoveDoesNotPublish(t *testing.T) {
	inner := afero.NewMemMapFs()
	sink := &fakeEventSink{}
	a := newAuditFSWithSink(t, inner, sink)
	if err := a.Remove("missing.txt"); err == nil {
		t.Fatal("expected remove error")
	}
	if len(sink.events) != 0 {
		t.Fatalf("events=%+v, want none", sink.events)
	}
}
