package components

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"text/template"

	"github.com/example/cos-workflow/internal/model"
)

const (
	TypeTriggerUpload    = "trigger.file.upload"
	TypeTriggerRemove    = "trigger.file.remove"
	TypeProcessRename    = "process.file.rename"
	TypeProcessRemove    = "process.file.remove"
	TypeProcessCondition = "process.condition"
	TypeOutputCopy       = "output.file.copy"
	TypeOutputWebhook    = "output.notify.webhook"
	TypeOutputEmail      = "output.notify.email"

	PathTargetBase = "basename"
	PathTargetFull = "full_path"

	CollisionFail      = "fail"
	CollisionSuffix    = "suffix"
	CollisionOverwrite = "overwrite"
)

var (
	defaultTriggerConfig   = json.RawMessage(`{"path_target":"basename","path_regex":"","mime_patterns":[],"mime_regex":"","scope_username":"","include_directories":false}`)
	defaultRenameConfig    = json.RawMessage(`{"mode":"template","template":"archive/{{.Date}}/{{.Base}}{{.Ext}}","pattern":"","replacement":"","collision":"fail"}`)
	defaultRemoveConfig    = json.RawMessage(`{"path_template":"","missing_ok":true}`)
	defaultCopyConfig      = json.RawMessage(`{"destination_username":"","destination_path":"shared/{{.Date}}/{{.FileName}}","collision":"suffix"}`)
	defaultWebhookConfig   = json.RawMessage(`{"url":"","method":"POST","secret_env":"","body_template":"","headers":{}}`)
	defaultEmailConfig     = json.RawMessage(`{"to":[],"subject_template":"","body_template":""}`)
	defaultConditionConfig = json.RawMessage(`{"path_target":"basename","path_regex":"","mime_patterns":[],"mime_regex":"","negate":false}`)

	schemaTrigger = json.RawMessage(`
{"type":"object","properties":{
  "path_target":{"type":"string","enum":["basename","full_path"],"default":"basename","title":"Match against"},
  "path_regex":{"type":"string","title":"Path regex","description":"RE2 regex; empty matches any path."},
  "mime_patterns":{"type":"array","items":{"type":"string"},"title":"MIME types","description":"Exact or wildcard, e.g. application/pdf or image/*."},
  "mime_regex":{"type":"string","title":"MIME regex","description":"Optional advanced MIME filter."},
  "scope_username":{"type":"string","title":"Only this user","description":"Leave on \"All users\" to match every user.","x-ref":"user","x-empty-label":"All users"}
},"required":["path_target"],"additionalProperties":false}
`)
	schemaTriggerRemove = json.RawMessage(`
{"type":"object","properties":{
  "path_target":{"type":"string","enum":["basename","full_path"],"default":"basename","title":"Match against"},
  "path_regex":{"type":"string","title":"Path regex"},
  "mime_patterns":{"type":"array","items":{"type":"string"},"title":"MIME types"},
  "mime_regex":{"type":"string","title":"MIME regex"},
  "scope_username":{"type":"string","title":"Only this user","x-ref":"user","x-empty-label":"All users"},
  "include_directories":{"type":"boolean","default":false,"title":"Include folder removals"}
},"required":["path_target"],"additionalProperties":false}
`)
	schemaRename = json.RawMessage(`
{"type":"object","properties":{
  "mode":{"type":"string","enum":["template","regex"],"default":"template","title":"Mode"},
  "template":{"type":"string","title":"New path template","description":"Supports {{.FileName}} {{.Base}} {{.Ext}} {{.Dir}} {{.Date}} {{.Time}} {{.DateTime}} {{.Username}} {{.MIME}} {{.EventType}} {{.RunID}}."},
  "pattern":{"type":"string","title":"Regex pattern","description":"RE2, applied to the basename."},
  "replacement":{"type":"string","title":"Regex replacement","description":"Go replacement syntax, e.g. photo_$1.jpg."},
  "collision":{"type":"string","enum":["fail","suffix","overwrite"],"default":"fail","title":"On collision"}
},"required":["mode","collision"],"additionalProperties":false}
`)
	schemaRemove = json.RawMessage(`
{"type":"object","properties":{
  "path_template":{"type":"string","title":"Optional path","description":"Empty removes the current path. A template can remove a sidecar."},
  "missing_ok":{"type":"boolean","default":true,"title":"Missing file is ok"}
},"additionalProperties":false}
`)
	schemaCopy = json.RawMessage(`
{"type":"object","properties":{
  "destination_username":{"type":"string","title":"Destination user","description":"Leave on \"Same as source user\" to copy within the same user.","x-ref":"user","x-empty-label":"Same as source user"},
  "destination_path":{"type":"string","title":"Destination folder / path","description":"Where to put the copy, e.g. folderA/{{.FileName}}. You can use {{.FileName}} {{.Date}} {{.Username}}."},
  "collision":{"type":"string","enum":["fail","suffix","overwrite"],"default":"suffix","title":"On collision"}
},"required":["destination_path","collision"],"additionalProperties":false}
`)
	schemaWebhook = json.RawMessage(`
{"type":"object","properties":{
  "url":{"type":"string","title":"URL"},
  "method":{"type":"string","enum":["POST","PUT","PATCH","DELETE","GET"],"default":"POST","title":"HTTP method","description":"GET and DELETE send no body."},
  "secret_env":{"type":"string","title":"HMAC secret env var","description":"Environment variable name; the value is never stored in the database."},
  "body_template":{"type":"string","title":"Body template","description":"Empty sends the default JSON payload."},
  "headers":{"type":"object","additionalProperties":{"type":"string"},"title":"Extra headers"}
},"required":["url"],"additionalProperties":false}
`)
	schemaEmail = json.RawMessage(`
{"type":"object","properties":{
  "to":{"type":"array","items":{"type":"string"},"title":"Recipients"},
  "subject_template":{"type":"string","title":"Subject template"},
  "body_template":{"type":"string","title":"Body template"}
},"required":["to"],"additionalProperties":false}
`)
	schemaCondition = json.RawMessage(`
{"type":"object","properties":{
  "path_target":{"type":"string","enum":["basename","full_path"],"default":"basename","title":"Match against"},
  "path_regex":{"type":"string","title":"Path regex","description":"If this does not match, the condition is false."},
  "mime_patterns":{"type":"array","items":{"type":"string"},"title":"MIME types","description":"Empty means any MIME."},
  "mime_regex":{"type":"string","title":"MIME regex","description":"Optional advanced MIME filter."},
  "negate":{"type":"boolean","default":false,"title":"Negate","description":"Run only when the filters do NOT match."}
},"required":["path_target"],"additionalProperties":false}
`)
)

