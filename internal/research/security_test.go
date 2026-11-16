package research

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicRequestsRejectUnsafeURLsBeforeTransport(t *testing.T) {
	for _, rawURL := range []string{
		"http://news.example/feed", "https://user:secret@news.example/feed", "https://news.example/feed#fragment",
		"https://news.example:8443/feed", "https://news.example.evil/feed", "https://evilnews.example/feed",
		"https://127.0.0.1/feed", "https://8.8.8.8/feed", "https://[::1]/feed", "//news.example/feed",
		"https://localhost/feed", "https://news.example./feed", "https://news.example/feed#",
	} {
		t.Run(rawURL, func(t *testing.T) {
			c := fixtureClient(`<rss><channel/></rss>`)
			called := false
			c.httpClient.Transport = responseTransport(func(r *http.Request) (*http.Response, error) {
				called = true
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`<rss><channel/></rss>`)), Request: r}, nil
			})
			if _, err := c.FetchFeed(context.Background(), Source{URL: rawURL}, "", ""); !errors.Is(err, ErrBlockedDestination) {
				t.Fatalf("feed error = %v, want blocked destination", err)
			}
			if _, err := c.VerifyDocument(context.Background(), rawURL); !errors.Is(err, ErrBlockedDestination) {
				t.Fatalf("document error = %v, want blocked destination", err)
			}
			if called {
				t.Fatal("unsafe request reached transport")
			}
		})
	}
}

func TestPublicTransportUsesCheckedIPAndBoundsResponseHeaders(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/large-headers" {
			w.Header().Set("X-Large", strings.Repeat("x", 65<<10))
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		io.WriteString(w, `<rss><channel><item><title>Public article</title><link>https://example.com/article</link><description>Public content</description></item></channel></rss>`)
	}))
	defer server.Close()
	c := NewClient([]string{"example.com"})
	c.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	c.dialIP = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "8.8.8.8:443" {
			t.Errorf("unchecked address = %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	c.httpClient.Transport.(*http.Transport).TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	result, err := c.FetchFeed(context.Background(), Source{URL: "https://example.com/feed"}, "", "")
	if err != nil || len(result.Documents) != 1 {
		t.Fatalf("real HTTPS collection = %+v, %v", result, err)
	}
	if _, err := c.FetchFeed(context.Background(), Source{URL: "https://example.com/large-headers"}, "", ""); err == nil {
		t.Fatal("oversized response headers were accepted")
	}
}

func TestPublicRequestErrorsDoNotExposeURLQueryValues(t *testing.T) {
	c := fixtureClient("")
	failure := errors.New("network unavailable")
	c.httpClient.Transport = responseTransport(func(*http.Request) (*http.Response, error) { return nil, failure })
	_, feedError := c.FetchFeed(context.Background(), Source{URL: "https://news.example/feed?token=secret-value"}, "", "")
	_, documentError := c.VerifyDocument(context.Background(), "https://news.example/article?token=secret-value")
	for _, err := range []error{feedError, documentError} {
		if !errors.Is(err, failure) || strings.Contains(err.Error(), "secret-value") {
			t.Fatalf("unsafe request error = %v", err)
		}
	}
}

func TestPublicRequestsRequireExplicitAllowlist(t *testing.T) {
	c := NewClient(nil)
	c.httpClient.Transport = fixtureClient(`<rss><channel/></rss>`).httpClient.Transport
	if _, err := c.FetchFeed(context.Background(), Source{URL: "https://news.example/feed"}, "", ""); !errors.Is(err, ErrBlockedDestination) {
		t.Fatalf("unconfigured domain error = %v", err)
	}
}

func TestFetchFeedRejectsUnsafeDocumentLinks(t *testing.T) {
	c := fixtureClient(`<rss><channel><item><title>Unsafe</title><link>http://127.0.0.1/admin</link><description>Do not fetch</description></item></channel></rss>`)
	if _, err := c.FetchFeed(context.Background(), Source{URL: "https://news.example/feed"}, "", ""); !errors.Is(err, ErrBlockedDestination) {
		t.Fatalf("unsafe feed document error = %v", err)
	}
}

func TestPublicRequestsValidateEveryRedirect(t *testing.T) {
	for _, target := range []string{"http://news.example/insecure", "https://127.0.0.1/admin", "https://news.example.evil/feed", "https://user:pass@news.example/feed", "https://news.example/feed#hidden"} {
		t.Run(target, func(t *testing.T) {
			c := fixtureClient("")
			calls := 0
			c.httpClient.Transport = responseTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return &http.Response{StatusCode: 302, Header: http.Header{"Location": {target}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`<rss><channel/></rss>`)), Request: r}, nil
			})
			if _, err := c.FetchFeed(context.Background(), Source{URL: "https://news.example/feed"}, "", ""); !errors.Is(err, ErrBlockedDestination) {
				t.Fatalf("redirect error = %v", err)
			}
			if calls != 1 {
				t.Fatalf("unsafe redirect was requested: %d calls", calls)
			}
		})
	}
}

func TestDNSRejectsInternalAddressesIncludingMixedAnswers(t *testing.T) {
	for _, rawIP := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.0.1", "169.254.169.254", "100.100.100.200", "0.0.0.0", "224.0.0.1", "198.18.0.1", "192.0.2.1", "::1", "::", "fe80::1", "fd00::1", "::ffff:127.0.0.1", "64:ff9b::a00:1", "2002:a00:1::1"} {
		t.Run(rawIP, func(t *testing.T) {
			c := NewClient([]string{"news.example"})
			c.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP(rawIP)}}, nil
			}
			c.dialIP = func(context.Context, string, string) (net.Conn, error) {
				t.Fatal("internal DNS answer reached dialer")
				return nil, nil
			}
			if _, err := c.dialContext(context.Background(), "tcp", "news.example:443"); !errors.Is(err, ErrBlockedDestination) {
				t.Fatalf("DNS error = %v, want blocked destination", err)
			}
		})
	}
}

func TestDNSDialsValidatedIPWithoutSecondLookup(t *testing.T) {
	c := NewClient([]string{"news.example"})
	lookups := 0
	c.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		lookups++
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	c.dialIP = func(_ context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != "8.8.8.8:443" {
			t.Fatalf("dial bypassed validated IP: %s %s", network, address)
		}
		left, right := net.Pipe()
		right.Close()
		return left, nil
	}
	conn, err := c.dialContext(context.Background(), "tcp", "news.example:443")
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if lookups != 1 {
		t.Fatalf("DNS lookups = %d, want 1", lookups)
	}
}
