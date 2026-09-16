package cos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tencentyun/cos-go-sdk-v5"
)

type stsClient interface {
	Get(ctx context.Context) (creds, error)
}
type creds struct {
	ID, Key, Token string
	Expiry         time.Time
}

// Creds is the exported alias used by external callers (e.g. cmd/ftp-server).
type Creds = creds

type Chain struct {
	sts            stsClient
	static         stsClient
	usePodIdentity bool
	refreshRatio   float64

	mu   sync.Mutex
	curr creds
	src  string
	exp  time.Time
}

func newChain(sts stsClient, st stsClient, usePod bool, refreshRatio float64) *Chain {
	return &Chain{sts: sts, static: st, usePodIdentity: usePod, refreshRatio: refreshRatio}
}

// NewChainFromEnv builds a credential chain from env vars. Pod identity is
// attempted when COS_USE_POD_IDENTITY=true AND TKE pod-identity env vars are
// set (TKE_ROLE_ARN + TKE_WEB_IDENTITY_TOKEN_FILE). The static AK/SK from
// COS_STATIC_SECRET_ID/COS_STATIC_SECRET_KEY is always configured as the
// fallback so STS errors don't break boot. Returns an error only when neither
// source is usable.
func NewChainFromEnv(ctx context.Context, usePodIdentity bool, id, key, token string, refreshRatio float64) (*Chain, error) {
	c := &Chain{usePodIdentity: usePodIdentity, refreshRatio: refreshRatio, static: &envStatic{id: id, key: key, tok: token}}
	if usePodIdentity {
		real, err := newOIDCSTS(ctx)
		if err != nil {
			c.sts = &noopSTS{err: err}
		} else {
			c.sts = real
		}
	}
	if !usePodIdentity && (id == "" || key == "") {
		return nil, errors.New("no credential source configured")
	}
	return c, nil
}

// HasStatic reports whether the chain has a usable static (SK/AK) fallback.
func (c *Chain) HasStatic() bool {
	if c.static == nil {
		return false
	}
	_, err := c.static.Get(context.Background())
	return err == nil
}

func (c *Chain) Get(ctx context.Context) (creds, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.curr.ID != "" && time.Until(c.exp) > 0 && time.Until(c.exp) > time.Duration(float64(c.exp.Sub(time.Now().Add(-time.Hour)))*c.refreshRatio) {
		return c.curr, c.src, nil
	}
	if c.usePodIdentity && c.sts != nil {
		got, err := c.sts.Get(ctx)
		if err == nil {
			c.curr = got
			c.src = "STS"
			c.exp = got.Expiry
			return got, "STS", nil
		}
	}
	if c.static != nil {
		got, err := c.static.Get(ctx)
		if err == nil {
			c.curr = got
			c.src = "AKSK"
			c.exp = got.Expiry
			return got, "AKSK", nil
		}
	}
	return creds{}, "", errors.New("cos: no credential source available")
}

func ToCOSURL(bucket, region string, c creds) *cos.BaseURL {
	host := bucket + ".cos." + region + ".myqcloud.com"
	return &cos.BaseURL{BucketURL: &url.URL{Scheme: "https", Host: host}}
}

type envStatic struct{ id, key, tok string }

func (e *envStatic) Get(ctx context.Context) (creds, error) {
	if e.id == "" || e.key == "" {
		return creds{}, errors.New("no static creds configured")
	}
	return creds{ID: e.id, Key: e.key, Token: e.tok, Expiry: time.Now().Add(1 * time.Hour)}, nil
}

type noopSTS struct{ err error }

func (n *noopSTS) Get(ctx context.Context) (creds, error) { return creds{}, n.err }

// HasTKEPodIdentity reports whether the runtime looks like a TKE pod with the
// OIDC web-identity token wired in. Used by callers to decide between STS and
// static credential sources at boot.
func HasTKEPodIdentity() bool {
	return os.Getenv("TKE_ROLE_ARN") != "" && os.Getenv("TKE_WEB_IDENTITY_TOKEN_FILE") != ""
}

// oidcSTS performs AssumeRoleWithWebIdentity against Tencent Cloud STS using
// the OIDC token mounted by TKE pod identity. Uses the public endpoint with
// `Authorization: SKIP` (sigv3) so no static key is required for the STS call.
type oidcSTS struct {
	roleArn, tokenFile, providerID, region string
}

func newOIDCSTS(_ context.Context) (stsClient, error) {
	roleArn := os.Getenv("TKE_ROLE_ARN")
	tokenFile := os.Getenv("TKE_WEB_IDENTITY_TOKEN_FILE")
	if roleArn == "" || tokenFile == "" {
		return nil, errors.New("tke pod identity env not set (need TKE_ROLE_ARN + TKE_WEB_IDENTITY_TOKEN_FILE)")
	}
	return &oidcSTS{
		roleArn:    roleArn,
		tokenFile:  tokenFile,
		providerID: os.Getenv("TKE_PROVIDER_ID"),
		region:     os.Getenv("TKE_REGION"),
	}, nil
}

func (o *oidcSTS) Get(ctx context.Context) (creds, error) {
	tok, err := os.ReadFile(o.tokenFile)
	if err != nil {
		return creds{}, fmt.Errorf("read web-identity token: %w", err)
	}
	body, _ := json.Marshal(map[string]string{
		"RoleArn":          o.roleArn,
		"WebIdentityToken": strings.TrimSpace(string(tok)),
		"RoleSessionName":  "cos-ftp-server",
		"ProviderId":       o.providerID,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://sts.tencentcloudapi.com/", bytes.NewReader(body))
	if err != nil {
		return creds{}, err
	}
	req.Header.Set("Host", "sts.tencentcloudapi.com")
	req.Header.Set("X-TC-Action", "AssumeRoleWithWebIdentity")
	req.Header.Set("X-TC-Version", "2018-08-13")
	req.Header.Set("X-TC-Timestamp", fmt.Sprintf("%d", time.Now().Unix()))
	req.Header.Set("Authorization", "SKIP")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return creds{}, fmt.Errorf("sts request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return creds{}, fmt.Errorf("sts status %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		Response struct {
			Credentials struct {
				Token        string `json:"Token"`
				TmpSecretId  string `json:"TmpSecretId"`
				TmpSecretKey string `json:"TmpSecretKey"`
			} `json:"Credentials"`
			ExpiredTime uint64 `json:"ExpiredTime"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return creds{}, fmt.Errorf("decode sts response: %w", err)
	}
	c := out.Response.Credentials
	if c.TmpSecretId == "" || c.TmpSecretKey == "" {
		return creds{}, errors.New("sts returned empty credentials")
	}
	exp := time.Now().Add(time.Hour)
	if out.Response.ExpiredTime > 0 {
		exp = time.Unix(int64(out.Response.ExpiredTime), 0)
	}
	return creds{ID: c.TmpSecretId, Key: c.TmpSecretKey, Token: c.Token, Expiry: exp}, nil
}