type TriggerConfig struct {
	PathTarget         string   `json:"path_target"`
	PathRegex          string   `json:"path_regex"`
	MIMEPatterns       []string `json:"mime_patterns"`
	MIMERegex          string   `json:"mime_regex"`
	ScopeUsername      string   `json:"scope_username"`
	IncludeDirectories bool     `json:"include_directories"`
}

type RenameConfig struct {
	Mode        string `json:"mode"`
	Template    string `json:"template"`
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
	Collision   string `json:"collision"`
}

type RemoveConfig struct {
	PathTemplate string `json:"path_template"`
	MissingOK    bool   `json:"missing_ok"`
}

type CopyConfig struct {
	DestinationUsername string `json:"destination_username"`
	DestinationPath     string `json:"destination_path"`
	Collision           string `json:"collision"`
}

type WebhookConfig struct {
	URL          string            `json:"url"`
	Method       string            `json:"method"`
	SecretEnv    string            `json:"secret_env"`
	BodyTemplate string            `json:"body_template"`
	Headers      map[string]string `json:"headers"`
}

type EmailConfig struct {
	To              []string `json:"to"`
	SubjectTemplate string   `json:"subject_template"`
	BodyTemplate    string   `json:"body_template"`
}

type ConditionConfig struct {
	PathTarget   string   `json:"path_target"`
	PathRegex    string   `json:"path_regex"`
	MIMEPatterns []string `json:"mime_patterns"`
	MIMERegex    string   `json:"mime_regex"`
	Negate       bool     `json:"negate"`
}

