package cos

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/tencentyun/cos-go-sdk-v5"
)

type Entry struct {
	Name       string
	Size       int64
	IsDir      bool
	ModifyTime time.Time
}

func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC1123, time.RFC1123Z, "2006-01-02T15:04:05.000Z", "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func (c *Client) List(ctx context.Context, prefix string) ([]Entry, error) {
	if err := c.refresh(ctx); err != nil {
		return nil, err
	}
	if !strings.HasSuffix(prefix, "/") && prefix != "" {
		prefix += "/"
	}
	res, _, err := c.cos.Bucket.Get(ctx, &cos.BucketGetOptions{Prefix: prefix, Delimiter: "/"})
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0)
	for _, p := range res.CommonPrefixes {
		out = append(out, Entry{Name: strings.TrimPrefix(p, prefix), IsDir: true})
	}
	for _, o := range res.Contents {
		name := strings.TrimPrefix(o.Key, prefix)
		if name == "" {
			continue
		}
		out = append(out, Entry{Name: name, Size: o.Size, ModifyTime: parseTime(o.LastModified)})
	}
	return out, nil
}

func (c *Client) Head(ctx context.Context, key string) (*Entry, error) {
	if err := c.refresh(ctx); err != nil {
		return nil, err
	}
	resp, err := c.cos.Object.Head(ctx, key, &cos.ObjectHeadOptions{})
	if err != nil {
		return nil, err
	}
	cl, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	return &Entry{Size: cl, ModifyTime: parseTime(resp.Header.Get("Last-Modified"))}, nil
}

func (c *Client) Get(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	if err := c.refresh(ctx); err != nil {
		return nil, err
	}
	opt := &cos.ObjectGetOptions{}
	if length > 0 {
		opt.Range = fmt.Sprintf("bytes=%d-%d", offset, offset+length-1)
	} else if offset > 0 {
		opt.Range = fmt.Sprintf("bytes=%d-", offset)
	}
	resp, err := c.cos.Object.Get(ctx, key, opt)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (c *Client) Put(ctx context.Context, key string, body io.Reader, size int64) error {
	if err := c.refresh(ctx); err != nil {
		return err
	}
	_ = size // ponytail: SDK derives size from reader; size param retained for API symmetry with T13
	_, err := c.cos.Object.Put(ctx, key, body, &cos.ObjectPutOptions{})
	return err
}

func (c *Client) Delete(ctx context.Context, key string) error {
	if err := c.refresh(ctx); err != nil {
		return err
	}
	_, err := c.cos.Object.Delete(ctx, key)
	return err
}

func (c *Client) Copy(ctx context.Context, src, dst string) error {
	if err := c.refresh(ctx); err != nil {
		return err
	}
	_, _, err := c.cos.Object.Copy(ctx, dst, src, &cos.ObjectCopyOptions{})
	return err
}
