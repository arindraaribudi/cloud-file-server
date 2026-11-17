package components

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/example/cos-workflow/internal/model"
)

// Type describes one component type exposed to the UI. Handlers are selected
// by Key + Version by the engine; this package owns metadata and validation.
type Type struct {
	Key           string
	Name          string
	Description   string
	Stages        []model.Stage
	EventType     string // trigger types only: upload or remove
	Version       int
	ConfigSchema  json.RawMessage
	DefaultConfig json.RawMessage
	Validate      func(json.RawMessage) error
}

type CatalogEntry struct {
	Key           string          `json:"key"`
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Stages        []model.Stage   `json:"stages"`
	EventType     string          `json:"event_type,omitempty"`
	Version       int             `json:"version"`
	ConfigSchema  json.RawMessage `json:"config_schema"`
	DefaultConfig json.RawMessage `json:"default_config"`
}

// Registry keeps every supported (type, version). Keeping historical versions
// lets saved workflows keep validating and executing after a type evolves.
type Registry struct {
	types map[string]map[int]Type
}

func NewRegistry() *Registry {
	r := &Registry{types: make(map[string]map[int]Type)}
	registerBuiltins(r)
	return r
}

func (r *Registry) add(t Type) {
	if t.Version <= 0 {
		t.Version = 1
	}
	if r.types[t.Key] == nil {
		r.types[t.Key] = make(map[int]Type)
	}
	r.types[t.Key][t.Version] = t
}

// Get resolves an exact version. A zero version means the current version.
func (r *Registry) Get(key string, version int) (Type, bool) {
	if version <= 0 {
		return r.Current(key)
	}
	versions, ok := r.types[key]
	if !ok {
		return Type{}, false
	}
	t, ok := versions[version]
	return t, ok
}

func (r *Registry) Current(key string) (Type, bool) {
	versions, ok := r.types[key]
	if !ok || len(versions) == 0 {
		return Type{}, false
	}
	max := 0
	for v := range versions {
		if v > max {
			max = v
		}
	}
	t, ok := versions[max]
	return t, ok
}

// Catalog returns the current version of every type, optionally filtered by
// stage, sorted by key for a stable UI.
func (r *Registry) Catalog(stage model.Stage) []CatalogEntry {
	out := make([]CatalogEntry, 0, len(r.types))
	for key := range r.types {
		t, ok := r.Current(key)
		if !ok {
			continue
		}
		if stage != "" && !allowedStage(t, stage) {
			continue
		}
		out = append(out, CatalogEntry{
			Key: t.Key, Name: t.Name, Description: t.Description,
			Stages: t.Stages, EventType: t.EventType, Version: t.Version,
			ConfigSchema: t.ConfigSchema, DefaultConfig: t.DefaultConfig,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func allowedStage(t Type, stage model.Stage) bool {
	for _, s := range t.Stages {
		if s == stage {
			return true
		}
	}
	return false
}

func (t Type) String() string {
	return fmt.Sprintf("%s v%d", t.Key, t.Version)
}
