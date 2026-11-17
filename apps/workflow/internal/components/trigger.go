package components

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/example/cos-workflow/internal/model"
)

type MatchResult struct {
	Matched bool   `json:"matched"`
	Reason  string `json:"reason"`
}

// MatchTrigger evaluates one trigger component against a file event.
func MatchTrigger(t Type, raw json.RawMessage, ev model.FileEvent) (MatchResult, error) {
	if t.EventType == "" {
		return MatchResult{}, fmt.Errorf("component %s is not a trigger", t.Key)
	}
	cfg, err := DecodeTrigger(raw)
	if err != nil {
		return MatchResult{}, err
	}
	if ev.EventType != t.EventType {
		return MatchResult{Reason: "event type is " + ev.EventType}, nil
	}
	if cfg.ScopeUsername != "" && cfg.ScopeUsername != ev.Username {
		return MatchResult{Reason: "username scope did not match"}, nil
	}
	if ev.IsDir && !cfg.IncludeDirectories {
		return MatchResult{Reason: "directory removals are excluded"}, nil
	}

	target := ev.Path
	if cfg.PathTarget == PathTargetBase {
		target = path.Base(strings.Trim(ev.Path, "/"))
	}
	if cfg.PathRegex != "" {
		re, err := regexp.Compile(cfg.PathRegex)
		if err != nil {
			return MatchResult{}, fmt.Errorf("path_regex: %w", err)
		}
		if !re.MatchString(target) {
			return MatchResult{Reason: "path did not match path_regex"}, nil
		}
	}
	if len(cfg.MIMEPatterns) > 0 {
		if !MatchMIMEPatterns(ev.MIME, cfg.MIMEPatterns) {
			return MatchResult{Reason: "MIME did not match mime_patterns"}, nil
		}
	}
	if cfg.MIMERegex != "" {
		re, err := regexp.Compile(cfg.MIMERegex)
		if err != nil {
			return MatchResult{}, fmt.Errorf("mime_regex: %w", err)
		}
		if !re.MatchString(NormalizeMediaType(ev.MIME)) {
			return MatchResult{Reason: "MIME did not match mime_regex"}, nil
		}
	}
	return MatchResult{Matched: true, Reason: "all filters matched"}, nil
}

// MatchMIMEPatterns matches a concrete MIME type against exact or wildcard
// patterns such as application/pdf, image/*, or */*.
func MatchMIMEPatterns(mediaType string, patterns []string) bool {
	mt := NormalizeMediaType(mediaType)
	parts := strings.Split(mt, "/")
	if len(parts) != 2 {
		return false
	}
	for _, p := range patterns {
		p = strings.TrimSpace(strings.ToLower(p))
		if p == "" {
			continue
		}
		if p == "*/*" {
			return true
		}
		pp := strings.Split(p, "/")
		if len(pp) != 2 {
			continue
		}
		if pp[0] != "*" && pp[0] != parts[0] {
			continue
		}
		if pp[1] != "*" && pp[1] != parts[1] {
			continue
		}
		return true
	}
	return false
}
