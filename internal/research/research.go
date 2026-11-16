package research

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

type Source struct {
	ID, Name, URL, Kind string
}

type Document struct {
	URL, Title, Summary, Kind, Hash string
	PublishedAt                     *time.Time
	CollectedAt                     time.Time
}

type FeedResult struct {
	Documents          []Document
	ETag, LastModified string
	NotModified        bool
}

var ErrEvidenceUnavailable = errors.New("public document evidence unavailable")

func DefaultFeeds() []Source {
	return []Source{
		{ID: "agencia-brasil-economia", Name: "Agência Brasil — Economia", URL: "https://agenciabrasil.ebc.com.br/rss/economia/feed.xml", Kind: "news"},
		{ID: "cvm-decisoes", Name: "CVM — Decisões", URL: "https://conteudo.cvm.gov.br/feed/decisoes.xml", Kind: "primary"},
		{ID: "cvm-legislacao", Name: "CVM — Legislação", URL: "https://conteudo.cvm.gov.br/feed/legislacao.xml", Kind: "primary"},
	}
}

func (c *Client) FetchFeed(ctx context.Context, source Source, etag, lastModified string) (FeedResult, error) {
	if _, err := c.validateURL(source.URL); err != nil {
		return FeedResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return FeedResult{}, err
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}
	response, err := c.httpClient.Do(req)
	if err != nil {
		return FeedResult{}, publicRequestError(err)
	}
	defer response.Body.Close()
	result := FeedResult{ETag: response.Header.Get("ETag"), LastModified: response.Header.Get("Last-Modified")}
	if response.StatusCode == http.StatusNotModified {
		result.NotModified = true
		if result.ETag == "" {
			result.ETag = etag
		}
		if result.LastModified == "" {
			result.LastModified = lastModified
		}
		return result, nil
	}
	if response.StatusCode != http.StatusOK {
		return FeedResult{}, fmt.Errorf("public source returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return FeedResult{}, err
	}
	if len(body) > 1<<20 {
		return FeedResult{}, errors.New("public response exceeds 1 MiB")
	}
	baseURL := source.URL
	if response.Request != nil && response.Request.URL != nil {
		baseURL = response.Request.URL.String()
	}
	documents, err := c.parseFeed(body, source, baseURL, time.Now().UTC())
	result.Documents = documents
	return result, err
}

var (
	scriptText       = regexp.MustCompile(`(?is)<script\b[^>]*>.*?(</script\s*>|$)`)
	styleText        = regexp.MustCompile(`(?is)<style\b[^>]*>.*?(</style\s*>|$)`)
	htmlTags         = regexp.MustCompile(`(?s)<[^>]*>`)
	punctuationSpace = regexp.MustCompile(`\s+([.,;:!?])`)
)

func plainText(value string, limit int) string {
	value = html.UnescapeString(value)
	value = commentText.ReplaceAllString(value, " ")
	value = scriptText.ReplaceAllString(value, " ")
	value = styleText.ReplaceAllString(value, " ")
	value = htmlTags.ReplaceAllString(value, " ")
	value = strings.Join(strings.Fields(value), " ")
	value = punctuationSpace.ReplaceAllString(value, "$1")
	if len(value) > limit {
		value = value[:limit]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return strings.TrimSpace(value)
}

func (c *Client) canonicalURL(rawURL, baseURL string) (string, error) {
	if strings.Contains(rawURL, "#") {
		return "", ErrBlockedDestination
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", errors.New("invalid public URL")
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", errors.New("invalid public base URL")
	}
	u = base.ResolveReference(u)
	if _, err := c.validateURL(u.String()); err != nil {
		return "", err
	}
	u.Host = strings.ToLower(u.Host)
	if u.Port() == "443" {
		u.Host = u.Hostname()
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), nil
}

func contentHash(title, summary string, published *time.Time) string {
	date := ""
	if published != nil {
		date = published.UTC().Format(time.RFC3339Nano)
	}
	hash := sha256.Sum256([]byte(title + "\x00" + summary + "\x00" + date))
	return hex.EncodeToString(hash[:])
}
