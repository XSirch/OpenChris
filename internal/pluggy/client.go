// Package pluggy reads personal MeuPluggy connections. It never refreshes an
// item or exposes payment, transfer, or trading operations.
package pluggy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const maxResponseBytes = 2 << 20
const maxPages = 20
const maxRecords = 10000
const WebhookHeader = "X-Conselho-Webhook-Secret"

var ErrDisabled = errors.New("Pluggy integration is disabled")
var ErrInvalid = errors.New("invalid Pluggy input")
var ErrUnauthorized = errors.New("Pluggy ownership or webhook authentication failed")
var ErrCapacity = errors.New("personal MeuPluggy supports up to five active connections")
var ErrBusy = errors.New("Pluggy item synchronization is busy")
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Object keeps provider decimals as json.Number and absent values as nil.
type Object = map[string]any

type APIError struct {
	Status     int
	RequestID  string
	RetryAfter time.Duration
}

func (e *APIError) Error() string { return fmt.Sprintf("Pluggy API returned HTTP %d", e.Status) }

type Client struct {
	clientID, clientSecret, baseURL string
	http                            *http.Client
	now                             func() time.Time
	mu                              sync.Mutex
	apiKey                          string
	keyUntil                        time.Time
}
type ClientOption func(*Client)

// WithBaseURL is for local HTTP fixtures; production uses https://api.pluggy.ai.
func WithBaseURL(base string) ClientOption {
	return func(c *Client) { c.baseURL = strings.TrimRight(base, "/") }
}
func WithHTTPClient(h *http.Client) ClientOption {
	return func(c *Client) {
		if h != nil {
			clone := *h
			c.http = &clone
		}
	}
}
func NewClient(clientID, clientSecret string, opts ...ClientOption) *Client {
	c := &Client{clientID: clientID, clientSecret: clientSecret, baseURL: "https://api.pluggy.ai", http: &http.Client{}, now: time.Now}
	for _, opt := range opts {
		opt(c)
	}
	// Apply these after options: callers cannot accidentally forward the key on redirects.
	c.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if c.http.Timeout <= 0 || c.http.Timeout > 20*time.Second {
		c.http.Timeout = 20 * time.Second
	}
	return c
}

func (c *Client) request(ctx context.Context, method, path string, q url.Values, body any, key string) (Object, error) {
	base, err := url.Parse(c.baseURL)
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, ErrInvalid
	}
	if !allowedRequest(method, path) {
		return nil, ErrInvalid
	}
	base.Path = strings.TrimRight(base.Path, "/") + path
	base.RawQuery = q.Encode()
	var payload io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return nil, e
		}
		payload = bytes.NewReader(b)
	}
	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, base.String(), payload)
	if err != nil {
		return nil, ErrInvalid
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if key != "" {
		req.Header.Set("X-API-KEY", key)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Pluggy request failed: %w", safeTransportError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		e := &APIError{Status: resp.StatusCode}
		if id := resp.Header.Get("X-Request-ID"); uuidPattern.MatchString(id) {
			e.RequestID = id
		}
		if n, e2 := strconv.Atoi(resp.Header.Get("Retry-After")); e2 == nil && n > 0 && n <= 86400 {
			e.RetryAfter = time.Duration(n) * time.Second
		}
		return nil, e
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, errors.New("Pluggy response read failed")
	}
	if len(b) > maxResponseBytes || !utf8.Valid(b) {
		return nil, errors.New("invalid or oversized Pluggy response")
	}
	if budget, ok := ctx.Value(responseBudgetKey{}).(*responseBudget); ok {
		budget.mu.Lock()
		budget.bytes += len(b)
		exceeded := budget.bytes > 8<<20
		budget.mu.Unlock()
		if exceeded {
			return nil, errors.New("Pluggy source snapshot byte limit exceeded")
		}
	}
	if resp.StatusCode == http.StatusNoContent {
		return Object{"results": []any{}, "totalPages": json.Number("0")}, nil
	}
	var out Object
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if d.Decode(&out) != nil || out == nil {
		return nil, errors.New("invalid Pluggy JSON response")
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("multiple Pluggy response values")
	}
	return out, nil
}

