package cos

import (
	"net/http"
	"net/url"
	"time"

	"github.com/tencentyun/cos-go-sdk-v5"
)

type Client struct {
	Bucket string
	Region string
	Chain  *Chain

	http *http.Client
	cos  *cos.Client
}

func NewClient(bucket, region string, c creds, fake interface{}) *Client {
	cli := &Client{Bucket: bucket, Region: region}
	if fake != nil {
		return cli
	}
	cli.http = &http.Client{
		Transport: &cos.AuthorizationTransport{
			SecretID:     c.ID,
			SecretKey:    c.Key,
			SessionToken: c.Token,
		},
		Timeout: 60 * time.Second,
	}
	u := &url.URL{Scheme: "https", Host: bucket + ".cos." + region + ".myqcloud.com"}
	cli.cos = cos.NewClient(&cos.BaseURL{BucketURL: u}, cli.http)
	return cli
}

func (c *Client) Refresh(creds creds) {
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