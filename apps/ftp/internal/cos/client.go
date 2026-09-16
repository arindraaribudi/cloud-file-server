package cos

import (
	"context"
	"net/http"
	"net/url"
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
