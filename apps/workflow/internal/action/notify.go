package action

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/example/cos-workflow/internal/components"
)

type NotifierConfig struct {
	AllowHTTP    bool
	AllowPrivate bool
	SMTPHost     string
	SMTPPort     string
	SMTPUser     string
	SMTPPass     string
	SMTPFrom     string
	SMTPTLS      string
}

type Notifier struct {
	HTTP         *http.Client
	AllowHTTP    bool
	AllowPrivate bool
	SMTPHost     string
	SMTPPort     string
	SMTPUser     string
	SMTPPass     string
	SMTPFrom     string
	SMTPTLS      string
}

func NewNotifier(cfg NotifierConfig) *Notifier {
	port := cfg.SMTPPort
	if port == "" {
		port = "587"
	}
	tlsMode := cfg.SMTPTLS
	if tlsMode == "" {
		tlsMode = "starttls"
	}
	return &Notifier{
		HTTP:         &http.Client{Timeout: 20 * time.Second},
		AllowHTTP:    cfg.AllowHTTP,
		AllowPrivate: cfg.AllowPrivate,
		SMTPHost:     cfg.SMTPHost,
		SMTPPort:     port,
		SMTPUser:     cfg.SMTPUser,
		SMTPPass:     cfg.SMTPPass,
		SMTPFrom:     cfg.SMTPFrom,
		SMTPTLS:      tlsMode,
	}
}

func (n *Notifier) SendWebhook(ctx context.Context, req *Request, cfg components.WebhookConfig) (map[string]any, error) {
	if err := n.validateTarget(ctx, cfg.URL); err != nil {
		return nil, err
	}
	body, contentType, err := n.webhookBody(req, cfg)
	if err != nil {
		return nil, err
	}

	method := cfg.Method
	if method == "" {
		method = http.MethodPost
	}
	hasBody := method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch
	if !hasBody {
		body = nil
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range cfg.Headers {
		httpReq.Header.Set(k, v)
	}
	if hasBody {
		httpReq.Header.Set("Content-Type", contentType)
	}
	httpReq.Header.Set("X-Workflow-Id", req.Context.WorkflowID.String())
	httpReq.Header.Set("X-Workflow-Run-Id", req.Context.RunID.String())
	if componentID(req) != "" {
		httpReq.Header.Set("X-Workflow-Component-Id", componentID(req))
	}
	httpReq.Header.Set("Idempotency-Key", req.Context.RunID.String()+":"+componentID(req))
	if cfg.SecretEnv != "" {
		secret := os.Getenv(cfg.SecretEnv)
		if secret == "" {
			return nil, fmt.Errorf("webhook secret env %s is not set", cfg.SecretEnv)
		}
		ts := time.Now().UTC().Format(time.RFC3339)
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(ts + "." + string(body)))
		httpReq.Header.Set("X-Workflow-Timestamp", ts)
		httpReq.Header.Set("X-Workflow-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	client := n.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, retryable(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &HTTPError{Status: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	return map[string]any{"channel": "webhook", "method": method, "url": cfg.URL, "status": resp.StatusCode}, nil
}

// webhookBody renders the request body exactly as SendWebhook sends it.
func (n *Notifier) webhookBody(req *Request, cfg components.WebhookConfig) ([]byte, string, error) {
	payloadJSON, err := json.Marshal(n.payload(req))
	if err != nil {
		return nil, "", err
	}
	data := templateData(req.Context)
	data["WorkflowName"] = workflowName(req)
	data["RunID"] = req.Context.RunID.String()
	data["Payload"] = string(payloadJSON)

	body := payloadJSON
	contentType := "application/json"
	if cfg.BodyTemplate != "" {
		rendered, err := renderTemplate("body", cfg.BodyTemplate, data)
		if err != nil {
			return nil, "", err
		}
		body = []byte(rendered)
		if !json.Valid(body) {
			contentType = "text/plain; charset=utf-8"
		}
	}
	return body, contentType, nil
}

// PreviewWebhook describes the request SendWebhook would make, without sending
// it. The target is still validated, so a blocked or invalid URL shows up here.
func (n *Notifier) PreviewWebhook(ctx context.Context, req *Request, cfg components.WebhookConfig) (map[string]any, error) {
	if err := n.validateTarget(ctx, cfg.URL); err != nil {
		return nil, err
	}
	body, contentType, err := n.webhookBody(req, cfg)
	if err != nil {
		return nil, err
	}
	method := cfg.Method
	if method == "" {
		method = http.MethodPost
	}
	hasBody := method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch
	out := map[string]any{
		"channel": "webhook", "method": method, "url": cfg.URL,
		"signed": cfg.SecretEnv != "", "dry_run": true,
	}
	if hasBody {
		out["content_type"] = contentType
		out["body"] = truncate(string(body), 4000)
	}
	if len(cfg.Headers) > 0 {
		names := make([]string, 0, len(cfg.Headers))
		for k := range cfg.Headers {
			names = append(names, k)
		}
		sort.Strings(names)
		out["extra_headers"] = names
	}
	return out, nil
}

// PreviewEmail renders the email SendEmail would send, without sending it.
func (n *Notifier) PreviewEmail(req *Request, cfg components.EmailConfig) (map[string]any, error) {
	if n.SMTPHost == "" || n.SMTPFrom == "" {
		return nil, fmt.Errorf("SMTP is not configured")
	}
	subject, body, err := n.emailContent(req, cfg)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"channel": "email", "to": cfg.To, "subject": subject,
		"body": truncate(body, 2000), "dry_run": true,
	}, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func (n *Notifier) SendEmail(ctx context.Context, req *Request, cfg components.EmailConfig) (map[string]any, error) {
	if n.SMTPHost == "" || n.SMTPFrom == "" {
		return nil, fmt.Errorf("SMTP is not configured")
	}
	subject, body, err := n.emailContent(req, cfg)
	if err != nil {
		return nil, err
	}
	if err := n.sendSMTP(ctx, cfg.To, subject, body); err != nil {
		return nil, err
	}
	return map[string]any{"channel": "email", "to": cfg.To, "subject": subject}, nil
}

func (n *Notifier) emailContent(req *Request, cfg components.EmailConfig) (string, string, error) {
	data := templateData(req.Context)
	data["WorkflowName"] = workflowName(req)
	data["RunID"] = req.Context.RunID.String()
	subject := "Workflow notification"
	if cfg.SubjectTemplate != "" {
		rendered, err := renderTemplate("subject", cfg.SubjectTemplate, data)
		if err != nil {
			return "", "", err
		}
		subject = rendered
	}
	body := ""
	if cfg.BodyTemplate != "" {
		rendered, err := renderTemplate("body", cfg.BodyTemplate, data)
		if err != nil {
			return "", "", err
		}
		body = rendered
	} else {
		payload, _ := json.MarshalIndent(n.payload(req), "", "  ")
		body = string(payload)
	}
	return subject, body, nil
}

func (n *Notifier) payload(req *Request) map[string]any {
	file := map[string]any{
		"original_path": req.Context.OriginalPath,
		"current_path":  req.Context.Path,
		"mime":          req.Context.MIME,
		"size":          req.Context.Size,
	}
	run := map[string]any{"id": req.Context.RunID.String(), "status": "running"}
	if req.Run != nil {
		run["source"] = req.Run.Source
		run["status"] = req.Run.Status
	}
	comp := map[string]any{"type": req.Component.TypeKey, "label": req.Component.Label}
	return map[string]any{
		"workflow":  map[string]any{"id": req.Context.WorkflowID.String(), "name": workflowName(req)},
		"run":       run,
		"event":     eventValue(req.Run),
		"file":      file,
		"component": comp,
	}
}

func (n *Notifier) validateTarget(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("invalid webhook URL")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("webhook URL must use http or https")
	}
	if u.Scheme == "http" && !n.AllowHTTP {
		return fmt.Errorf("plain HTTP webhook URLs are disabled")
	}
	host := u.Hostname()
	ips := []net.IP{}
	if ip := net.ParseIP(host); ip != nil {
		ips = append(ips, ip)
	} else {
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return retryable(err)
		}
		for _, addr := range addrs {
			ips = append(ips, addr.IP)
		}
	}
	if n.AllowPrivate {
		return nil
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("private webhook targets are disabled")
		}
	}
	return nil
}

