package components

import (
	"encoding/json"
	"testing"

	"github.com/example/cos-workflow/internal/model"
)

func TestRegistryCatalogAndDefaults(t *testing.T) {
	reg := NewRegistry()
	all := reg.Catalog("")
	if len(all) != 8 {
		t.Fatalf("catalog=%d, want 8", len(all))
	}
	triggers := reg.Catalog(model.StageTrigger)
	if len(triggers) != 2 {
		t.Fatalf("trigger catalog=%d, want 2", len(triggers))
	}
	for _, entry := range all {
		if entry.Version != 1 {
			t.Errorf("%s version=%d", entry.Key, entry.Version)
		}
		if !json.Valid(entry.ConfigSchema) {
			t.Errorf("%s config schema is not valid JSON", entry.Key)
		}
		if !json.Valid(entry.DefaultConfig) {
			t.Errorf("%s default config is not valid JSON", entry.Key)
		}
		tpe, ok := reg.Current(entry.Key)
		if !ok {
			t.Errorf("%s missing from registry", entry.Key)
			continue
		}
		// Notification defaults intentionally start blank until an operator
		// provides a URL or recipient. The schema is still valid.
		if entry.Key == TypeOutputWebhook || entry.Key == TypeOutputEmail {
			continue
		}
		if err := tpe.Validate(tpe.DefaultConfig); err != nil {
			t.Errorf("%s default config invalid: %v", entry.Key, err)
		}
	}
}

func TestRegistryHistoricalVersion(t *testing.T) {
	reg := NewRegistry()
	if _, ok := reg.Get(TypeTriggerUpload, 1); !ok {
		t.Fatal("v1 missing")
	}
	if _, ok := reg.Get(TypeTriggerUpload, 99); ok {
		t.Fatal("unexpected v99")
	}
	if _, ok := reg.Current("missing"); ok {
		t.Fatal("unexpected missing type")
	}
}

// Every field that holds an FTP username must be tagged x-ref=user so the UI
// renders a picker over existing users instead of free text. New username
// fields must be added here deliberately.
func TestUserFieldsAreUserRefs(t *testing.T) {
	reg := NewRegistry()
	wantUserFields := map[string]bool{
		"scope_username":       true,
		"destination_username": true,
	}
	seen := map[string]bool{}
	for _, entry := range reg.Catalog("") {
		var schema struct {
			Properties map[string]map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(entry.ConfigSchema, &schema); err != nil {
			t.Fatalf("%s: %v", entry.Key, err)
		}
		for name, prop := range schema.Properties {
			isUserField := wantUserFields[name]
			ref, _ := prop["x-ref"].(string)
			if isUserField && ref != "user" {
				t.Errorf("%s.%s: x-ref=%q, want \"user\"", entry.Key, name, ref)
			}
			if isUserField {
				seen[name] = true
				if label, _ := prop["x-empty-label"].(string); label == "" {
					t.Errorf("%s.%s: optional user field needs x-empty-label", entry.Key, name)
				}
			}
			// any other field mentioning "username" must be flagged here too
			if !isUserField && (name == "username" || len(name) > 9 && name[len(name)-9:] == "_username") {
				t.Errorf("%s.%s looks like a user field but is not in the user-ref allowlist", entry.Key, name)
			}
		}
	}
	for name := range wantUserFields {
		if !seen[name] {
			t.Errorf("expected a schema field named %s", name)
		}
	}
}