func DecodeCondition(raw json.RawMessage) (ConditionConfig, error) {
	var c ConditionConfig
	if err := decodeStrict(raw, &c); err != nil {
		return c, err
	}
	if c.PathTarget == "" {
		c.PathTarget = PathTargetBase
	}
	if c.MIMEPatterns == nil {
		c.MIMEPatterns = []string{}
	}
	if err := validateCondition(&c); err != nil {
		return c, err
	}
	return c, nil
}

func registerBuiltins(r *Registry) {
	r.add(Type{
		Key: TypeTriggerUpload, Name: "File uploaded",
		Description: "Match upload events with path and MIME filters.",
		Stages:      []model.Stage{model.StageTrigger}, EventType: "upload", Version: 1,
		ConfigSchema: schemaTrigger, DefaultConfig: defaultTriggerConfig,
		Validate: func(raw json.RawMessage) error { _, err := DecodeTrigger(raw); return err },
	})
	r.add(Type{
		Key: TypeTriggerRemove, Name: "File removed",
		Description: "Match remove events with path and MIME filters.",
		Stages:      []model.Stage{model.StageTrigger}, EventType: "remove", Version: 1,
		ConfigSchema: schemaTriggerRemove, DefaultConfig: defaultTriggerConfig,
		Validate: func(raw json.RawMessage) error { _, err := DecodeTrigger(raw); return err },
	})
	r.add(Type{
		Key: TypeProcessRename, Name: "Rename file",
		Description: "Rename or move the current file with a template or regex.",
		Stages:      []model.Stage{model.StageProcess}, Version: 1,
		ConfigSchema: schemaRename, DefaultConfig: defaultRenameConfig,
		Validate: func(raw json.RawMessage) error { _, err := DecodeRename(raw); return err },
	})
	r.add(Type{
		Key: TypeProcessRemove, Name: "Remove file",
		Description: "Remove the current file or a sidecar path.",
		Stages:      []model.Stage{model.StageProcess}, Version: 1,
		ConfigSchema: schemaRemove, DefaultConfig: defaultRemoveConfig,
		Validate: func(raw json.RawMessage) error { _, err := DecodeRemove(raw); return err },
	})
	r.add(Type{
		Key: TypeProcessCondition, Name: "Condition",
		Description: "Continue only when the path/MIME filters match; otherwise stop the run.",
		Stages:      []model.Stage{model.StageProcess}, Version: 1,
		ConfigSchema: schemaCondition, DefaultConfig: defaultConditionConfig,
		Validate: func(raw json.RawMessage) error { _, err := DecodeCondition(raw); return err },
	})
	r.add(Type{
		Key: TypeOutputCopy, Name: "Copy file",
		Description: "Copy the current file to another folder or user.",
		Stages:      []model.Stage{model.StageOutput}, Version: 1,
		ConfigSchema: schemaCopy, DefaultConfig: defaultCopyConfig,
		Validate: func(raw json.RawMessage) error { _, err := DecodeCopy(raw); return err },
	})
	r.add(Type{
		Key: TypeOutputWebhook, Name: "Webhook notification",
		Description: "Send a signed HTTP notification.",
		Stages:      []model.Stage{model.StageOutput}, Version: 1,
		ConfigSchema: schemaWebhook, DefaultConfig: defaultWebhookConfig,
		Validate: func(raw json.RawMessage) error { _, err := DecodeWebhook(raw); return err },
	})
	r.add(Type{
		Key: TypeOutputEmail, Name: "Email notification",
		Description: "Send an email via SMTP.",
		Stages:      []model.Stage{model.StageOutput}, Version: 1,
		ConfigSchema: schemaEmail, DefaultConfig: defaultEmailConfig,
		Validate: func(raw json.RawMessage) error { _, err := DecodeEmail(raw); return err },
	})
}

func DecodeTrigger(raw json.RawMessage) (TriggerConfig, error) {
	var c TriggerConfig
	if err := decodeStrict(raw, &c); err != nil {
		return c, err
	}
	if c.PathTarget == "" {
		c.PathTarget = PathTargetBase
	}
	if c.MIMEPatterns == nil {
		c.MIMEPatterns = []string{}
	}
	if err := validateTrigger(&c); err != nil {
		return c, err
	}
	return c, nil
}

