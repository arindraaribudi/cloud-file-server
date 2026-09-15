package cos

import (
	"context"
	"errors"
	"sync"
	"net/url"
	"time"

	sts "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sts/v20180813"
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

	mu    sync.Mutex
	curr  creds
	src   string
	exp   time.Time
}

func newChain(sts stsClient, st stsClient, usePod bool, refreshRatio float64) *Chain {
	return &Chain{sts: sts, static: st, usePodIdentity: usePod, refreshRatio: refreshRatio}
}

func NewChainFromEnv(ctx context.Context, usePodIdentity bool, id, key, token string, refreshRatio float64) (*Chain, error) {
	if !usePodIdentity && id == "" {
		return nil, errors.New("no credential source configured")
	}
	c := &Chain{usePodIdentity: usePodIdentity, refreshRatio: refreshRatio, static: &envStatic{id: id, key: key, tok: token}}
	if usePodIdentity {
		real, err := newOIDCSTS(ctx)
		if err != nil {
			c.sts = &noopSTS{err: err}
		} else {
			c.sts = real
		}
	}
	return c, nil
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

func newOIDCSTS(ctx context.Context) (stsClient, error) {
	return nil, errors.New("oidc sts not wired in stub")
}

var _ = sts.NewClient
