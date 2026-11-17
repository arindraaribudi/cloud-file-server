package fileevents

import (
	"testing"

	"github.com/spf13/afero"
)

var pngHeader = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}

func mustWrite(t *testing.T, fs afero.Fs, name string, data []byte) {
	t.Helper()
	if err := afero.WriteFile(fs, name, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectMIMEExtensionWinsInFallback(t *testing.T) {
	fs := afero.NewMemMapFs()
	mustWrite(t, fs, "report.txt", pngHeader)
	got, src := DetectMIME(fs, "report.txt", int64(len(pngHeader)), MIMEFallback, DefaultMIMESniffMaxBytes)
	if got != "text/plain" || src != MIMESourceExtension {
		t.Fatalf("got %q/%q, want text/plain/extension", got, src)
	}
}

func TestDetectMIMESniffsUnknownExtension(t *testing.T) {
	fs := afero.NewMemMapFs()
	mustWrite(t, fs, "blob.unknownext", pngHeader)
	got, src := DetectMIME(fs, "blob.unknownext", int64(len(pngHeader)), MIMEFallback, DefaultMIMESniffMaxBytes)
	if got != "image/png" || src != MIMESourceSniffed {
		t.Fatalf("got %q/%q, want image/png/sniffed", got, src)
	}
}

func TestDetectMIMEAlwaysOverridesExtension(t *testing.T) {
	fs := afero.NewMemMapFs()
	mustWrite(t, fs, "report.txt", pngHeader)
	got, src := DetectMIME(fs, "report.txt", int64(len(pngHeader)), MIMEAlways, DefaultMIMESniffMaxBytes)
	if got != "image/png" || src != MIMESourceSniffed {
		t.Fatalf("got %q/%q, want image/png/sniffed", got, src)
	}
}

func TestDetectMIMEOffUsesExtensionOnly(t *testing.T) {
	fs := afero.NewMemMapFs()
	mustWrite(t, fs, "blob.unknownext", pngHeader)
	got, src := DetectMIME(fs, "blob.unknownext", int64(len(pngHeader)), MIMEOff, DefaultMIMESniffMaxBytes)
	if got != "application/octet-stream" || src != MIMESourceNone {
		t.Fatalf("got %q/%q, want octet/none", got, src)
	}
}

func TestDetectMIMETextSniff(t *testing.T) {
	fs := afero.NewMemMapFs()
	data := []byte("hello, workflow")
	mustWrite(t, fs, "notes.unknownext", data)
	got, src := DetectMIME(fs, "notes.unknownext", int64(len(data)), MIMEFallback, DefaultMIMESniffMaxBytes)
	if got != "text/plain" || src != MIMESourceSniffed {
		t.Fatalf("got %q/%q, want text/plain/sniffed", got, src)
	}
}

func TestDetectMIMEMaxBytesSkipsSniff(t *testing.T) {
	fs := afero.NewMemMapFs()
	mustWrite(t, fs, "blob.unknownext", pngHeader)
	got, src := DetectMIME(fs, "blob.unknownext", 9<<20, MIMEFallback, 8<<20)
	if got != "application/octet-stream" || src != MIMESourceNone {
		t.Fatalf("got %q/%q, want octet/none", got, src)
	}
}

func TestDetectMIMEEmptyUnknown(t *testing.T) {
	fs := afero.NewMemMapFs()
	mustWrite(t, fs, "empty.unknownext", nil)
	got, src := DetectMIME(fs, "empty.unknownext", 0, MIMEFallback, DefaultMIMESniffMaxBytes)
	if got != "application/octet-stream" || src != MIMESourceNone {
		t.Fatalf("got %q/%q, want octet/none", got, src)
	}
}

type rangeFS struct {
	afero.Fs
	reads int
}

func (f *rangeFS) ReadRange(string, int64, int64) ([]byte, error) {
	f.reads++
	return pngHeader, nil
}

func TestDetectMIMEUsesRangeReader(t *testing.T) {
	fs := &rangeFS{Fs: afero.NewMemMapFs()}
	mustWrite(t, fs.Fs, "blob.unknownext", pngHeader)
	got, src := DetectMIME(fs, "blob.unknownext", int64(len(pngHeader)), MIMEFallback, DefaultMIMESniffMaxBytes)
	if got != "image/png" || src != MIMESourceSniffed {
		t.Fatalf("got %q/%q, want image/png/sniffed", got, src)
	}
	if fs.reads != 1 {
		t.Fatalf("ReadRange calls=%d, want 1", fs.reads)
	}
}