func (n *Notifier) sendSMTP(ctx context.Context, to []string, subject, body string) error {
	addr := net.JoinHostPort(n.SMTPHost, n.SMTPPort)
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return retryable(err)
	}
	c, err := smtp.NewClient(conn, n.SMTPHost)
	if err != nil {
		_ = conn.Close()
		return retryable(err)
	}
	defer func() { _ = c.Close() }()

	if n.SMTPTLS != "none" {
		if err := c.StartTLS(&tls.Config{ServerName: n.SMTPHost, MinVersion: tls.VersionTLS12}); err != nil {
			return retryable(err)
		}
	}
	if n.SMTPUser != "" {
		if err := c.Auth(smtp.PlainAuth("", n.SMTPUser, n.SMTPPass, n.SMTPHost)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(n.SMTPFrom); err != nil {
		return retryable(err)
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp rcpt %s: %w", rcpt, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return retryable(err)
	}
	msg := "From: " + sanitizeHeader(n.SMTPFrom) + "\r\n" +
		"To: " + sanitizeHeader(strings.Join(to, ", ")) + "\r\n" +
		"Subject: " + sanitizeHeader(subject) + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" + body + "\r\n"
	if _, err := w.Write([]byte(msg)); err != nil {
		_ = w.Close()
		return retryable(err)
	}
	if err := w.Close(); err != nil {
		return retryable(err)
	}
	if err := c.Quit(); err != nil {
		return retryable(err)
	}
	return nil
}

func sanitizeHeader(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

func workflowName(req *Request) string {
	if req.Workflow != nil {
		return req.Workflow.Name
	}
	return ""
}

func componentID(req *Request) string {
	if req.Component != nil {
		return req.Component.ID.String()
	}
	return ""
}
