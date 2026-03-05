package scrapling

import (
	"fmt"
	"sync"
)

// SpiderRequest represents a URL to be crawled, optionally with metadata.
type SpiderRequest struct {
	URL      string
	Meta     map[string]interface{}
	Callback func(*Adaptor, *SpiderRequest) ([]SpiderRequest, []interface{}, error)
}

// Spider is a Scrapy-style concurrent web crawler.
// Define ParseFunc to handle each fetched page and yield new requests or items.
//
//	spider := scrapling.NewSpider(scrapling.NewStaticFetcher())
//	spider.Start("https://example.com", func(page *scrapling.Adaptor, req *scrapling.SpiderRequest) ([]scrapling.SpiderRequest, []interface{}, error) {
//	    title := page.Title()
//	    fmt.Println(title)
//	    return nil, []interface{}{title}, nil
//	})
type Spider struct {
	fetcher     Fetcher
	concurrency int
	visited     map[string]bool
	mu          sync.Mutex
}

// NewSpider creates a Spider using the given Fetcher with a default concurrency of 5.
func NewSpider(fetcher Fetcher) *Spider {
	return &Spider{
		fetcher:     fetcher,
		concurrency: 5,
		visited:     make(map[string]bool),
	}
}

// WithConcurrency sets the number of concurrent requests.
func (s *Spider) WithConcurrency(n int) *Spider {
	s.concurrency = n
	return s
}

// Start begins crawling from startURL using parseFunc as the page handler.
// It blocks until all reachable pages have been processed or an error occurs.
func (s *Spider) Start(startURL string, parseFunc func(*Adaptor, *SpiderRequest) ([]SpiderRequest, []interface{}, error)) ([]interface{}, error) {
	initial := SpiderRequest{URL: startURL, Callback: parseFunc}
	queue := make(chan SpiderRequest, 1024)
	results := make(chan interface{}, 1024)
	errs := make(chan error, 64)

	var wg sync.WaitGroup
	sem := make(chan struct{}, s.concurrency)

	queue <- initial

	var allItems []interface{}
	done := make(chan struct{})

	go func() {
		for item := range results {
			allItems = append(allItems, item)
		}
		close(done)
	}()

	go func() {
		for req := range queue {
			s.mu.Lock()
			if s.visited[req.URL] {
				s.mu.Unlock()
				continue
			}
			s.visited[req.URL] = true
			s.mu.Unlock()

			wg.Add(1)
			sem <- struct{}{}

			go func(r SpiderRequest) {
				defer wg.Done()
				defer func() { <-sem }()

				page, err := s.fetcher.Fetch(r.URL)
				if err != nil {
					errs <- fmt.Errorf("spider: fetch %s: %w", r.URL, err)
					return
				}

				cb := r.Callback
				if cb == nil {
					return
				}

				newReqs, items, err := cb(page, &r)
				if err != nil {
					errs <- fmt.Errorf("spider: parse %s: %w", r.URL, err)
					return
				}

				for _, item := range items {
					results <- item
				}
				for _, next := range newReqs {
					if next.Callback == nil {
						next.Callback = cb
					}
					queue <- next
				}
			}(req)
		}
	}()

	wg.Wait()
	close(results)
	<-done

	var firstErr error
	select {
	case firstErr = <-errs:
	default:
	}

	return allItems, firstErr
}
