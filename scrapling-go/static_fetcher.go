package scrapling

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// StaticFetcher fetches pages using the standard Go HTTP client.
// It does not execute JavaScript and is suitable for static HTML pages.
//
//	fetcher := scrapling.NewStaticFetcher()
//	page, err := fetcher.Fetch("https://example.com")
type StaticFetcher struct {
	// UserAgent overrides the default User-Agent header.
	UserAgent string
}

// NewStaticFetcher creates a StaticFetcher with sensible defaults.
func NewStaticFetcher() *StaticFetcher {
	return &StaticFetcher{
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	}
}

// Fetch retrieves the page at url using the standard HTTP client.
func (f *StaticFetcher) Fetch(rawURL string, opts ...FetchOptions) (*Adaptor, error) {
	opt := defaultFetchOptions()
	if len(opts) > 0 {
		opt = opts[0]
	}

	transport := &http.Transport{}
	if opt.Proxy != "" {
		proxyURL, err := url.Parse(opt.Proxy)
		if err != nil {
			return nil, fmt.Errorf("static fetcher: invalid proxy URL: %w", err)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
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
		return nil, fmt.Errorf("static fetcher: build request: %w", err)
	}

	ua := f.UserAgent
	if ua == "" {
		ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")

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
		return nil, fmt.Errorf("static fetcher: HTTP request: %w", err)
	}
	defer resp.Body.Close()

	headers := make(map[string]string, len(resp.Header))
	for k := range resp.Header {
		headers[strings.ToLower(k)] = resp.Header.Get(k)
	}

	return newAdaptor(resp.Body, rawURL, resp.StatusCode, headers)
}