// Transport errors may embed a URL; return only errors that cannot contain credentials.
func safeTransportError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return errors.New("transport unavailable")
}
func allowedRequest(method, path string) bool {
	if method == http.MethodPost {
		return path == "/auth" || path == "/connect_token" || path == "/webhooks"
	}
	if method != http.MethodGet {
		return false
	}
	if strings.HasPrefix(path, "/items/") {
		return uuidPattern.MatchString(strings.TrimPrefix(path, "/items/"))
	}
	return path == "/accounts" || path == "/v2/transactions" || path == "/investments" || path == "/connectors" || path == "/webhooks"
}
func (c *Client) key(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.apiKey != "" && c.now().Before(c.keyUntil) {
		return c.apiKey, nil
	}
	if strings.TrimSpace(c.clientID) == "" || strings.TrimSpace(c.clientSecret) == "" {
		return "", ErrDisabled
	}
	out, err := c.request(ctx, "POST", "/auth", nil, Object{"clientId": c.clientID, "clientSecret": c.clientSecret}, "")
	if err != nil {
		return "", err
	}
	key, _ := out["apiKey"].(string)
	if key == "" || len(key) > 8192 {
		return "", errors.New("Pluggy authentication omitted API key")
	}
	c.apiKey = key
	c.keyUntil = c.now().Add(2*time.Hour - time.Minute)
	return key, nil
}
func (c *Client) authenticated(ctx context.Context, method, path string, q url.Values, body any) (Object, error) {
	key, err := c.key(ctx)
	if err != nil {
		return nil, err
	}
	out, err := c.request(ctx, method, path, q, body, key)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == 401 {
		c.mu.Lock()
		if c.apiKey == key {
			c.apiKey = ""
			c.keyUntil = time.Time{}
		}
		c.mu.Unlock()
	}
	return out, err
}

func (c *Client) Item(ctx context.Context, id string) (Object, error) {
	if !uuidPattern.MatchString(id) {
		return nil, ErrInvalid
	}
	out, err := c.authenticated(ctx, "GET", "/items/"+id, nil, nil)
	if err == nil && textField(out, "id") != id {
		return nil, errors.New("Pluggy returned a different item")
	}
	return out, err
}
func (c *Client) ConnectToken(ctx context.Context, userID string) (string, error) {
	// MeuPluggy starts OAuth with empty credentials. Comparing them with earlier
	// consents blocks authorization before the user can select a bank to share.
	out, err := c.authenticated(ctx, "POST", "/connect_token", nil, Object{"options": Object{"clientUserId": userID, "avoidDuplicates": false, "products": []string{"ACCOUNTS", "CREDIT_CARDS", "TRANSACTIONS", "INVESTMENTS"}}})
	if err != nil {
		return "", err
	}
	token, _ := out["accessToken"].(string)
	if token == "" || len(token) > 16384 {
		return "", errors.New("Pluggy response omitted connect token")
	}
	return token, nil
}
func (c *Client) Accounts(ctx context.Context, itemID string) ([]Object, error) {
	if !uuidPattern.MatchString(itemID) {
		return nil, ErrInvalid
	}
	return c.pages(ctx, "/accounts", url.Values{"itemId": {itemID}})
}
func (c *Client) Transactions(ctx context.Context, accountID string) ([]Object, error) {
	if !uuidPattern.MatchString(accountID) {
		return nil, ErrInvalid
	}
	ctx = withResponseBudget(ctx)
	query := url.Values{"accountId": {accountID}}
	seen := map[string]bool{query.Encode(): true}
	rows := []Object{}
	for range maxPages {
		out, err := c.authenticated(ctx, "GET", "/v2/transactions", query, nil)
		if err != nil {
			return nil, err
		}
		if err = c.checkPaginationLinks(out); err != nil {
			return nil, err
		}
		results, ok := out["results"].([]any)
		if !ok {
			return nil, errors.New("Pluggy transaction page omitted results")
		}
		for _, raw := range results {
			row, ok := raw.(map[string]any)
			if !ok {
				return nil, errors.New("invalid Pluggy transaction record")
			}
			rows = append(rows, row)
		}
		if len(results) > 500 || len(rows) > maxRecords {
			return nil, errors.New("Pluggy transaction record limit exceeded")
		}
		next, exists := out["next"]
		if !exists {
			return nil, errors.New("Pluggy transaction page omitted cursor")
		}
		if next == nil {
			return rows, nil
		}
		link, ok := next.(string)
		if !ok || link == "" || len(link) > 8192 {
			return nil, errors.New("invalid Pluggy transaction cursor")
		}
		u, err := url.Parse(link)
		base, _ := url.Parse(c.baseURL)
		if err != nil || u.User != nil || u.Fragment != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/v2/transactions") || (u.Host == "" && u.Scheme != "") || (u.Host != "" && (u.Host != base.Host || u.Scheme != base.Scheme)) {
			return nil, errors.New("unsafe Pluggy transaction cursor URL")
		}
		values, err := url.ParseQuery(u.RawQuery)
		if err != nil || len(values) != 2 || len(values["accountId"]) != 1 || values.Get("accountId") != accountID || len(values["after"]) != 1 || values.Get("after") == "" {
			return nil, errors.New("Pluggy transaction cursor changed account or filters")
		}
		encoded := values.Encode()
		if seen[encoded] {
			return nil, errors.New("Pluggy transaction cursor repeated")
		}
		seen[encoded] = true
		query = values
	}
	return nil, errors.New("Pluggy transaction page limit exceeded")
}
func (c *Client) Investments(ctx context.Context, itemID string) ([]Object, error) {
	if !uuidPattern.MatchString(itemID) {
		return nil, ErrInvalid
	}
	return c.pages(ctx, "/investments", url.Values{"itemId": {itemID}})
}
func (c *Client) Connectors(ctx context.Context) ([]Object, error) {
	return c.pages(ctx, "/connectors", url.Values{"search": {"MeuPluggy"}})
}

