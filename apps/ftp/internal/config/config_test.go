package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@h:5432/d")
	t.Setenv("FTP_LISTEN", "")
	t.Setenv("FTP_PASSIVE_PORT_RANGE", "")
	t.Setenv("FTP_IDLE_TIMEOUT", "")
	t.Setenv("STS_REFRESH_RATIO", "")
	t.Setenv("AUDIT_RETENTION_DAYS", "")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.FTPListen != ":2121" {
		t.Errorf("FTPListen=%q", c.FTPListen)
	}
	if c.PassivePortRange.Start != 50000 || c.PassivePortRange.End != 50999 {
		t.Errorf("passive range=%v", c.PassivePortRange)
	}
	if c.IdleTimeout != 300*time.Second {
		t.Errorf("IdleTimeout=%v", c.IdleTimeout)
	}
	if c.AuditRetentionDays != 365 {
		t.Errorf("AuditRetentionDays=%d", c.AuditRetentionDays)
	}
	if c.STSRefreshRatio != 0.8 {
		t.Errorf("STSRefreshRatio=%v", c.STSRefreshRatio)
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when DATABASE_URL empty")
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@h:5432/d")
	t.Setenv("FTP_LISTEN", ":2121")
	t.Setenv("FTP_PASSIVE_PORT_RANGE", "60000-60099")
	t.Setenv("FTP_IDLE_TIMEOUT", "600s")
	t.Setenv("STS_REFRESH_RATIO", "0.5")
	t.Setenv("AUDIT_RETENTION_DAYS", "180")
	t.Setenv("ADMIN_LISTEN", ":8080")
	t.Setenv("LOG_LEVEL", "info")
	t.Setenv("LOG_FORMAT", "json")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.PassivePortRange.Start != 60000 || c.PassivePortRange.End != 60099 {
		t.Errorf("PassivePortRange=%+v", c.PassivePortRange)
	}
	if c.IdleTimeout != 600*time.Second {
		t.Errorf("IdleTimeout=%v", c.IdleTimeout)
	}
	if c.STSRefreshRatio != 0.5 {
		t.Errorf("STSRefreshRatio=%v", c.STSRefreshRatio)
	}
	if c.AuditRetentionDays != 180 {
		t.Errorf("AuditRetentionDays=%d", c.AuditRetentionDays)
	}
}

func TestAdminCookieSecure(t *testing.T) {
	cases := []struct {
		env, url, want string
	}{
		{"", "http://localhost:9001", "false"},
		{"", "https://app.example.com", "true"},
		{"true", "http://localhost:9001", "true"},
		{"false", "https://app.example.com", "false"},
	}
	for _, tc := range cases {
		got := adminCookieSecure(tc.env, tc.url)
		wantBool := tc.want == "true"
		if got != wantBool {
			t.Errorf("adminCookieSecure(%q,%q)=%v, want %v", tc.env, tc.url, got, wantBool)
		}
	}
}

func TestLoadInvalidPassiveRange(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@h:5432/d")
	cases := []struct{ name, val string }{
		{"bad-format", "abc"},
		{"end-le-start", "50000-50000"},
		{"end-toobig", "50000-70000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FTP_PASSIVE_PORT_RANGE", tc.val)
			if _, err := Load(); err == nil {
				t.Fatalf("expected error for %q", tc.val)
			}
		})
	}
}