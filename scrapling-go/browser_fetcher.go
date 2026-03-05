package scrapling

import (
	"context"
	"fmt"
	"strings"

	"github.com/chromedp/chromedp"
)

// BrowserFetchOptions extends FetchOptions with browser-specific settings.
type BrowserFetchOptions struct {
	FetchOptions
	// WaitSelector waits until the CSS selector is visible before returning.
	WaitSelector string
	// Headless runs the browser in headless mode (default: true).
	Headless bool
	// DisableImages disables image loading to speed up requests.
	DisableImages bool
}

// BrowserFetcher fetches pages using a real Chromium browser via chromedp.
// It executes JavaScript and handles dynamic pages. It is the Go equivalent
// of Python scrapling's PlaywrightFetcher / StealthyFetcher (browser mode).
//
//	fetcher := scrapling.NewBrowserFetcher()
//	page, err := fetcher.Fetch("https://example.com", scrapling.BrowserFetchOptions{
//	    WaitSelector: "#content",
//	})
type BrowserFetcher struct {
	// UserAgent overrides the browser's default User-Agent.
	UserAgent string
}

// NewBrowserFetcher creates a BrowserFetcher with sensible defaults.
func NewBrowserFetcher() *BrowserFetcher {
	return &BrowserFetcher{}
}

// Fetch retrieves the page at url using a headless Chromium browser.
func (f *BrowserFetcher) Fetch(rawURL string, opts ...BrowserFetchOptions) (*Adaptor, error) {
	opt := BrowserFetchOptions{
		FetchOptions: defaultFetchOptions(),
		Headless:     true,
	}
	if len(opts) > 0 {
		opt = opts[0]
	}

	allocOpts := chromedp.DefaultExecAllocatorOptions[:]
	if !opt.Headless {
		allocOpts = append(allocOpts, chromedp.Flag("headless", false))
	}
	if f.UserAgent != "" {
		allocOpts = append(allocOpts, chromedp.UserAgent(f.UserAgent))
	}
	if opt.Proxy != "" {
		allocOpts = append(allocOpts, chromedp.ProxyServer(opt.Proxy))
	}
	if opt.DisableImages {
		allocOpts = append(allocOpts,
			chromedp.Flag("blink-settings", "imagesEnabled=false"),
		)
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	defer cancelAlloc()

	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()

	if opt.Timeout > 0 {
		var cancelTimeout context.CancelFunc
		ctx, cancelTimeout = context.WithTimeout(ctx, opt.Timeout)
		defer cancelTimeout()
	}

	// Set extra headers (cookies, custom headers) via CDP.
	extraHeaders := make(map[string]interface{}, len(opt.Headers)+1)
	for k, v := range opt.Headers {
		extraHeaders[k] = v
	}
	if len(opt.Cookies) > 0 {
		var cookieParts []string
		for _, c := range opt.Cookies {
			cookieParts = append(cookieParts, c.Name+"="+c.Value)
		}
		extraHeaders["Cookie"] = strings.Join(cookieParts, "; ")
	}

	var rawHTML string
	var statusCode int64 = 200
	var respHeaders map[string]string

	tasks := chromedp.Tasks{
		chromedp.Navigate(rawURL),
	}

	if opt.WaitSelector != "" {
		tasks = append(tasks, chromedp.WaitVisible(opt.WaitSelector, chromedp.ByQuery))
	}

	tasks = append(tasks,
		chromedp.OuterHTML("html", &rawHTML),
	)

	if err := chromedp.Run(ctx, tasks...); err != nil {
		return nil, fmt.Errorf("browser fetcher: chromedp run: %w", err)
	}

	if respHeaders == nil {
		respHeaders = make(map[string]string)
	}

	return newAdaptorFromString(rawHTML, rawURL, int(statusCode), respHeaders)
}
