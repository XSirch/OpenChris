package research

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func documentClient(body, contentType string) *Client {
	c := fixtureClient(body)
	base := c.httpClient.Transport
	c.httpClient.Transport = responseTransport(func(r *http.Request) (*http.Response, error) {
		response, err := base.RoundTrip(r)
		response.Header.Set("Content-Type", contentType)
		return response, err
	})
	return c
}

const publicArticle = `<html><head><title>Inflação &amp; atividade</title><meta name='description' content='Resumo público da decisão.'><meta property="article:published_time" content="2012-03-05T10:30:00-03:00"></head><body><nav>Menu irrelevante.</nav><article><h1>Inflação e atividade</h1><p>O documento apresenta os dados públicos de atividade econômica e descreve os critérios usados na avaliação. A série histórica continua disponível para consulta, com as limitações indicadas no texto.</p></article><script>malicious()</script></body></html>`

func TestVerifyDocumentExtractsObservedContentAndOldDate(t *testing.T) {
	c := documentClient(publicArticle, "text/html; charset=utf-8")
	before := time.Now()
	document, err := c.VerifyDocument(context.Background(), "https://news.example/article")
	if err != nil {
		t.Fatal(err)
	}
	if document.URL != "https://news.example/article" || document.Title != "Inflação & atividade" || document.Summary != "Resumo público da decisão." {
		t.Fatalf("observed document = %+v", document)
	}
	if document.PublishedAt == nil || document.PublishedAt.Format(time.RFC3339) != "2012-03-05T10:30:00-03:00" {
		t.Fatalf("publication date = %v", document.PublishedAt)
	}
	if document.Hash == "" || document.CollectedAt.Before(before) || document.Kind != "public" {
		t.Fatalf("incomplete observed document = %+v", document)
	}
}

func TestVerifyDocumentKeepsMissingDateUnknownAndHashesContent(t *testing.T) {
	body := `<html><head><title>Texto público</title></head><body><main><p>` + strings.Repeat("A informação pública inclui ressalvas e critérios de interpretação. ", 20) + `</p></main></body></html>`
	c := documentClient(body, "text/html")
	first, err := c.VerifyDocument(context.Background(), "https://news.example/article")
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.VerifyDocument(context.Background(), "https://news.example/article")
	if err != nil {
		t.Fatal(err)
	}
	if first.Hash == "" || first.Hash != second.Hash || first.PublishedAt != nil || len(first.Summary) > 600 || strings.Contains(first.Summary, "<") {
		t.Fatalf("content/hash/date = %+v / %+v", first, second)
	}
	c = documentClient(strings.Replace(body, "</p>", " Mais um dado observado.</p>", 1), "text/html")
	changed, err := c.VerifyDocument(context.Background(), "https://news.example/article")
	if err != nil {
		t.Fatal(err)
	}
	if changed.Hash == first.Hash {
		t.Fatal("changed content beyond the snippet did not create a new hash")
	}
}

func TestVerifyDocumentRejectsUnavailableEvidence(t *testing.T) {
	for _, tc := range []struct{ name, body, contentType string }{
		{"empty", "", "text/html"},
		{"JavaScript shell", `<html><head><title>Fresh news</title><meta name="description" content="A convincing claim from the page metadata."></head><body><div id="app"></div><script>renderArticle()</script><noscript>Enable JavaScript to continue reading the article in this application.</noscript></body></html>`, "text/html"},
		{"metadata only", `<html><head><title>Fresh news</title><meta name="description" content="` + strings.Repeat("Claim without article. ", 20) + `"></head><body></body></html>`, "text/html"},
		{"navigation only", `<html><head><title>News</title></head><body><nav>` + strings.Repeat("Home Latest Business Markets About Contact ", 20) + `</nav><footer>All rights reserved.</footer></body></html>`, "text/html"},
		{"unsupported binary", "%PDF-1.7 binary data", "application/pdf"},
		{"invalid UTF8", "<html><body>" + strings.Repeat("a", 100) + string([]byte{0xff}) + "</body></html>", "text/html; charset=utf-8"},
		{"oversized", strings.Repeat("x", (1<<20)+1), "text/plain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := documentClient(tc.body, tc.contentType).VerifyDocument(context.Background(), "https://news.example/article")
			if !errors.Is(err, ErrEvidenceUnavailable) {
				t.Fatalf("error = %v, want evidence unavailable", err)
			}
		})
	}
}

