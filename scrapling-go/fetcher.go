package scrapling

import (
	"net/http"
	"time"
)

// FetchOptions configures a fetch request.
type FetchOptions struct {
	// Headers are additional HTTP headers to send.
	Headers map[string]string
	// Cookies are additional cookies to attach.
	Cookies []*http.Cookie
	// Timeout for the HTTP request (default: 30s).
	Timeout time.Duration
	// Proxy URL (e.g. "http://user:pass@host:port").
	Proxy string
	// FollowRedirects controls whether redirects are followed (default: true).
	FollowRedirects bool
	// Retries is the number of retry attempts on failure (default: 0).
	Retries int
}

func defaultFetchOptions() FetchOptions {
	return FetchOptions{
		Timeout:         30 * time.Second,
		FollowRedirects: true,
	}
}

// Fetcher is the common interface for all scrapling fetchers.
type Fetcher interface {
	// Fetch retrieves the page at url and returns an Adaptor for parsing.
	Fetch(url string, opts ...FetchOptions) (*Adaptor, error)
}
