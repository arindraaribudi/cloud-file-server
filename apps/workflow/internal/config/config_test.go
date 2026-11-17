package config

import (
	"testing"
	"time"
)

func clearWorkflowEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"WORKFLOW_LISTEN", "WORKFLOW_FILE_API_URL", "WORKFLOW_WORKERS",
		"WORKFLOW_POLL_INTERVAL", "WORKFLOW_MAX_ATTEMPTS", "WORKFLOW_COMPONENT_TIMEOUT",
		"WORKFLOW_RUN_TIMEOUT", "WORKFLOW_RUN_RETENTION_DAYS", "WORKFLOW_SMTP_HOST",
		"WORKFLOW_SMTP_PORT", "WORKFLOW_SMTP_USER", "WORKFLOW_SMTP_PASS",
		"WORKFLOW_SMTP_FROM", "WORKFLOW_SMTP_TLS", "WORKFLOW_WEBHOOK_ALLOW_HTTP",
		"WORKFLOW_WEBHOOK_ALLOW_PRIVATE",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearWorkflowEnv(t)
	t.Setenv("DATABASE_URL", "postgres://u:p@h:5432/d")
	t.Setenv("WORKFLOW_SERVICE_TOKEN", "tok")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":8081" || c.FileAPIURL != "http://ftp-server:7000" {
		t.Errorf("listen/file api=%q/%q", c.Listen, c.FileAPIURL)
	}
	if c.Workers != 4 || c.MaxAttempts != 3 || c.RetentionDays != 90 {
		t.Errorf("numbers=%d/%d/%d", c.Workers, c.MaxAttempts, c.RetentionDays)
	}
	if c.PollInterval != 500*time.Millisecond || c.ComponentTimeout != 60*time.Second || c.RunTimeout != 10*time.Minute {
		t.Errorf("durations=%v/%v/%v", c.PollInterval, c.ComponentTimeout, c.RunTimeout)
	}
	if c.SMTPPort != "587" || c.SMTPTLS != "starttls" {
		t.Errorf("smtp=%q/%q", c.SMTPPort, c.SMTPTLS)
	}
	if c.WebhookAllowHTTP || c.WebhookAllowPrivate {
		t.Errorf("webhook flags should default false")
	}
}

func TestLoadRequiresDatabaseAndToken(t *testing.T) {
	clearWorkflowEnv(t)
	t.Setenv("DATABASE_URL", "")
	t.Setenv("WORKFLOW_SERVICE_TOKEN", "tok")
	if _, err := Load(); err == nil {
		t.Fatal("expected DATABASE_URL error")
	}
	t.Setenv("DATABASE_URL", "postgres://u:p@h:5432/d")
	t.Setenv("WORKFLOW_SERVICE_TOKEN", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected service token error")
	}
}

func TestLoadOverrides(t *testing.T) {
	clearWorkflowEnv(t)
	t.Setenv("DATABASE_URL", "postgres://u:p@h:5432/d")
	t.Setenv("WORKFLOW_SERVICE_TOKEN", "tok")
	t.Setenv("WORKFLOW_LISTEN", ":9999")
	t.Setenv("WORKFLOW_FILE_API_URL", "http://files:7000")
	t.Setenv("WORKFLOW_WORKERS", "8")
	t.Setenv("WORKFLOW_POLL_INTERVAL", "2s")
	t.Setenv("WORKFLOW_MAX_ATTEMPTS", "5")
	t.Setenv("WORKFLOW_COMPONENT_TIMEOUT", "30s")
	t.Setenv("WORKFLOW_RUN_TIMEOUT", "20m")
	t.Setenv("WORKFLOW_RUN_RETENTION_DAYS", "7")
	t.Setenv("WORKFLOW_SMTP_HOST", "smtp.example.com")
	t.Setenv("WORKFLOW_SMTP_PORT", "25")
	t.Setenv("WORKFLOW_SMTP_TLS", "none")
	t.Setenv("WORKFLOW_WEBHOOK_ALLOW_HTTP", "true")
	t.Setenv("WORKFLOW_WEBHOOK_ALLOW_PRIVATE", "true")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":9999" || c.FileAPIURL != "http://files:7000" {
		t.Errorf("listen/file api=%q/%q", c.Listen, c.FileAPIURL)
	}
	if c.Workers != 8 || c.MaxAttempts != 5 || c.RetentionDays != 7 {
		t.Errorf("numbers=%d/%d/%d", c.Workers, c.MaxAttempts, c.RetentionDays)
	}
	if c.PollInterval != 2*time.Second || c.ComponentTimeout != 30*time.Second || c.RunTimeout != 20*time.Minute {
		t.Errorf("durations=%v/%v/%v", c.PollInterval, c.ComponentTimeout, c.RunTimeout)
	}
	if c.SMTPHost != "smtp.example.com" || c.SMTPPort != "25" || c.SMTPTLS != "none" {
		t.Errorf("smtp=%q/%q/%q", c.SMTPHost, c.SMTPPort, c.SMTPTLS)
	}
	if !c.WebhookAllowHTTP || !c.WebhookAllowPrivate {
		t.Errorf("webhook flags should be true")
	}
}

func TestLoadRejectsInvalidSettings(t *testing.T) {
	cases := []struct{ key, value string }{
		{"WORKFLOW_WORKERS", "0"},
		{"WORKFLOW_POLL_INTERVAL", "nope"},
		{"WORKFLOW_MAX_ATTEMPTS", "-1"},
		{"WORKFLOW_COMPONENT_TIMEOUT", "0s"},
		{"WORKFLOW_RUN_TIMEOUT", "nope"},
		{"WORKFLOW_RUN_RETENTION_DAYS", "0"},
		{"WORKFLOW_SMTP_TLS", "ssl"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			clearWorkflowEnv(t)
			t.Setenv("DATABASE_URL", "postgres://u:p@h:5432/d")
			t.Setenv("WORKFLOW_SERVICE_TOKEN", "tok")
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); err == nil {
				t.Fatalf("expected error for %s=%q", tc.key, tc.value)
			}
		})
	}
}