func TestVerifyDocumentRecordsFinalRedirectURLAndPageDate(t *testing.T) {
	c := fixtureClient("")
	c.httpClient.Transport = responseTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/old" {
			return &http.Response{StatusCode: 301, Header: http.Header{"Location": {"https://www.news.example/final"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		body := `<html><head><title>Final article</title></head><body><article><time itemprop="datePublished" datetime="2010-01-02">2 Jan 2010</time><p>` + strings.Repeat("Observed public article with sufficient content. ", 5) + `</p></article></body></html>`
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	document, err := c.VerifyDocument(context.Background(), "https://news.example/old")
	if err != nil {
		t.Fatal(err)
	}
	if document.URL != "https://www.news.example/final" || document.PublishedAt == nil || document.PublishedAt.Year() != 2010 {
		t.Fatalf("redirect/date = %+v", document)
	}
}

func TestVerifyDocumentDoesNotTreatHTTPFailureAsEvidence(t *testing.T) {
	c := documentClient(publicArticle, "text/html")
	base := c.httpClient.Transport
	c.httpClient.Transport = responseTransport(func(r *http.Request) (*http.Response, error) {
		response, err := base.RoundTrip(r)
		response.StatusCode = 403
		return response, err
	})
	if _, err := c.VerifyDocument(context.Background(), "https://news.example/article"); !errors.Is(err, ErrEvidenceUnavailable) {
		t.Fatalf("HTTP error = %v", err)
	}
}

func TestVerifyDocumentIgnoresDatesOutsideArticleAndNonDataScripts(t *testing.T) {
	body := `<html><head><title>Public article</title><!-- <meta property="article:published_time" content="2026-11-07"> --><script>const date={"datePublished":"2026-11-07"}; const html='<meta property="article:published_time" content="2026-11-07">';</script></head><body><header><time datetime="2026-11-07">Today</time></header><article><p>` + strings.Repeat("Actual observed article without a known publication date. ", 5) + `</p></article></body></html>`
	document, err := documentClient(body, "text/html").VerifyDocument(context.Background(), "https://news.example/article")
	if err != nil {
		t.Fatal(err)
	}
	if document.PublishedAt != nil {
		t.Fatalf("unrelated publication date = %v", document.PublishedAt)
	}
}

func TestVerifyDocumentReadsJSONLDPublicationDate(t *testing.T) {
	body := `<html><head><title>Public article</title><script type="application/ld+json">{"@context":"https://schema.org","@type":"NewsArticle","url":"https://news.example/article","datePublished":"2013-02-01T12:00:00Z","dateModified":"2026-11-07T12:00:00Z"}</script></head><body><article><p>` + strings.Repeat("Actual observed article with a declared publication date. ", 5) + `</p></article></body></html>`
	document, err := documentClient(body, "text/html").VerifyDocument(context.Background(), "https://news.example/article")
	if err != nil {
		t.Fatal(err)
	}
	if document.PublishedAt == nil || document.PublishedAt.Year() != 2013 {
		t.Fatalf("JSON-LD publication date = %v", document.PublishedAt)
	}
}

func TestVerifyDocumentHashTracksEffectiveMetadataSummary(t *testing.T) {
	first, err := documentClient(publicArticle, "text/html").VerifyDocument(context.Background(), "https://news.example/article")
	if err != nil {
		t.Fatal(err)
	}
	changedBody := strings.Replace(publicArticle, "Resumo público da decisão.", "Outro resumo público da decisão.", 1)
	second, err := documentClient(changedBody, "text/html").VerifyDocument(context.Background(), "https://news.example/article")
	if err != nil {
		t.Fatal(err)
	}
	if first.Summary == second.Summary || first.Hash == second.Hash {
		t.Fatalf("changed observed summary kept hash: %+v / %+v", first, second)
	}
}

func TestVerifyDocumentPublicationDateRequiresArticleIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, metadata string
		wantYear       int
	}{
		{"graph distinguishes website from article", `<script type="application/ld+json">{"@graph":[{"@type":"WebSite","url":"https://news.example/article","datePublished":"2026-11-07"},{"@type":"NewsArticle","url":"https://news.example/article","datePublished":"2010-01-02"}]}</script>`, 2010},
		{"article URL absent", `<script type="application/ld+json">{"@type":"NewsArticle","datePublished":"2026-11-07"}</script>`, 0},
		{"other article URL", `<script type="application/ld+json">{"@type":"NewsArticle","url":"https://news.example/different","datePublished":"2026-11-07"}</script>`, 0},
		{"website only", `<script type="application/ld+json">{"@type":"WebSite","url":"https://news.example/article","datePublished":"2026-11-07"}</script>`, 0},
		{"unmarked time", `<time datetime="2026-11-07">Observation date</time>`, 0},
		{"generic date metadata", `<meta name="date" content="2026-11-07">`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `<html><head><title>Public article</title></head><body><article>` + tc.metadata + `<p>` + strings.Repeat("Observed public article whose publication date needs specific evidence. ", 5) + `</p></article></body></html>`
			document, err := documentClient(body, "text/html").VerifyDocument(context.Background(), "https://news.example/article")
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantYear == 0 && document.PublishedAt != nil || tc.wantYear != 0 && (document.PublishedAt == nil || document.PublishedAt.Year() != tc.wantYear) {
				t.Fatalf("publication date=%v wantYear=%d", document.PublishedAt, tc.wantYear)
			}
		})
	}
}
