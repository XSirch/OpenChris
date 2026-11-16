package research

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type responseTransport func(*http.Request) (*http.Response, error)

func (f responseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fixtureClient(body string) *Client {
	c := NewClient([]string{"news.example"})
	c.httpClient.Transport = responseTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/rss+xml"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	return c
}

func TestFetchFeedAtomAndMissingPublishedDate(t *testing.T) {
	c := fixtureClient(`<feed xmlns="http://www.w3.org/2005/Atom"><entry><title type="xhtml"><div xmlns="http://www.w3.org/1999/xhtml">Norma <b>nova</b></div></title><link rel="self" href="https://news.example/api/1"/><link rel="alternate" href="/norma"/><summary type="html">&lt;p&gt;Regra de aplicação.&lt;/p&gt;</summary><published>2019-05-02T10:00:00Z</published></entry><entry><title>Sem publicação</title><link href="https://news.example/sem-publicacao"/><content type="xhtml"><div xmlns="http://www.w3.org/1999/xhtml"><p>Texto <b>completo</b>.</p></div></content><updated>2026-11-07T12:00:00Z</updated></entry></feed>`)
	result, err := c.FetchFeed(context.Background(), Source{URL: "https://news.example/feed", Kind: "primary"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Documents) != 2 {
		t.Fatalf("documents = %d, want 2", len(result.Documents))
	}
	if got := result.Documents[0]; got.URL != "https://news.example/norma" || got.Title != "Norma nova" || got.Summary != "Regra de aplicação." || got.PublishedAt == nil || got.PublishedAt.Year() != 2019 {
		t.Fatalf("atom document: %+v", got)
	}
	if got := result.Documents[1]; got.PublishedAt != nil || got.Summary != "Texto completo." {
		t.Fatalf("missing published date or XHTML: %+v", got)
	}
}

func TestFetchFeedDeduplicatesURLsAndContentAndLimitsItems(t *testing.T) {
	var body strings.Builder
	body.WriteString(`<rss><channel>`)
	body.WriteString(`<item><title>Original</title><link>https://NEWS.example:443/same</link><description>Original</description></item><item><title>Changed</title><link>https://news.example/same</link><description>Changed</description></item><item><title>Original</title><link>https://news.example/syndicated</link><description>Original</description></item>`)
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&body, `<item><title>Title %d</title><link>https://news.example/item/%d</link><description>Unique %d</description></item>`, i, i, i)
	}
	body.WriteString(`</channel></rss>`)
	result, err := fixtureClient(body.String()).FetchFeed(context.Background(), Source{URL: "https://news.example/feed"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Documents) != 30 {
		t.Fatalf("documents = %d, want 30", len(result.Documents))
	}
	if result.Documents[0].URL != "https://news.example/same" || result.Documents[1].URL != "https://news.example/item/0" || result.Documents[29].URL != "https://news.example/item/28" {
		t.Fatalf("dedupe/order failed: %+v", result.Documents)
	}
}

func TestFetchFeedConditionalRequests(t *testing.T) {
	c := fixtureClient("")
	c.httpClient.Transport = responseTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("If-None-Match") != `"old"` || r.Header.Get("If-Modified-Since") != "Mon, 02 Jan 2006 15:04:05 GMT" {
			t.Errorf("missing conditional headers: %v", r.Header)
		}
		return &http.Response{StatusCode: http.StatusNotModified, Header: http.Header{"Etag": {`"current"`}, "Last-Modified": {"Mon, 02 Jan 2006 15:04:05 GMT"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	result, err := c.FetchFeed(context.Background(), Source{URL: "https://news.example/feed"}, `"old"`, "Mon, 02 Jan 2006 15:04:05 GMT")
	if err != nil {
		t.Fatal(err)
	}
	if !result.NotModified || len(result.Documents) != 0 || result.ETag != `"current"` || result.LastModified != "Mon, 02 Jan 2006 15:04:05 GMT" {
		t.Fatalf("cache result: %+v", result)
	}
}

func TestFetchFeedRejectsUnavailableOrInvalidResponse(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"malformed XML", `<rss><channel>`, 200},
		{"unrecognized XML", `<html><body>Not a feed</body></html>`, 200},
		{"oversized", `<rss><channel><item><title>` + strings.Repeat("x", 1<<20) + `</title></item></channel></rss>`, 200},
		{"HTTP failure", `<rss><channel/></rss>`, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixtureClient(tc.body)
			base := c.httpClient.Transport
			c.httpClient.Transport = responseTransport(func(r *http.Request) (*http.Response, error) {
				response, err := base.RoundTrip(r)
				response.StatusCode = tc.status
				return response, err
			})
			if _, err := c.FetchFeed(context.Background(), Source{URL: "https://news.example/feed"}, "", ""); err == nil {
				t.Fatal("invalid source was treated as successful collection")
			}
		})
	}
}

func TestFetchFeedLimitsUTF8SummaryAndReturnsCacheValidators(t *testing.T) {
	c := fixtureClient(`<rss><channel><item><title>Crédito</title><link>https://news.example/credito</link><description>` + strings.Repeat("çã", 301) + `</description></item></channel></rss>`)
	base := c.httpClient.Transport
	c.httpClient.Transport = responseTransport(func(r *http.Request) (*http.Response, error) {
		response, err := base.RoundTrip(r)
		response.Header.Set("ETag", `"one"`)
		response.Header.Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		return response, err
	})
	result, err := c.FetchFeed(context.Background(), Source{URL: "https://news.example/feed"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Documents) != 1 || len(result.Documents[0].Summary) > 600 || !utf8.ValidString(result.Documents[0].Summary) || result.ETag != `"one"` || result.LastModified == "" {
		t.Fatalf("limits/cache result: %+v", result)
	}
}

func TestFetchFeedHonorsCancellation(t *testing.T) {
	c := fixtureClient("")
	c.httpClient.Transport = responseTransport(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := c.FetchFeed(ctx, Source{URL: "https://news.example/feed"}, "", ""); err == nil {
		t.Fatal("timeout was ignored")
	}
	if time.Since(started) > time.Second {
		t.Fatal("cancellation did not bound collection")
	}
}

func TestFetchFeedRSSPreservesDatesAndPlainUTF8(t *testing.T) {
	c := fixtureClient(`<rss version="2.0"><channel>
<item><title><![CDATA[Inflação &amp; crédito]]></title><link>https://news.example/antiga</link><description><![CDATA[<p>Decisão sobre <b>crédito</b> &amp; inflação.</p><script>malicious()</script>]]></description><pubDate>Mon, 02 Jan 2006 15:04:05 -0300</pubDate></item>
<item><title>Sem data</title><link>https://news.example/sem-data</link><description>Data de publicação desconhecida.</description></item>
</channel></rss>`)
	before := time.Now()
	result, err := c.FetchFeed(context.Background(), Source{URL: "https://news.example/feed", Kind: "news"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Documents) != 2 {
		t.Fatalf("documents = %d, want 2", len(result.Documents))
	}
	old := result.Documents[0]
	if old.Title != "Inflação & crédito" || old.Summary != "Decisão sobre crédito & inflação." {
		t.Fatalf("plain text = %q / %q", old.Title, old.Summary)
	}
	if old.PublishedAt == nil || old.PublishedAt.Format(time.RFC3339) != "2006-01-02T15:04:05-03:00" {
		t.Fatalf("publication date = %v", old.PublishedAt)
	}
	if result.Documents[1].PublishedAt != nil {
		t.Fatal("missing publication date was invented")
	}
	if old.Kind != "news" || old.CollectedAt.Before(before) || old.Hash == "" || !utf8.ValidString(old.Summary) {
		t.Fatalf("incomplete evidence: %+v", old)
	}
}

func TestFetchFeedStripsEncodedMarkupAndComments(t *testing.T) {
	c := fixtureClient(`<rss><channel><item><title>Informação</title><link>https://news.example/info</link><description><![CDATA[&lt;p&gt;Informação útil.&lt;/p&gt;&lt;script&gt;malicious()&lt;/script&gt;<!-- comentário oculto -->]]></description></item></channel></rss>`)
	result, err := c.FetchFeed(context.Background(), Source{URL: "https://news.example/feed"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Documents) != 1 || result.Documents[0].Summary != "Informação útil." {
		t.Fatalf("encoded markup survived: %+v", result.Documents)
	}
}

func TestFetchFeedRejectsInvalidXMLAndEmptyEvidence(t *testing.T) {
	for _, body := range []string{`<rss/>`, `<rss><channel/></rss><extra/>`, `<rss><channel/></rss>trailing garbage`, `<rss><channel><item><title>No URL</title></item></channel></rss>`, `<rss><channel><item><link>https://news.example/empty</link></item></channel></rss>`} {
		t.Run(body, func(t *testing.T) {
			if _, err := fixtureClient(body).FetchFeed(context.Background(), Source{URL: "https://news.example/feed"}, "", ""); err == nil {
				t.Fatal("invalid or empty evidence accepted")
			}
		})
	}
}

func TestFetchFeedPreservesValidatorsWhen304OmitsThem(t *testing.T) {
	c := fixtureClient("")
	c.httpClient.Transport = responseTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 304, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	result, err := c.FetchFeed(context.Background(), Source{URL: "https://news.example/feed"}, `"prior"`, "Mon, 02 Jan 2006 15:04:05 GMT")
	if err != nil {
		t.Fatal(err)
	}
	if !result.NotModified || result.ETag != `"prior"` || result.LastModified != "Mon, 02 Jan 2006 15:04:05 GMT" {
		t.Fatalf("cache validators lost: %+v", result)
	}
}

func TestFetchFeedResolvesLinksAgainstFinalRedirectLocation(t *testing.T) {
	c := fixtureClient("")
	c.httpClient.Transport = responseTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/old/feed" {
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"/new/feed"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`<rss><channel><item><title>Relative</title><link>article</link><description>Body</description></item></channel></rss>`)), Request: r}, nil
	})
	result, err := c.FetchFeed(context.Background(), Source{URL: "https://news.example/old/feed"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Documents) != 1 || result.Documents[0].URL != "https://news.example/new/article" {
		t.Fatalf("wrong relative URL: %+v", result.Documents)
	}
}
