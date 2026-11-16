package telegram

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
	"time"
	"unicode/utf8"
)

var (
	ErrDisabled     = errors.New("telegram disabled")
	ErrUnauthorized = errors.New("telegram unauthorized")
	ErrInvalid      = errors.New("telegram invalid input")
	ErrConflict     = errors.New("telegram update conflicts with accepted content")
)

type Config struct {
	Enabled         bool
	AutonomyEnabled bool
	BotToken        string
	WebhookSecret   string
	PublicURL       string
	AllowedUserID   int64
	AllowedChatID   int64
}

var tokenPattern = regexp.MustCompile(`^[1-9][0-9]*:[A-Za-z0-9_-]+$`)
var secretPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{32,256}$`)

func tokenIdentity(token string) (int64, error) {
	if len(token) > 256 || !tokenPattern.MatchString(token) {
		return 0, ErrInvalid
	}
	id, err := strconv.ParseInt(strings.SplitN(token, ":", 2)[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, ErrInvalid
	}
	return id, nil
}

func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if _, err := tokenIdentity(c.BotToken); err != nil {
		return ErrInvalid
	}
	u, err := url.Parse(c.PublicURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !secretPattern.MatchString(c.WebhookSecret) || c.AllowedUserID <= 0 || c.AllowedChatID <= 0 {
		return ErrInvalid
	}
	return nil
}

// APIError contains only safe operational metadata. Telegram descriptions and
// request URLs can contain secrets and are never retained in errors.
type APIError struct {
	Code       int
	RetryAfter time.Duration
	Ambiguous  bool
}

func (e *APIError) Error() string {
	if e.Ambiguous {
		return "telegram delivery outcome unknown"
	}
	return fmt.Sprintf("telegram API rejected request (code %d)", e.Code)
}

type Client struct {
	token, baseURL string
	http           *http.Client
}

func NewClient(token string) (*Client, error) {
	return NewClientWithBaseURL(token, "https://api.telegram.org", nil)
}

// NewClientWithBaseURL makes the endpoint override explicit for isolated API
// tests. Runtime configuration never supplies an alternate Telegram endpoint.
func NewClientWithBaseURL(token, baseURL string, httpClient *http.Client) (*Client, error) {
	if _, err := tokenIdentity(token); err != nil {
		return nil, ErrInvalid
	}
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, ErrInvalid
	}
	var bounded http.Client
	if httpClient != nil {
		bounded = *httpClient
	}
	if bounded.Timeout <= 0 || bounded.Timeout > 15*time.Second {
		bounded.Timeout = 10 * time.Second
	}
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{token: token, baseURL: strings.TrimRight(baseURL, "/"), http: &bounded}, nil
}

func (c *Client) call(ctx context.Context, method string, payload any, result any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return ErrInvalid
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return &APIError{Ambiguous: method == "sendMessage"}
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return &APIError{Ambiguous: method == "sendMessage"}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return &APIError{Code: response.StatusCode, Ambiguous: method == "sendMessage"}
	}
	var envelope struct {
		OK         bool            `json:"ok"`
		Result     json.RawMessage `json:"result"`
		Code       int             `json:"error_code"`
		Parameters struct {
			RetryAfter int64 `json:"retry_after"`
		} `json:"parameters"`
	}
	decodeErr := json.Unmarshal(data, &envelope)
	if response.StatusCode == http.StatusTooManyRequests || (decodeErr == nil && !envelope.OK && envelope.Code == 429) {
		retry := envelope.Parameters.RetryAfter
		if retry < 1 {
			retry = 1
		}
		if retry > 3600 {
			retry = 3600
		}
		return &APIError{Code: 429, RetryAfter: time.Duration(retry) * time.Second}
	}
	if decodeErr != nil || response.StatusCode < 200 || response.StatusCode >= 300 || !envelope.OK {
		code := response.StatusCode
		if decodeErr == nil && !envelope.OK && envelope.Code >= 400 && envelope.Code <= 599 {
			code = envelope.Code
		}
		// Only a parsed ok:false proves the provider rejected the operation.
		ambiguous := method == "sendMessage" && !(decodeErr == nil && !envelope.OK && envelope.Code >= 400 && envelope.Code <= 599)
		return &APIError{Code: code, Ambiguous: ambiguous}
	}
	if len(envelope.Result) == 0 || string(envelope.Result) == "null" || json.Unmarshal(envelope.Result, result) != nil {
		return &APIError{Ambiguous: method == "sendMessage"}
	}
	return nil
}

type Bot struct {
	ID    int64 `json:"id"`
	IsBot bool  `json:"is_bot"`
}
type WebhookInfo struct {
	URL                string `json:"url"`
	PendingUpdateCount int    `json:"pending_update_count"`
}

func (c *Client) GetMe(ctx context.Context) (Bot, error) {
	var bot Bot
	err := c.call(ctx, "getMe", struct{}{}, &bot)
	if err == nil && (bot.ID <= 0 || !bot.IsBot) {
		err = &APIError{}
	}
	return bot, err
}
func (c *Client) GetWebhookInfo(ctx context.Context) (WebhookInfo, error) {
	var info WebhookInfo
	err := c.call(ctx, "getWebhookInfo", struct{}{}, &info)
	return info, err
}
func (c *Client) SetWebhook(ctx context.Context, cfg Config) error {
	var ok bool
	err := c.call(ctx, "setWebhook", struct {
		URL         string   `json:"url"`
		Secret      string   `json:"secret_token"`
		Updates     []string `json:"allowed_updates"`
		Connections int      `json:"max_connections"`
		Drop        bool     `json:"drop_pending_updates"`
	}{cfg.PublicURL, cfg.WebhookSecret, []string{"message"}, 1, false}, &ok)
	if err == nil && !ok {
		return &APIError{}
	}
	return err
}
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) (int64, error) {
	if chatID <= 0 || strings.TrimSpace(text) == "" || strings.ContainsRune(text, 0) || !utf8.ValidString(text) || utf8.RuneCountInString(text) > 4096 {
		return 0, ErrInvalid
	}
	var message struct {
		ID int64 `json:"message_id"`
	}
	err := c.call(ctx, "sendMessage", struct {
		ChatID  int64  `json:"chat_id"`
		Text    string `json:"text"`
		Protect bool   `json:"protect_content"`
	}{chatID, text, true}, &message)
	if err == nil && message.ID <= 0 {
		err = &APIError{Ambiguous: true}
	}
	return message.ID, err
}