func DecodeRename(raw json.RawMessage) (RenameConfig, error) {
	var c RenameConfig
	if err := decodeStrict(raw, &c); err != nil {
		return c, err
	}
	if c.Mode == "" {
		c.Mode = "template"
	}
	if c.Collision == "" {
		c.Collision = CollisionFail
	}
	if err := validateRename(&c); err != nil {
		return c, err
	}
	return c, nil
}

func DecodeRemove(raw json.RawMessage) (RemoveConfig, error) {
	var c RemoveConfig
	if err := decodeStrict(raw, &c); err != nil {
		return c, err
	}
	if err := validateRemove(&c); err != nil {
		return c, err
	}
	return c, nil
}

func DecodeCopy(raw json.RawMessage) (CopyConfig, error) {
	var c CopyConfig
	if err := decodeStrict(raw, &c); err != nil {
		return c, err
	}
	if c.Collision == "" {
		c.Collision = CollisionSuffix
	}
	if err := validateCopy(&c); err != nil {
		return c, err
	}
	return c, nil
}

func DecodeWebhook(raw json.RawMessage) (WebhookConfig, error) {
	var c WebhookConfig
	if err := decodeStrict(raw, &c); err != nil {
		return c, err
	}
	if c.Headers == nil {
		c.Headers = map[string]string{}
	}
	if err := validateWebhook(&c); err != nil {
		return c, err
	}
	return c, nil
}

func DecodeEmail(raw json.RawMessage) (EmailConfig, error) {
	var c EmailConfig
	if err := decodeStrict(raw, &c); err != nil {
		return c, err
	}
	if err := validateEmail(&c); err != nil {
		return c, err
	}
	return c, nil
}

func decodeStrict(raw json.RawMessage, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func validateTrigger(c *TriggerConfig) error {
	switch c.PathTarget {
	case PathTargetBase, PathTargetFull:
	default:
		return fmt.Errorf("path_target must be basename or full_path")
	}
	if len(c.PathRegex) > 512 {
		return fmt.Errorf("path_regex is too long")
	}
	if c.PathRegex != "" {
		if _, err := regexp.Compile(c.PathRegex); err != nil {
			return fmt.Errorf("path_regex: %w", err)
		}
	}
	for i, p := range c.MIMEPatterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if !validMIMEPattern(p) {
			return fmt.Errorf("mime_patterns[%d]: invalid %q", i, p)
		}
		c.MIMEPatterns[i] = p
	}
	if len(c.MIMERegex) > 512 {
		return fmt.Errorf("mime_regex is too long")
	}
	if c.MIMERegex != "" {
		if _, err := regexp.Compile(c.MIMERegex); err != nil {
			return fmt.Errorf("mime_regex: %w", err)
		}
	}
	if len(c.ScopeUsername) > 64 {
		return fmt.Errorf("scope_username is too long")
	}
	return nil
}

func validateRename(c *RenameConfig) error {
	switch c.Mode {
	case "template", "regex":
	default:
		return fmt.Errorf("mode must be template or regex")
	}
	if !validCollision(c.Collision) {
		return fmt.Errorf("collision must be fail, suffix, or overwrite")
	}
	if c.Mode == "template" {
		if err := requireTemplate("template", c.Template); err != nil {
			return err
		}
		return validatePathTemplate(c.Template)
	}
	if c.Pattern == "" {
		return fmt.Errorf("pattern is required in regex mode")
	}
	if len(c.Pattern) > 512 {
		return fmt.Errorf("pattern is too long")
	}
	if _, err := regexp.Compile(c.Pattern); err != nil {
		return fmt.Errorf("pattern: %w", err)
	}
	if len(c.Replacement) > 512 {
		return fmt.Errorf("replacement is too long")
	}
	return nil
}

func validateRemove(c *RemoveConfig) error {
	if c.PathTemplate == "" {
		return nil
	}
	if err := requireTemplate("path_template", c.PathTemplate); err != nil {
		return err
	}
	return validatePathTemplate(c.PathTemplate)
}

