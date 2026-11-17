package action

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Correlation struct {
	WorkflowID  string
	RunID       string
	ComponentID string
}

type FileClient struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func NewFileClient(baseURL, token string, timeout time.Duration) *FileClient {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &FileClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: timeout},
	}
}

type renameRequest struct {
	Username  string `json:"username"`
	From      string `json:"from"`
	To        string `json:"to"`
	Overwrite bool   `json:"overwrite"`
}

type removeRequest struct {
	Username  string `json:"username"`
	Path      string `json:"path"`
	MissingOK bool   `json:"missing_ok"`
}

type copyRequest struct {
	SourceUsername string `json:"source_username"`
	SourcePath     string `json:"source_path"`
	TargetUsername string `json:"target_username"`
	TargetPath     string `json:"target_path"`
	Overwrite      bool   `json:"overwrite"`
}

type StatResult struct {
	Exists   bool      `json:"exists"`
	IsDir    bool      `json:"is_dir"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

func (c *FileClient) Rename(ctx context.Context, username, from, to string, overwrite bool, corr Correlation) error {
	return c.post(ctx, "/internal/v1/files/rename", renameRequest{Username: username, From: from, To: to, Overwrite: overwrite}, corr, nil)
}

func (c *FileClient) Remove(ctx context.Context, username, path string, missingOK bool, corr Correlation) error {
	return c.post(ctx, "/internal/v1/files/remove", removeRequest{Username: username, Path: path, MissingOK: missingOK}, corr, nil)
}

func (c *FileClient) Copy(ctx context.Context, srcUser, srcPath, dstUser, dstPath string, overwrite bool, corr Correlation) error {
	return c.post(ctx, "/internal/v1/files/copy", copyRequest{
		SourceUsername: srcUser, SourcePath: srcPath,
		TargetUsername: dstUser, TargetPath: dstPath, Overwrite: overwrite,
	}, corr, nil)
}

func (c *FileClient) Stat(ctx context.Context, username, path string, corr Correlation) (*StatResult, error) {
	q := url.Values{}
	q.Set("username", username)
	q.Set("path", path)
	var out StatResult
	if err := c.get(ctx, "/internal/v1/files/stat?"+q.Encode(), corr, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *FileClient) post(ctx context.Context, path string, body any, corr Correlation, out any) error {
	return c.do(ctx, http.MethodPost, path, body, corr, out)
}

func (c *FileClient) get(ctx context.Context, path string, corr Correlation, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, corr, out)
}

func (c *FileClient) do(ctx context.Context, method, path string, body any, corr Correlation, out any) error {
	if c == nil || c.BaseURL == "" {
		return fmt.Errorf("file API is not configured")
	}
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("X-Workflow-Token", c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if corr.WorkflowID != "" {
		req.Header.Set("X-Workflow-Id", corr.WorkflowID)
	}
	if corr.RunID != "" {
		req.Header.Set("X-Workflow-Run-Id", corr.RunID)
	}
	if corr.ComponentID != "" {
		req.Header.Set("X-Workflow-Component-Id", corr.ComponentID)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return retryable(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &HTTPError{Status: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil && err != io.EOF {
			return err
		}
	}
	return nil
}
