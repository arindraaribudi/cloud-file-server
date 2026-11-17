package fileevents

import (
	"io"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/spf13/afero"
)

// RangeReader is an optional capability implemented by storage drivers that
// can read a small byte range without materializing the whole object. It keeps
// MIME sniffing cheap on COS.
type RangeReader interface {
	ReadRange(name string, offset, length int64) ([]byte, error)
}

// DetectMIME classifies a file by extension and, when needed, by sniffing the
// first 512 bytes. mode is fallback, always, or off. size is the file size;
// pass -1 when unknown. maxBytes caps sniffing.
func DetectMIME(fs afero.Fs, name string, size int64, mode string, maxBytes int64) (string, string) {
	mode = normalizeMode(mode)
	ext := extensionMIME(name)

	if mode == MIMEOff {
		return extensionOrNone(ext)
	}
	if mode != MIMEAlways && ext != "" && ext != "application/octet-stream" {
		return ext, MIMESourceExtension
	}
	if fs == nil || size == 0 {
		return extensionOrNone(ext)
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMIMESniffMaxBytes
	}
	if size > maxBytes {
		return extensionOrNone(ext)
	}

	head, err := readHead(fs, name, size)
	if err == nil && len(head) > 0 {
		sniffed := normalizeMediaType(http.DetectContentType(head))
		if sniffed != "" && sniffed != "application/octet-stream" {
			return sniffed, MIMESourceSniffed
		}
	}
	return extensionOrNone(ext)
}

func extensionMIME(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if ext == "" {
		return ""
	}
	return normalizeMediaType(mime.TypeByExtension(ext))
}

func extensionOrNone(ext string) (string, string) {
	if ext != "" {
		return ext, MIMESourceExtension
	}
	return "application/octet-stream", MIMESourceNone
}

func normalizeMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case MIMEAlways:
		return MIMEAlways
	case MIMEOff:
		return MIMEOff
	default:
		return MIMEFallback
	}
}

func normalizeMediaType(s string) string {
	if s == "" {
		return ""
	}
	if mt, _, err := mime.ParseMediaType(s); err == nil {
		return strings.ToLower(mt)
	}
	if i := strings.IndexByte(s, ';'); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(strings.TrimSpace(s))
}

func readHead(fs afero.Fs, name string, size int64) ([]byte, error) {
	n := int64(512)
	if size > 0 && size < n {
		n = size
	}
	if n <= 0 {
		return nil, nil
	}
	if rr, ok := fs.(RangeReader); ok {
		return rr.ReadRange(name, 0, n)
	}
	f, err := fs.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, n))
}
