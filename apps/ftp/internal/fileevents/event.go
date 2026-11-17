package fileevents

import (
	"time"

	"github.com/google/uuid"
)

// Event types written to public.file_events.
const (
	EventUpload = "upload"
	EventRemove = "remove"
	EventRename = "rename"
)

// MIME detection modes.
const (
	MIMEFallback = "fallback"
	MIMEAlways   = "always"
	MIMEOff      = "off"
)

// MIME sources recorded alongside the detected type.
const (
	MIMESourceExtension = "extension"
	MIMESourceSniffed   = "sniffed"
	MIMESourceNone      = "none"
)

// DefaultMIMESniffMaxBytes is the default cap for content sniffing. Larger
// files are classified by extension only so an upload close never pulls a
// large object through the app just to classify it.
const DefaultMIMESniffMaxBytes int64 = 8 << 20

// Event is the durable handoff between the file server and the workflow
// service. It mirrors public.file_events.
type Event struct {
	EventTime       time.Time `json:"event_time"`
	EventType       string    `json:"event_type"`
	UserID          int64     `json:"user_id,omitempty"`
	Username        string    `json:"username"`
	RootFolder      string    `json:"root_folder"`
	ConnectionType  string    `json:"connection_type"`
	BackendLocation string    `json:"backend_location"`
	Path            string    `json:"path"`
	OldPath         string    `json:"old_path,omitempty"`
	IsDir           bool      `json:"is_dir"`
	Size            int64     `json:"size"`
	MIME            string    `json:"mime"`
	MIMESource      string    `json:"mime_source"`
	Overwrite       bool      `json:"overwrite"`
	SessionID       uuid.UUID `json:"session_id"`
}
