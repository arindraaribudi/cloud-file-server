package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	DatabaseURL      string
	Listen           string
	FileAPIURL       string
	ServiceToken     string
	Workers          int
	PollInterval     time.Duration
	MaxAttempts      int
	ComponentTimeout time.Duration
	RunTimeout       time.Duration
	RetentionDays    int

	SMTPHost string
	SMTPPort string
	SMTPUser string
	SMTPPass string
	SMTPFrom string
	SMTPTLS  string

	WebhookAllowHTTP    bool
	WebhookAllowPrivate bool
}

func Load() (*Config, error) {
	c := &Config{
		DatabaseURL:         os.Getenv("DATABASE_URL"),
		Listen:              getenv("WORKFLOW_LISTEN", ":8081"),
		FileAPIURL:          getenv("WORKFLOW_FILE_API_URL", "http://ftp-server:7000"),
		ServiceToken:        os.Getenv("WORKFLOW_SERVICE_TOKEN"),
		Workers:             4,
		PollInterval:        500 * time.Millisecond,
		MaxAttempts:         3,
		ComponentTimeout:    60 * time.Second,
		RunTimeout:          10 * time.Minute,
		RetentionDays:       90,
		SMTPHost:            os.Getenv("WORKFLOW_SMTP_HOST"),
		SMTPPort:            getenv("WORKFLOW_SMTP_PORT", "587"),
		SMTPUser:            os.Getenv("WORKFLOW_SMTP_USER"),
		SMTPPass:            os.Getenv("WORKFLOW_SMTP_PASS"),
		SMTPFrom:            os.Getenv("WORKFLOW_SMTP_FROM"),
		SMTPTLS:             getenv("WORKFLOW_SMTP_TLS", "starttls"),
		WebhookAllowHTTP:    getenv("WORKFLOW_WEBHOOK_ALLOW_HTTP", "false") == "true",
		WebhookAllowPrivate: getenv("WORKFLOW_WEBHOOK_ALLOW_PRIVATE", "false") == "true",
	}
	if c.DatabaseURL == "" {
		return nil, errors.New("DATABASE_URL required")
	}
	if c.ServiceToken == "" {
		return nil, errors.New("WORKFLOW_SERVICE_TOKEN required")
	}
	if c.FileAPIURL == "" {
		return nil, errors.New("WORKFLOW_FILE_API_URL required")
	}

	if v := os.Getenv("WORKFLOW_WORKERS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("WORKFLOW_WORKERS: must be a positive integer")
		}
		c.Workers = n
	}
	if v := os.Getenv("WORKFLOW_POLL_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("WORKFLOW_POLL_INTERVAL: %w", err)
		}
		c.PollInterval = d
	}
	if v := os.Getenv("WORKFLOW_MAX_ATTEMPTS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("WORKFLOW_MAX_ATTEMPTS: must be a positive integer")
		}
		c.MaxAttempts = n
	}
	if v := os.Getenv("WORKFLOW_COMPONENT_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("WORKFLOW_COMPONENT_TIMEOUT: %w", err)
		}
		c.ComponentTimeout = d
	}
	if v := os.Getenv("WORKFLOW_RUN_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("WORKFLOW_RUN_TIMEOUT: %w", err)
		}
		c.RunTimeout = d
	}
	if v := os.Getenv("WORKFLOW_RUN_RETENTION_DAYS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("WORKFLOW_RUN_RETENTION_DAYS: must be a positive integer")
		}
		c.RetentionDays = n
	}
	switch c.SMTPTLS {
	case "starttls", "none":
	default:
		return nil, fmt.Errorf("WORKFLOW_SMTP_TLS: %q must be starttls or none", c.SMTPTLS)
	}
	return c, nil
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
