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
			// COS 0-byte placeholders come back with "0001-01-01T00:00:00.000Z"
			// — parses fine but is year 1. FileZilla's "filter invalid dates"
			// hides anything before 1980. Substitute now() so the dir is shown.
			if t.Year() < 1980 {
				return time.Now()
			}
			return t
		}
	}
	return time.Now()
}

func (c *Client) List(ctx context.Context, prefix string) ([]Entry, error) {
	if err := c.refresh(ctx); err != nil {
		return nil, err
	}
	if !strings.HasSuffix(prefix, "/") && prefix != "" {
		prefix += "/"
	}
	var res *cos.BucketGetResult
	err := c.do(ctx, func() error {
		var ierr error
		res, _, ierr = c.cos.Bucket.Get(ctx, &cos.BucketGetOptions{Prefix: prefix, Delimiter: "/"})
		return ierr
	})
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0)
	seenDirs := make(map[string]struct{})
	for _, p := range res.CommonPrefixes {
		name := strings.TrimSuffix(strings.TrimPrefix(p, prefix), "/")
		if name == "" {
			continue
		}
		seenDirs[name] = struct{}{}
		// CommonPrefixes carries no object metadata (no LastModified) — COS
		// only returns the prefix string. Zero-value ModifyTime renders as
		// year 1, which FileZilla's LIST view filters out entirely. Same
		// fix as the Contents/placeholder branch below: substitute now().
		out = append(out, Entry{Name: name, IsDir: true, ModifyTime: time.Now()})
	}
	for _, o := range res.Contents {
		name := strings.TrimPrefix(o.Key, prefix)
		if name == "" {
			continue
		}
		// Empty-folder placeholders (created by Mkdir) show up in Contents
		// with a trailing "/" when there is nothing deeper — CommonPrefixes
		// only fires for prefixes with at least one grandchild key. Promote
		// them to directories and dedupe against CommonPrefixes.
		if strings.HasSuffix(name, "/") {
			dirName := strings.TrimSuffix(name, "/")
			if dirName == "" {
				continue
			}
			if _, dup := seenDirs[dirName]; dup {
				continue
			}
			seenDirs[dirName] = struct{}{}
			out = append(out, Entry{Name: dirName, Size: o.Size, ModifyTime: parseTime(o.LastModified), IsDir: true})
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
	var resp *cos.Response
	err := c.do(ctx, func() error {
		var ierr error
		resp, ierr = c.cos.Object.Head(ctx, key, &cos.ObjectHeadOptions{})
		return ierr
	})
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
	var resp *cos.Response
	err := c.do(ctx, func() error {
		var ierr error
		resp, ierr = c.cos.Object.Get(ctx, key, opt)
		return ierr
	})
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
	return c.do(ctx, func() error {
		_, err := c.cos.Object.Put(ctx, key, body, &cos.ObjectPutOptions{})
		return err
	})
}

func (c *Client) Delete(ctx context.Context, key string) error {
	if err := c.refresh(ctx); err != nil {
		return err
	}
	return c.do(ctx, func() error {
		_, err := c.cos.Object.Delete(ctx, key)
		return err
	})
}

func (c *Client) Copy(ctx context.Context, src, dst string) error {
	if err := c.refresh(ctx); err != nil {
		return err
	}
	return c.do(ctx, func() error {
		_, _, err := c.cos.Object.Copy(ctx, dst, src, &cos.ObjectCopyOptions{})
		return err
	})
}
