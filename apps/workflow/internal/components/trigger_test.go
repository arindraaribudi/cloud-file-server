package components

import (
	"encoding/json"
	"testing"

	"github.com/example/cos-workflow/internal/model"
)

func TestMatchTriggerUploadPathAndMIME(t *testing.T) {
	reg := NewRegistry()
	tpe, _ := reg.Current(TypeTriggerUpload)
	cfg := json.RawMessage(`{"path_target":"basename","path_regex":"^invoice_.*\\.pdf$","mime_patterns":["application/pdf"]}`)
	ev := model.FileEvent{EventType: "upload", Path: "invoices/invoice_123.pdf", MIME: "application/pdf"}
	res, err := MatchTrigger(tpe, cfg, ev)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Matched {
		t.Fatalf("not matched: %s", res.Reason)
	}
}

func TestMatchTriggerRejectsWrongMIME(t *testing.T) {
	reg := NewRegistry()
	tpe, _ := reg.Current(TypeTriggerUpload)
	cfg := json.RawMessage(`{"path_target":"basename","path_regex":"^invoice_","mime_patterns":["application/pdf"]}`)
	ev := model.FileEvent{EventType: "upload", Path: "invoice_123.jpg", MIME: "image/jpeg"}
	res, err := MatchTrigger(tpe, cfg, ev)
	if err != nil {
		t.Fatal(err)
	}
	if res.Matched {
		t.Fatal("expected MIME mismatch")
	}
}

func TestMatchTriggerRemoveDirectoryToggle(t *testing.T) {
	reg := NewRegistry()
	tpe, _ := reg.Current(TypeTriggerRemove)
	base := json.RawMessage(`{"path_target":"full_path","path_regex":"^archive$","include_directories":false}`)
	ev := model.FileEvent{EventType: "remove", Path: "archive", IsDir: true}
	res, err := MatchTrigger(tpe, base, ev)
	if err != nil {
		t.Fatal(err)
	}
	if res.Matched {
		t.Fatal("directory should be excluded")
	}
	withDirs := json.RawMessage(`{"path_target":"full_path","path_regex":"^archive$","include_directories":true}`)
	res, err = MatchTrigger(tpe, withDirs, ev)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Matched {
		t.Fatalf("directory should match: %s", res.Reason)
	}
}

func TestMatchMIMEPatterns(t *testing.T) {
	cases := []struct {
		mime string
		pat  string
		want bool
	}{
		{"application/pdf", "application/pdf", true},
		{"image/png", "image/*", true},
		{"image/png", "*/*", true},
		{"text/plain", "image/*", false},
		{"application/json; charset=utf-8", "application/json", true},
	}
	for _, tc := range cases {
		if got := MatchMIMEPatterns(tc.mime, []string{tc.pat}); got != tc.want {
			t.Errorf("%s vs %s = %v, want %v", tc.mime, tc.pat, got, tc.want)
		}
	}
}
