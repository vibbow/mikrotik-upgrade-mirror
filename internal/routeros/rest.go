// Package routeros is a small client for the RouterOS REST API (/rest, v7.1+).
package routeros

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// ErrAuth means the router rejected the login. Callers must not retry: repeated bad
// logins can lock the source address out.
var ErrAuth = errors.New("登录失败 (用户名或密码被拒绝)")

// Client talks to one router.
type Client struct {
	base       string
	user, pass string
	http       *http.Client
}

// Dial finds the REST endpoint (https first, then http) and verifies the login. It
// makes exactly one authenticated request per scheme and stops at the first 401.
func Dial(ctx context.Context, host, user, pass string, timeout time.Duration) (*Client, error) {
	tr := &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, // routers use self-signed certs
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: timeout,
		DialContext:         (&net.Dialer{Timeout: timeout}).DialContext,
	}
	var errs []string
	for _, scheme := range []string{"https", "http"} {
		c := &Client{
			base: scheme + "://" + host + "/rest",
			user: user, pass: pass,
			http: &http.Client{Transport: tr, Timeout: timeout},
		}
		err := c.probe(ctx)
		if err == nil {
			return c, nil
		}
		if errors.Is(err, ErrAuth) {
			return nil, err
		}
		if strings.Contains(err.Error(), "HTTP 404") {
			err = fmt.Errorf("此地址没有 REST API (可能是 RouterOS v6，或 www 服务未开启)")
		}
		errs = append(errs, scheme+": "+err.Error())
	}
	return nil, errors.New(strings.Join(errs, "; "))
}

// WithTimeout returns a copy whose requests may take up to d (for long downloads).
func (c *Client) WithTimeout(d time.Duration) *Client {
	cp := *c
	h := *c.http
	h.Timeout = d
	cp.http = &h
	return &cp
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.user, c.pass)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrAuth
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
			Detail  string `json:"detail"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Message != "" {
			if e.Detail != "" {
				return fmt.Errorf("%s %s: %s (%s)", method, path, e.Message, e.Detail)
			}
			return fmt.Errorf("%s %s: %s", method, path, e.Message)
		}
		return fmt.Errorf("%s %s: HTTP %d", method, path, resp.StatusCode)
	}
	if out != nil && len(bytes.TrimSpace(raw)) == 0 {
		return fmt.Errorf("%s %s: HTTP %d 但响应内容为空 (Server=%q, Content-Type=%q)", method, path, resp.StatusCode, resp.Header.Get("Server"), resp.Header.Get("Content-Type"))
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%s %s: bad JSON: %w", method, path, err)
		}
	}
	return nil
}

func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}
func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, body, out)
}
func (c *Client) Put(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPut, path, body, out)
}
func (c *Client) Patch(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPatch, path, body, out)
}

// Row is one item of a RouterOS list; every value is a string.
type Row map[string]string

// ID is the item's .id ("*1").
func (r Row) ID() string { return r[".id"] }

// List fetches a menu as rows.
func (c *Client) List(ctx context.Context, path string) ([]Row, error) {
	var rows []Row
	if err := c.Get(ctx, path, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// MainVersion strips the channel suffix: "7.23.7 (long-term)" -> "7.23.7".
func MainVersion(v string) string {
	if i := strings.IndexByte(v, ' '); i > 0 {
		return v[:i]
	}
	return v
}

// probe checks that the endpoint really is a RouterOS REST API: /system/resource must
// answer with an object that has a version. Anything else (an empty 200, an HTML page)
// is reported with a snippet of what came back.
func (c *Client) probe(ctx context.Context) error {
	var raw json.RawMessage
	if err := c.Get(ctx, "/system/resource", &raw); err != nil {
		return err
	}
	var res map[string]any
	if json.Unmarshal(raw, &res) == nil {
		if _, ok := res["version"]; ok {
			return nil
		}
	}
	snip := strings.TrimSpace(string(raw))
	if len(snip) > 120 {
		snip = snip[:120] + "..."
	}
	return fmt.Errorf("%s 不是有效的 RouterOS REST 响应 (没有 version 字段), 返回内容: %q", c.base, snip)
}
