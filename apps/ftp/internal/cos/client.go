package cos

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tencentyun/cos-go-sdk-v5"
)

type Client struct {
	Bucket string
	Region string
	Chain  *Chain

	http   *http.Client
	cos    *cos.Client
	curr   creds
	mu     sync.Mutex
}

// NewClient builds a COS client backed by a static credential snapshot. Used by
// tests and by callers that don't need rotation. For STS-backed or rotating
// credentials use NewClientWithChain.
func NewClient(bucket, region string, c creds, fake interface{}) *Client {
	cli := &Client{Bucket: bucket, Region: region}
	if fake != nil {
		return cli
	}
	cli.setHTTP(c)
	return cli
}

// NewClientWithChain builds a COS client whose credentials are resolved lazily
// from the supplied chain on every operation, so STS-issued sessions get
// rotated transparently before expiry.
func NewClientWithChain(bucket, region string, chain *Chain) *Client {
	cli := &Client{Bucket: bucket, Region: region, Chain: chain}
	// Seed with static if available so the first call before chain resolves
	// still has something usable.
	if chain != nil && chain.HasStatic() {
		if got, _, err := chain.Get(context.Background()); err == nil {
			cli.setHTTP(got)
		}
	}
	return cli
}

// refresh re-seeds the http client when the chain yields a different
// credential snapshot. Returns an error if the chain is configured but cannot
// resolve any credential source.
func (c *Client) refresh(ctx context.Context) error {
	if c.Chain == nil {
		return nil
	}
	got, _, err := c.Chain.Get(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if got.ID != c.curr.ID || got.Key != c.curr.Key || got.Token != c.curr.Token {
		c.setHTTP(got)
	}
	c.mu.Unlock()
	return nil
}

func (c *Client) setHTTP(creds creds) {
	c.curr = creds
	c.http = &http.Client{
		Transport: &cos.AuthorizationTransport{
			SecretID:     creds.ID,
			SecretKey:    creds.Key,
			SessionToken: creds.Token,
		},
		Timeout: 60 * time.Second,
	}
	u := &url.URL{Scheme: "https", Host: c.Bucket + ".cos." + c.Region + ".myqcloud.com"}
	c.cos = cos.NewClient(&cos.BaseURL{BucketURL: u}, c.http)
}

// do runs op. If op returns an auth-class error (bad signature, expired token,
// unknown access key), the chain cache is invalidated and op is retried once
// with fresh credentials. Any other error, or a failed retry, is returned as-is.
func (c *Client) do(ctx context.Context, op func() error) error {
	err := op()
	if err == nil || !isAuthErr(err) || c.Chain == nil {
		return err
	}
	c.Chain.Invalidate()
	if rerr := c.refresh(ctx); rerr != nil {
		return err // keep original auth error; chain itself is broken
	}
	return op()
}

// isAuthErr matches the credential-error codes COS returns when the request
// was rejected for authentication reasons. Substring match on Error() — the
// COS SDK wraps server responses without exposing a typed sentinel.
func isAuthErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	for _, sub := range []string{
		"SignatureDoesNotMatch",
		"ExpiredToken",
		"TokenExpired",
		"InvalidAccessKeyId",
		"InvalidToken",
	} {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
