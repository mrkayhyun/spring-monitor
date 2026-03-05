package main

import (
	"fmt"
	"log"

	scrapling "github.com/mrkayhyun/scrapling-go"
)

func main() {
	// ── StaticFetcher ──────────────────────────────────────────────────────────
	fmt.Println("=== StaticFetcher ===")
	static := scrapling.NewStaticFetcher()
	page, err := static.Fetch("https://books.toscrape.com/")
	if err != nil {
		log.Fatalf("static fetch: %v", err)
	}
	fmt.Println("Title:", page.Title())
	for _, el := range page.CSS("article.product_pod h3 a") {
		fmt.Println(" •", el.Attr("title"))
	}

	// ── StealthyFetcher (curl-cffi equivalent, Chrome fingerprint) ─────────────
	fmt.Println("\n=== StealthyFetcher (Chrome TLS fingerprint) ===")
	stealth := scrapling.NewStealthyFetcher()
	stealth.Profile = scrapling.ProfileChrome
	page2, err := stealth.Fetch("https://httpbin.org/headers")
	if err != nil {
		log.Fatalf("stealth fetch: %v", err)
	}
	fmt.Println("Status:", page2.StatusCode())
	fmt.Println("Body snippet:", page2.RawHTML()[:200])

	// ── BrowserFetcher (headless Chromium) ─────────────────────────────────────
	fmt.Println("\n=== BrowserFetcher (headless Chromium) ===")
	browser := scrapling.NewBrowserFetcher()
	page3, err := browser.Fetch("https://books.toscrape.com/", scrapling.BrowserFetchOptions{
		WaitSelector: "article.product_pod",
	})
	if err != nil {
		log.Fatalf("browser fetch: %v", err)
	}
	fmt.Println("Title:", page3.Title())
}
