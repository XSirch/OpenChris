package research

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

var ErrBlockedDestination = errors.New("public destination blocked")

// Client fetches public evidence through an explicit domain allowlist.
// Resolver and dialer hooks are private; production callers cannot bypass IP checks.
type Client struct {
	httpClient *http.Client
	domains    []string
	lookupIP   func(context.Context, string) ([]net.IPAddr, error)
	dialIP     func(context.Context, string, string) (net.Conn, error)
}

func NewClient(domains []string) *Client {
	c := &Client{lookupIP: net.DefaultResolver.LookupIPAddr, dialIP: (&net.Dialer{Timeout: 8 * time.Second}).DialContext}
	for _, domain := range domains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if validDomain(domain) {
			c.domains = append(c.domains, domain)
		}
	}
	c.httpClient = &http.Client{
		Timeout:   12 * time.Second,
		Transport: &http.Transport{Proxy: nil, DialContext: c.dialContext, TLSHandshakeTimeout: 6 * time.Second, ResponseHeaderTimeout: 10 * time.Second, MaxResponseHeaderBytes: 64 << 10, IdleConnTimeout: 30 * time.Second, MaxIdleConns: 8, ForceAttemptHTTP2: true},
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) > 5 {
				return errors.New("public redirect limit exceeded")
			}
			_, err := c.validateURL(request.URL.String())
			return err
		},
	}
	return c
}

func publicRequestError(err error) error {
	var urlError *url.Error
	if errors.As(err, &urlError) {
		err = urlError.Err
	}
	return fmt.Errorf("public request failed: %w", err)
}

func validDomain(domain string) bool {
	if len(domain) > 253 || net.ParseIP(domain) != nil {
		return false
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-') {
				return false
			}
		}
	}
	return true
}

func (c *Client) validateURL(rawURL string) (*url.URL, error) {
	if strings.Contains(rawURL, "#") || strings.TrimSpace(rawURL) != rawURL || len(rawURL) > 4096 {
		return nil, ErrBlockedDestination
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Fragment != "" || u.Host == "" {
		return nil, ErrBlockedDestination
	}
	host := strings.ToLower(u.Hostname())
	if !validDomain(host) || u.Port() != "" && u.Port() != "443" {
		return nil, ErrBlockedDestination
	}
	for _, domain := range c.domains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return u, nil
		}
	}
	return nil, ErrBlockedDestination
}

func (c *Client) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if _, err := c.validateURL("https://" + address); err != nil {
		return nil, err
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || port != "443" {
		return nil, ErrBlockedDestination
	}
	addresses, err := c.lookupIP(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("public DNS lookup failed: %w", err)
	}
	if len(addresses) == 0 {
		return nil, ErrBlockedDestination
	}
	// Check the entire answer before dialing, then pin the connection to a checked IP.
	for _, address := range addresses {
		if address.Zone != "" || !publicIP(address.IP) {
			return nil, ErrBlockedDestination
		}
	}
	var dialErr error
	for _, address := range addresses {
		conn, err := c.dialIP(ctx, network, net.JoinHostPort(address.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		dialErr = err
	}
	return nil, fmt.Errorf("public connection failed: %w", dialErr)
}

var nonPublicRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/32"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
}

func publicIP(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() {
		return false
	}
	if address.Is6() && !netip.MustParsePrefix("2000::/3").Contains(address) {
		return false
	}
	for _, prefix := range nonPublicRanges {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}