func validateCopy(c *CopyConfig) error {
	if !validCollision(c.Collision) {
		return fmt.Errorf("collision must be fail, suffix, or overwrite")
	}
	if err := requireTemplate("destination_path", c.DestinationPath); err != nil {
		return err
	}
	if err := validatePathTemplate(c.DestinationPath); err != nil {
		return err
	}
	if len(c.DestinationUsername) > 64 {
		return fmt.Errorf("destination_username is too long")
	}
	return nil
}

func validateWebhook(c *WebhookConfig) error {
	c.Method = strings.ToUpper(strings.TrimSpace(c.Method))
	if c.Method == "" {
		c.Method = http.MethodPost
	}
	switch c.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodGet:
	default:
		return fmt.Errorf("method must be POST, PUT, PATCH, DELETE, or GET")
	}
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("url must be an http(s) URL")
	}
	if c.SecretEnv != "" && !validEnvName(c.SecretEnv) {
		return fmt.Errorf("secret_env must be an environment variable name")
	}
	if c.BodyTemplate != "" {
		if _, err := template.New("body").Option("missingkey=error").Parse(c.BodyTemplate); err != nil {
			return fmt.Errorf("body_template: %w", err)
		}
	}
	for k := range c.Headers {
		if len(k) == 0 || len(k) > 128 {
			return fmt.Errorf("header name is invalid")
		}
	}
	return nil
}

func validateEmail(c *EmailConfig) error {
	if len(c.To) == 0 {
		return fmt.Errorf("to requires at least one recipient")
	}
	for _, addr := range c.To {
		if _, err := mail.ParseAddress(addr); err != nil {
			return fmt.Errorf("to: %w", err)
		}
	}
	if c.SubjectTemplate != "" {
		if _, err := template.New("subject").Option("missingkey=error").Parse(c.SubjectTemplate); err != nil {
			return fmt.Errorf("subject_template: %w", err)
		}
	}
	if c.BodyTemplate != "" {
		if _, err := template.New("body").Option("missingkey=error").Parse(c.BodyTemplate); err != nil {
			return fmt.Errorf("body_template: %w", err)
		}
	}
	return nil
}

func validateCondition(c *ConditionConfig) error {
	switch c.PathTarget {
	case PathTargetBase, PathTargetFull:
	default:
		return fmt.Errorf("path_target must be basename or full_path")
	}
	if len(c.PathRegex) > 512 {
		return fmt.Errorf("path_regex is too long")
	}
	if c.PathRegex != "" {
		if _, err := regexp.Compile(c.PathRegex); err != nil {
			return fmt.Errorf("path_regex: %w", err)
		}
	}
	for i, p := range c.MIMEPatterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if !validMIMEPattern(p) {
			return fmt.Errorf("mime_patterns[%d]: invalid %q", i, p)
		}
		c.MIMEPatterns[i] = p
	}
	if len(c.MIMERegex) > 512 {
		return fmt.Errorf("mime_regex is too long")
	}
	if c.MIMERegex != "" {
		if _, err := regexp.Compile(c.MIMERegex); err != nil {
			return fmt.Errorf("mime_regex: %w", err)
		}
	}
	return nil
}

func requireTemplate(name, s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("%s is required", name)
	}
	if _, err := template.New(name).Option("missingkey=error").Parse(s); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func validatePathTemplate(s string) error {
	if strings.HasPrefix(s, "/") {
		return fmt.Errorf("path must be root-relative")
	}
	for _, seg := range strings.Split(s, "/") {
		if seg == ".." {
			return fmt.Errorf("path must not contain ..")
		}
	}
	return nil
}

func validCollision(s string) bool {
	return s == CollisionFail || s == CollisionSuffix || s == CollisionOverwrite
}

func validEnvName(s string) bool {
	return regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(s)
}

func validMIMEPattern(p string) bool {
	parts := strings.Split(p, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	for _, part := range parts {
		if part == "*" {
			continue
		}
		if !regexp.MustCompile(`^[a-z0-9][a-z0-9!#$&^_.+-]*$`).MatchString(part) {
			return false
		}
	}
	return true
}

// NormalizeMediaType strips parameters and lowercases a MIME type.
func NormalizeMediaType(s string) string {
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
