package research

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	commentText  = regexp.MustCompile(`(?s)<!--.*?(-->|$)`)
	noscriptText = regexp.MustCompile(`(?is)<noscript\b[^>]*>.*?(</noscript\s*>|$)`)
	templateText = regexp.MustCompile(`(?is)<template\b[^>]*>.*?(</template\s*>|$)`)
	headText     = regexp.MustCompile(`(?is)<head\b[^>]*>.*?(</head\s*>|$)`)
	articleText  = regexp.MustCompile(`(?is)<article\b[^>]*>(.*?)</article\s*>`)
	mainText     = regexp.MustCompile(`(?is)<main\b[^>]*>(.*?)</main\s*>`)
	bodyText     = regexp.MustCompile(`(?is)<body\b[^>]*>(.*?)</body\s*>`)
	titleText    = regexp.MustCompile(`(?is)<title\b[^>]*>(.*?)</title\s*>`)
	h1Text       = regexp.MustCompile(`(?is)<h1\b[^>]*>(.*?)</h1\s*>`)
	metaTags     = regexp.MustCompile(`(?is)<meta\b(?:[^>"']|"[^"]*"|'[^']*')*>`)
	timeTags     = regexp.MustCompile(`(?is)<time\b(?:[^>"']|"[^"]*"|'[^']*')*>`)
	attributes   = regexp.MustCompile(`([A-Za-z_:][-A-Za-z0-9_:.]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>]+))`)
	dataScripts  = regexp.MustCompile(`(?is)<script\b((?:[^>"']|"[^"]*"|'[^']*')*)>(.*?)</script\s*>`)
	chromeText   = []*regexp.Regexp{
		regexp.MustCompile(`(?is)<nav\b[^>]*>.*?(</nav\s*>|$)`),
		regexp.MustCompile(`(?is)<header\b[^>]*>.*?(</header\s*>|$)`),
		regexp.MustCompile(`(?is)<footer\b[^>]*>.*?(</footer\s*>|$)`),
		regexp.MustCompile(`(?is)<aside\b[^>]*>.*?(</aside\s*>|$)`),
	}
)