func (c *Client) pages(ctx context.Context, path string, query url.Values) ([]Object, error) {
	ctx = withResponseBudget(ctx)
	if query == nil {
		query = url.Values{}
	}
	query.Set("pageSize", "500")
	rows := []Object{}
	expectedPages := -1
	for page := 1; page <= maxPages; page++ {
		query.Set("page", strconv.Itoa(page))
		out, err := c.authenticated(ctx, "GET", path, query, nil)
		if err != nil {
			return nil, err
		}
		if err = c.checkPaginationLinks(out); err != nil {
			return nil, err
		}
		results, ok := out["results"].([]any)
		if !ok {
			return nil, errors.New("Pluggy list omitted results")
		}
		for _, raw := range results {
			row, ok := raw.(map[string]any)
			if !ok {
				return nil, errors.New("invalid Pluggy list record")
			}
			rows = append(rows, row)
		}
		if len(results) > 500 || len(rows) > maxRecords {
			return nil, errors.New("Pluggy record limit exceeded")
		}
		total, hasTotal := out["totalPages"]
		last := 1
		if !hasTotal {
			return nil, errors.New("Pluggy list omitted page count")
		}
		if hasTotal {
			n, ok := total.(json.Number)
			if !ok {
				return nil, errors.New("invalid Pluggy page count")
			}
			last, err = strconv.Atoi(n.String())
			if err != nil || last < 0 || last > maxPages {
				return nil, errors.New("Pluggy page limit exceeded")
			}
		}
		if expectedPages >= 0 && last != expectedPages {
			return nil, errors.New("Pluggy page count changed during traversal")
		}
		expectedPages = last
		if supplied, ok := out["page"].(json.Number); ok && supplied.String() != strconv.Itoa(page) {
			return nil, errors.New("Pluggy returned a different page")
		}
		if page >= last {
			return rows, nil
		}
		if len(results) == 0 {
			return nil, errors.New("Pluggy list ended before final page")
		}
	}
	return nil, errors.New("Pluggy page limit exceeded")
}

type responseBudgetKey struct{}
type responseBudget struct {
	mu    sync.Mutex
	bytes int
}

func withResponseBudget(ctx context.Context) context.Context {
	if ctx.Value(responseBudgetKey{}) != nil {
		return ctx
	}
	return context.WithValue(ctx, responseBudgetKey{}, &responseBudget{})
}
func (c *Client) checkPaginationLinks(out Object) error {
	for _, key := range []string{"next", "nextUrl", "nextPageUrl", "nextPage", "previous"} {
		if value, ok := out[key].(string); ok && value != "" {
			u, err := url.Parse(value)
			base, _ := url.Parse(c.baseURL)
			if err != nil || u.User != nil || (u.Host != "" && (u.Host != base.Host || u.Scheme != base.Scheme)) {
				return errors.New("external Pluggy pagination URL rejected")
			}
		}
	}
	if links, ok := out["links"].(map[string]any); ok {
		return c.checkPaginationLinks(links)
	}
	return nil
}
func textField(o Object, key string) string { s, _ := o[key].(string); return s }
