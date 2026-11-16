package research

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func cvmFixtureClient(link string, domains []string) *Client {
	c := NewClient(domains)
	c.httpClient.Transport = responseTransport(func(request *http.Request) (*http.Response, error) {
		body := `<rss><channel><item><title>Documento oficial</title><link>` + link + `</link><description>Resumo público.</description></item></channel></rss>`
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	return c
}

func TestCVMOfficialFeedsUpgradeOnlyApprovedLegacyLinksToHTTPS(t *testing.T) {
	for _, tc := range []struct{ feed, legacy, want string }{
		{"https://conteudo.cvm.gov.br/feed/decisoes.xml", "http://www.cvm.gov.br/decisoes/2026/20261103_R1.html", "https://www.cvm.gov.br/decisoes/2026/20261103_R1.html"},
		{"https://conteudo.cvm.gov.br/feed/legislacao.xml", "http://www.cvm.gov.br:80/legislacao/resolucoes/resol249.html", "https://www.cvm.gov.br/legislacao/resolucoes/resol249.html"},
	} {
		t.Run(tc.feed, func(t *testing.T) {
			c := cvmFixtureClient(tc.legacy, []string{"conteudo.cvm.gov.br", "www.cvm.gov.br"})
			base := c.httpClient.Transport
			c.httpClient.Transport = responseTransport(func(request *http.Request) (*http.Response, error) {
				if request.URL.Scheme != "https" {
					t.Fatalf("plaintext HTTP was requested: %s", request.URL.Scheme)
				}
				return base.RoundTrip(request)
			})
			result, err := c.FetchFeed(context.Background(), Source{URL: tc.feed, Kind: "primary"}, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Documents) != 1 || result.Documents[0].URL != tc.want {
				t.Fatalf("legacy normalization = %+v", result.Documents)
			}
		})
	}
}

func TestCVMLegacyExceptionRejectsOtherSourcesAndUnsafeLinks(t *testing.T) {
	approved := "https://conteudo.cvm.gov.br/feed/decisoes.xml"
	for _, tc := range []struct{ name, feed, link string }{
		{"other feed host", "https://news.example/feed", "http://www.cvm.gov.br/decisoes/2026/article.html"},
		{"other CVM feed", "https://conteudo.cvm.gov.br/feed/other.xml", "http://www.cvm.gov.br/decisoes/2026/article.html"},
		{"feed with query", approved + "?other", "http://www.cvm.gov.br/decisoes/2026/article.html"},
		{"other allowlisted host", approved, "http://news.example/decisoes/2026/article.html"},
		{"credentials", approved, "http://user:pass@www.cvm.gov.br/decisoes/2026/article.html"},
		{"fragment", approved, "http://www.cvm.gov.br/decisoes/2026/article.html#section"},
		{"empty fragment", approved, "http://www.cvm.gov.br/decisoes/2026/article.html#"},
		{"custom port", approved, "http://www.cvm.gov.br:8080/decisoes/2026/article.html"},
		{"HTTPS port in HTTP URL", approved, "http://www.cvm.gov.br:443/decisoes/2026/article.html"},
		{"other path", approved, "http://www.cvm.gov.br/admin/article.html"},
		{"traversal", approved, "http://www.cvm.gov.br/decisoes/../admin.html"},
		{"encoded traversal", approved, "http://www.cvm.gov.br/decisoes/%2e%2e/admin.html"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := cvmFixtureClient(tc.link, []string{"conteudo.cvm.gov.br", "www.cvm.gov.br", "news.example"})
			if _, err := c.FetchFeed(context.Background(), Source{URL: tc.feed}, "", ""); !errors.Is(err, ErrBlockedDestination) {
				t.Fatalf("unsafe legacy URL error = %v", err)
			}
		})
	}
}

func TestCVMLegacyUpgradeDoesNotBypassAllowlistOrStandaloneHTTPS(t *testing.T) {
	link := "http://www.cvm.gov.br/decisoes/2026/article.html"
	c := cvmFixtureClient(link, []string{"conteudo.cvm.gov.br"})
	if _, err := c.FetchFeed(context.Background(), Source{URL: "https://conteudo.cvm.gov.br/feed/decisoes.xml"}, "", ""); !errors.Is(err, ErrBlockedDestination) {
		t.Fatalf("unlisted upgraded host error = %v", err)
	}
	c = NewClient([]string{"www.cvm.gov.br"})
	if _, err := c.VerifyDocument(context.Background(), link); !errors.Is(err, ErrBlockedDestination) {
		t.Fatalf("standalone HTTP document error = %v", err)
	}
}

func TestCVMLegacyUpgradeUsesOriginalSourceBeforeRedirect(t *testing.T) {
	c := NewClient([]string{"news.example", "conteudo.cvm.gov.br", "www.cvm.gov.br"})
	c.httpClient.Transport = responseTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "news.example" {
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://conteudo.cvm.gov.br/feed/decisoes.xml"}}, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
		}
		return cvmFixtureClient("http://www.cvm.gov.br/decisoes/2026/article.html", nil).httpClient.Transport.RoundTrip(request)
	})
	if _, err := c.FetchFeed(context.Background(), Source{URL: "https://news.example/feed"}, "", ""); !errors.Is(err, ErrBlockedDestination) {
		t.Fatalf("redirect granted CVM exception = %v", err)
	}
}
