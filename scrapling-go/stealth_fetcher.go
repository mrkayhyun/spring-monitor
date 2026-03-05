package scrapling

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
)

// TLSProfile selects which browser's TLS fingerprint to impersonate.
type TLSProfile int

const (
	// ProfileChrome impersonates Google Chrome (default).
	ProfileChrome TLSProfile = iota
	// ProfileFirefox impersonates Mozilla Firefox.
	ProfileFirefox
	// ProfileSafari impersonates Apple Safari.
	ProfileSafari
	// ProfileEdge impersonates Microsoft Edge.
	ProfileEdge
	// ProfileRandomized uses a randomised fingerprint to avoid pattern detection.
	ProfileRandomized
)

// StealthyFetcher fetches pages using uTLS to spoof the TLS fingerprint,
// making requests appear to originate from a real browser. This is the Go
// equivalent of Python scrapling's curl-cffi-based fetcher and can bypass
// TLS-fingerprint-based bot detection (e.g. Cloudflare, Akamai).
//
//	fetcher := scrapling.NewStealthyFetcher()
//	page, err := fetcher.Fetch("https://example.com")
type StealthyFetcher struct {
	// Profile selects the TLS fingerprint profile (default: ProfileChrome).
	Profile TLSProfile
	// UserAgent overrides the User-Agent header.
	UserAgent string
}

// NewStealthyFetcher creates a StealthyFetcher with Chrome fingerprint defaults.
func NewStealthyFetcher() *StealthyFetcher {
	return &StealthyFetcher{
		Profile:   ProfileChrome,
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	}
}

// Fetch retrieves the page at url with a spoofed TLS fingerprint.
func (f *StealthyFetcher) Fetch(rawURL string, opts ...FetchOptions) (*Adaptor, error) {
	opt := defaultFetchOptions()
	if len(opts) > 0 {
		opt = opts[0]
	}

	transport := &http.Transport{
		DialTLSContext: f.dialTLS,
	}

	client := &http.Client{
		Timeout:   opt.Timeout,
		Transport: transport,
	}
	if !opt.FollowRedirects {
		client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("stealth fetcher: build request: %w", err)
	}

	ua := f.UserAgent
	if ua == "" {
		ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")

	for k, v := range opt.Headers {
		req.Header.Set(k, v)
	}
	for _, c := range opt.Cookies {
		req.AddCookie(c)
	}

	var resp *http.Response
	for attempt := 0; attempt <= opt.Retries; attempt++ {
		resp, err = client.Do(req)
		if err == nil {
			break
		}
		if attempt < opt.Retries {
			time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("stealth fetcher: HTTP request: %w", err)
	}
	defer resp.Body.Close()

	headers := make(map[string]string, len(resp.Header))
	for k := range resp.Header {
		headers[strings.ToLower(k)] = resp.Header.Get(k)
	}

	return newAdaptor(resp.Body, rawURL, resp.StatusCode, headers)
}

// dialTLS dials a TLS connection using the configured uTLS fingerprint profile.
func (f *StealthyFetcher) dialTLS(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("stealth fetcher: split host port: %w", err)
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}

	tlsConn := utls.UClient(conn, &utls.Config{
		ServerName:         host,
		InsecureSkipVerify: false, //nolint:gosec // users choose to accept risk via option
		MinVersion:         tls.VersionTLS12,
	}, f.helloID())

	if err := tlsConn.Handshake(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("stealth fetcher: TLS handshake: %w", err)
	}
	return tlsConn, nil
}

// helloID maps a TLSProfile to a uTLS ClientHelloID.
func (f *StealthyFetcher) helloID() utls.ClientHelloID {
	switch f.Profile {
	case ProfileFirefox:
		return utls.HelloFirefox_Auto
	case ProfileSafari:
		return utls.HelloSafari_Auto
	case ProfileEdge:
		return utls.HelloEdge_Auto
	case ProfileRandomized:
		return utls.HelloRandomized
	default:
		return utls.HelloChrome_Auto
	}
}
