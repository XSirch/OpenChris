package codex

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	base, token string
	http        *http.Client
}

func NewClient(base, token string) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || len(token) < 32 {
		return nil, failure("invalid_configuration")
	}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if !ip.IP.IsPrivate() && !ip.IP.IsLoopback() {
				return nil, failure("invalid_configuration")
			}
		}
		if len(ips) == 0 {
			return nil, failure("unavailable")
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}, MaxIdleConns: 2, IdleConnTimeout: 30 * time.Second}
	return &Client{base: strings.TrimRight(base, "/"), token: token, http: &http.Client{Transport: transport, Timeout: 185 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) call(ctx context.Context, method, path string, input, out any) error {
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return failure("invalid_request")
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return failure("invalid_request")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return failure("failed_unknown")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, MaxResult+2049))
	if err != nil || len(raw) > MaxResult+2048 {
		return failure("invalid_output")
	}
	if response.StatusCode != 200 {
		var problem Error
		if strictJSON(raw, &problem) == nil && allowedError(problem.Kind) {
			problem.Transient = problem.Kind == "busy"
			return &problem
		}
		return failure("unavailable")
	}
	return strictJSON(raw, out)
}
func (c *Client) Status(ctx context.Context) (Status, error) {
	var s Status
	err := c.call(ctx, "GET", "/v1/status", nil, &s)
	return s, err
}
func (c *Client) Run(ctx context.Context, in Request) (Result, error) {
	var r Result
	if err := in.Validate(); err != nil {
		return r, err
	}
	err := c.call(ctx, "POST", "/v1/run", in, &r)
	return r, err
}
func allowedError(s string) bool {
	switch s {
	case "busy", "not_authenticated", "rate_limited", "unavailable", "failed_unknown", "invalid_request", "invalid_output", "invalid_evidence", "incomplete", "output_limit", "policy_violation", "auth_storage_failed":
		return true
	}
	return false
}
func authorized(header, token string) bool {
	return len(header) == len(token)+7 && subtle.ConstantTimeCompare([]byte(header), []byte("Bearer "+token)) == 1
}