// VerifyDocument verifies access and records the content observed by this server.
// This establishes public evidence availability, not the truth of an article's claims.
func (c *Client) VerifyDocument(ctx context.Context, rawURL string) (Document, error) {
	if _, err := c.validateURL(rawURL); err != nil {
		return Document{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Document{}, ErrEvidenceUnavailable
	}
	request.Header.Set("Accept", "text/html, application/xhtml+xml, text/plain;q=0.8")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return Document{}, fmt.Errorf("%w: %w", ErrEvidenceUnavailable, publicRequestError(err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Document{}, fmt.Errorf("%w: HTTP %d", ErrEvidenceUnavailable, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return Document{}, fmt.Errorf("%w: response read failed", ErrEvidenceUnavailable)
	}
	if len(body) > 1<<20 || !utf8.Valid(body) {
		return Document{}, ErrEvidenceUnavailable
	}
	contentType := response.Header.Get("Content-Type")
	if contentType == "" {
		contentType = http.DetectContentType(body)
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return Document{}, ErrEvidenceUnavailable
	}
	finalURL := rawURL
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL.String()
	}
	finalURL, err = c.canonicalURL(finalURL, rawURL)
	if err != nil {
		return Document{}, err
	}
	document := Document{URL: finalURL, Kind: "public", CollectedAt: time.Now().UTC()}
	var content string
	switch mediaType {
	case "text/html", "application/xhtml+xml":
		document.Title, document.Summary, document.PublishedAt, content = extractHTML(string(body), finalURL)
	case "text/plain":
		content = plainText(string(body), len(body))
		document.Summary = plainText(content, 600)
	default:
		return Document{}, ErrEvidenceUnavailable
	}
	if len(content) < 80 || len(strings.Fields(content)) < 12 {
		return Document{}, ErrEvidenceUnavailable
	}
	document.Hash = contentHash(document.Title, document.Summary+"\x00"+content, document.PublishedAt)
	return document, nil
}

func extractHTML(body, documentURL string) (title, summary string, published *time.Time, content string) {
	body = commentText.ReplaceAllString(body, " ")
	ldDate := jsonLDPublicationDate(body, documentURL)
	for _, pattern := range []*regexp.Regexp{scriptText, styleText, noscriptText, templateText} {
		body = pattern.ReplaceAllString(body, " ")
	}
	metadata := make(map[string]string)
	for _, tag := range metaTags.FindAllString(body, -1) {
		attrs := tagAttributes(tag)
		key := strings.ToLower(attrs["property"])
		if key == "" {
			key = strings.ToLower(attrs["name"])
		}
		if key == "" {
			key = strings.ToLower(attrs["itemprop"])
		}
		if key != "" && attrs["content"] != "" {
			metadata[key] = attrs["content"]
		}
	}
	title = matchedText(titleText, body)
	if title == "" {
		title = metadata["og:title"]
	}
	if title == "" {
		title = matchedText(h1Text, body)
	}
	title = plainText(title, 300)
	published = parseDate(metadata["article:published_time"], metadata["datepublished"], metadata["pubdate"], metadata["citation_publication_date"])
	if published == nil {
		published = ldDate
	}
	body = headText.ReplaceAllString(body, " ")
	if article := matchedText(articleText, body); article != "" {
		body = article
	} else if main := matchedText(mainText, body); main != "" {
		body = main
	} else if inner := matchedText(bodyText, body); inner != "" {
		body = inner
	}
	for _, pattern := range chromeText {
		body = pattern.ReplaceAllString(body, " ")
	}
	if published == nil {
		for _, tag := range timeTags.FindAllString(body, -1) {
			attrs := tagAttributes(tag)
			if !strings.EqualFold(attrs["itemprop"], "datePublished") {
				continue
			}
			if date := parseDate(attrs["datetime"]); date != nil {
				published = date
				break
			}
		}
	}
	content = plainText(body, len(body))
	summary = plainText(metadata["description"], 600)
	if summary == "" {
		summary = plainText(metadata["og:description"], 600)
	}
	if summary == "" {
		summary = plainText(content, 600)
	}
	return
}

func jsonLDPublicationDate(body, documentURL string) *time.Time {
	for _, match := range dataScripts.FindAllStringSubmatch(body, -1) {
		if !strings.EqualFold(tagAttributes(match[1])["type"], "application/ld+json") {
			continue
		}
		var data any
		if json.Unmarshal([]byte(match[2]), &data) != nil {
			continue
		}
		if date := dateFromJSONLD(data, documentURL); date != nil {
			return date
		}
	}
	return nil
}

func dateFromJSONLD(data any, documentURL string) *time.Time {
	switch value := data.(type) {
	case map[string]any:
		articleURL, _ := value["url"].(string)
		if articleType(value["@type"]) && sameDocumentURL(articleURL, documentURL) {
			if date, ok := value["datePublished"].(string); ok {
				if parsed := parseDate(date); parsed != nil {
					return parsed
				}
			}
		}
		return dateFromJSONLD(value["@graph"], documentURL)
	case []any:
		for _, item := range value {
			if date := dateFromJSONLD(item, documentURL); date != nil {
				return date
			}
		}
	}
	return nil
}

func articleType(value any) bool {
	switch kind := value.(type) {
	case string:
		kind = strings.TrimPrefix(strings.TrimPrefix(kind, "https://schema.org/"), "http://schema.org/")
		return kind == "Article" || kind == "NewsArticle" || kind == "BlogPosting"
	case []any:
		for _, item := range kind {
			if articleType(item) {
				return true
			}
		}
	}
	return false
}

func sameDocumentURL(declared, observed string) bool {
	normalize := func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
			return ""
		}
		u.Fragment, u.RawFragment = "", ""
		u.Host = strings.ToLower(u.Host)
		if u.Port() == "443" {
			u.Host = u.Hostname()
		}
		if u.Path == "" {
			u.Path = "/"
		}
		return u.String()
	}
	identity := normalize(declared)
	return identity != "" && identity == normalize(observed)
}

func matchedText(pattern *regexp.Regexp, body string) string {
	if match := pattern.FindStringSubmatch(body); len(match) > 1 {
		return match[1]
	}
	return ""
}

func tagAttributes(tag string) map[string]string {
	attrs := make(map[string]string)
	for _, match := range attributes.FindAllStringSubmatch(tag, -1) {
		value := match[2]
		if value == "" {
			value = match[3]
		}
		if value == "" {
			value = match[4]
		}
		attrs[strings.ToLower(match[1])] = html.UnescapeString(value)
	}
	return attrs
}
