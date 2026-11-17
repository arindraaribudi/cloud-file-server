package action

import (
	"fmt"
	"path"
	"strings"
	"text/template"
	"time"

	"github.com/example/cos-workflow/internal/model"
)

func templateData(rc *model.RunContext) map[string]string {
	p := strings.TrimPrefix(rc.Path, "/")
	dir := path.Dir(p)
	if dir == "." {
		dir = ""
	}
	ext := path.Ext(p)
	base := strings.TrimSuffix(path.Base(p), ext)
	when := rc.EventTime
	if when.IsZero() {
		when = time.Now().UTC()
	}
	return map[string]string{
		"FileName":  path.Base(p),
		"Base":      base,
		"Ext":       ext,
		"Dir":       dir,
		"Date":      when.Format("2006-01-02"),
		"Time":      when.Format("150405"),
		"DateTime":  when.Format("2006-01-02_150405"),
		"Username":  rc.Username,
		"MIME":      rc.MIME,
		"EventType": rc.EventType,
		"RunID":     rc.RunID.String(),
	}
}

func renderTemplate(name, text string, data map[string]string) (string, error) {
	t, err := template.New(name).Option("missingkey=error").Parse(text)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

func renderPath(text string, rc *model.RunContext) (string, error) {
	rendered, err := renderTemplate("path", text, templateData(rc))
	if err != nil {
		return "", err
	}
	return cleanRelPath(rendered)
}

func cleanRelPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return "", fmt.Errorf("path is empty")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", fmt.Errorf("path must not contain ..")
		}
	}
	clean := path.Clean(p)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("invalid path")
	}
	return clean, nil
}

func addSuffix(p, runID string) string {
	ext := path.Ext(p)
	base := strings.TrimSuffix(p, ext)
	return base + "_" + shortID(runID) + ext
}

func shortID(s string) string {
	s = strings.ReplaceAll(s, "-", "")
	if len(s) > 8 {
		s = s[:8]
	}
	if s == "" {
		return "retry"
	}
	return s
}
