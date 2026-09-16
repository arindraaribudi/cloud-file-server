package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type PortRange struct{ Start, End int }

type Config struct {
	DatabaseURL         string
	FTPListen           string
	FTPPublicIP         string
	FTPLocalRoot        string
	PassivePortRange    PortRange
	FTPTLSCert          string
	FTPTLSKey           string
	IdleTimeout         time.Duration
	FTPAllowPlain       bool
	FTPDefaultAllowActive bool
	FTPDefaultRefuseOverwrite bool
	COSStaticSecretID      string
	COSStaticSecretKey     string
	COSStaticSessionToken  string
	COSBucket              string
	COSRegion              string
	STSRefreshRatio     float64
	AuditRetentionDays  int
	AuthLockoutLimit    int
	AuthLockoutWindow   time.Duration
	AdminListen         string
	AdminCookieSecure   bool
	SeedEnabled         bool
	LogLevel            string
	LogFormat           string

	FTPDefaultRootPrefix string

	PublicURL string

	OIDCIssuerURL           string
	OIDCClientID            string
	OIDCClientSecret        string
	OIDCAdminGroup          string
	OIDCReadonlyGroup       string
	OIDCFrontendRedirectURL string
}

func Load() (*Config, error) {
	c := &Config{
		DatabaseURL:                os.Getenv("DATABASE_URL"),
		FTPListen:                  getenv("FTP_LISTEN", ":2121"),
		FTPPublicIP:                os.Getenv("FTP_PUBLIC_IP"),
		FTPLocalRoot:               getenv("FTP_LOCAL_ROOT", "/tmp/ftp-m1"),
		FTPTLSCert:                 os.Getenv("FTP_TLS_CERT"),
		FTPTLSKey:                  os.Getenv("FTP_TLS_KEY"),
		IdleTimeout:                300 * time.Second,
		FTPAllowPlain:              getenv("FTP_ALLOW_PLAIN", "false") == "true",
		FTPDefaultAllowActive:      getenv("FTP_DEFAULT_ALLOW_ACTIVE", "false") == "true",
		FTPDefaultRefuseOverwrite:  getenv("FTP_DEFAULT_REFUSE_OVERWRITE", "false") == "true",
		COSStaticSecretID:          os.Getenv("COS_STATIC_SECRET_ID"),
		COSStaticSecretKey:         os.Getenv("COS_STATIC_SECRET_KEY"),
		COSStaticSessionToken:      os.Getenv("COS_STATIC_SESSION_TOKEN"),
		COSBucket:                  getenv("COS_BUCKET", "test-1409486316"),
		COSRegion:                  getenv("COS_REGION", "ap-bangkok"),
		STSRefreshRatio:            0.8,
		AuditRetentionDays:         365,
		AuthLockoutLimit:           5,
		AuthLockoutWindow:          15 * time.Minute,
		AdminListen:                getenv("ADMIN_LISTEN", ":8080"),
		AdminCookieSecure:          adminCookieSecure(getenv("ADMIN_COOKIE_SECURE", ""), getenv("PUBLIC_URL", "http://localhost:9001")),
		SeedEnabled:                getenv("FTP_SEED", "false") == "true",
		LogLevel:                   getenv("LOG_LEVEL", "info"),
		LogFormat:                  getenv("LOG_FORMAT", "json"),

		PublicURL: getenv("PUBLIC_URL", "http://localhost:9001"),

		OIDCIssuerURL:           os.Getenv("OIDC_ISSUER_URL"),
		OIDCClientID:            os.Getenv("OIDC_CLIENT_ID"),
		OIDCClientSecret:        os.Getenv("OIDC_CLIENT_SECRET"),
		OIDCAdminGroup:          getenv("OIDC_ADMIN_GROUP", "admin"),
		OIDCReadonlyGroup:       getenv("OIDC_READONLY_GROUP", "readonly"),
		OIDCFrontendRedirectURL: getenv("OIDC_FRONTEND_REDIRECT_URL", "/auth/callback"),

		FTPDefaultRootPrefix: getenv("FTP_DEFAULT_ROOT_PREFIX", "/t/t/"),
	}
	if v := os.Getenv("FTP_PASSIVE_PORT_RANGE"); v != "" {
		var s, e int
		if _, err := fmt.Sscanf(v, "%d-%d", &s, &e); err != nil {
			return nil, fmt.Errorf("FTP_PASSIVE_PORT_RANGE: %w", err)
		}
		if s < 1 || e <= s || e > 65535 {
			return nil, errors.New("FTP_PASSIVE_PORT_RANGE invalid")
		}
		c.PassivePortRange = PortRange{s, e}
	} else {
		c.PassivePortRange = PortRange{50000, 50999}
	}
	if v := os.Getenv("FTP_IDLE_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("FTP_IDLE_TIMEOUT: %w", err)
		}
		c.IdleTimeout = d
	}
	if v := os.Getenv("STS_REFRESH_RATIO"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, fmt.Errorf("STS_REFRESH_RATIO: %w", err)
		}
		c.STSRefreshRatio = f
	}
	if v := os.Getenv("AUDIT_RETENTION_DAYS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("AUDIT_RETENTION_DAYS: %w", err)
		}
		c.AuditRetentionDays = n
	}
	if c.DatabaseURL == "" {
		return nil, errors.New("DATABASE_URL required")
	}
	return c, nil
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// adminCookieSecure picks the default value based on the PUBLIC_URL scheme.
// Explicit ADMIN_COOKIE_SECURE wins. When unset and the public URL is http://,
// cookies can't carry the Secure flag, so we fall back to false (otherwise the
// browser silently drops session/state cookies on every redirect).
func adminCookieSecure(envVal, publicURL string) bool {
	if envVal != "" {
		return envVal == "true"
	}
	return strings.HasPrefix(strings.ToLower(publicURL), "https://")
}