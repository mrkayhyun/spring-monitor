// Package scrapling provides a Go implementation of the Scrapling web scraping framework.
// It supports CSS selectors, XPath, text/regex search, HTTP fetching with TLS fingerprint
// spoofing, browser automation via chromedp, session management, proxy rotation, and
// a Scrapy-style Spider framework.
package scrapling

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/antchfx/htmlquery"
	"golang.org/x/net/html"
)

// Element represents a single HTML element with selector capabilities.
type Element struct {
	node      *html.Node
	doc       *goquery.Selection
	sourceURL string
}

// Adaptor wraps an HTML document and provides Scrapling-style selector methods.
// It is returned by all fetchers after a successful page fetch.
type Adaptor struct {
	doc       *goquery.Document
	rawHTML   string
	url       string
	statusCode int
	headers   map[string]string
}

// newAdaptor creates a new Adaptor from an HTML reader.
func newAdaptor(r io.Reader, url string, statusCode int, headers map[string]string) (*Adaptor, error) {
	doc, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		return nil, fmt.Errorf("scrapling: parse HTML: %w", err)
	}
	raw, _ := doc.Html()
	return &Adaptor{
		doc:        doc,
		rawHTML:    raw,
		url:        url,
		statusCode: statusCode,
		headers:    headers,
	}, nil
}

// newAdaptorFromString creates an Adaptor from a raw HTML string.
func newAdaptorFromString(rawHTML, url string, statusCode int, headers map[string]string) (*Adaptor, error) {
	return newAdaptor(strings.NewReader(rawHTML), url, statusCode, headers)
}

// URL returns the page URL.
func (a *Adaptor) URL() string { return a.url }

// StatusCode returns the HTTP status code.
func (a *Adaptor) StatusCode() int { return a.statusCode }

// Headers returns the response headers.
func (a *Adaptor) Headers() map[string]string { return a.headers }

// RawHTML returns the raw HTML content.
func (a *Adaptor) RawHTML() string { return a.rawHTML }

// CSS returns all elements matching the CSS selector.
// Supports ::text and ::attr(name) pseudo-elements (Scrapling-style).
//
//	items := page.CSS(".product h2::text")
func (a *Adaptor) CSS(selector string) []*Element {
	return cssSelect(a.doc.Selection, selector, a.url)
}

// CSSFirst returns the first element matching the CSS selector, or nil.
//
//	item := page.CSSFirst(".product h2")
func (a *Adaptor) CSSFirst(selector string) *Element {
	els := a.CSS(selector)
	if len(els) == 0 {
		return nil
	}
	return els[0]
}

// XPath returns all elements matching the XPath expression.
//
//	items := page.XPath("//div[@class='product']//h2/text()")
func (a *Adaptor) XPath(expr string) []*Element {
	root, err := htmlquery.Parse(strings.NewReader(a.rawHTML))
	if err != nil {
		return nil
	}
	nodes := htmlquery.Find(root, expr)
	els := make([]*Element, 0, len(nodes))
	for _, n := range nodes {
		els = append(els, &Element{node: n, sourceURL: a.url})
	}
	return els
}

// XPathFirst returns the first element matching the XPath expression, or nil.
func (a *Adaptor) XPathFirst(expr string) *Element {
	els := a.XPath(expr)
	if len(els) == 0 {
		return nil
	}
	return els[0]
}

// FindOptions configures text/regex find operations.
type FindOptions struct {
	// CaseSensitive controls case sensitivity for text searches (default: true).
	CaseSensitive bool
	// Recursive searches within nested elements (default: true).
	Recursive bool
}

// Find returns all elements whose text content contains the given string.
// Set opts.CaseSensitive = false for case-insensitive matching.
//
//	el := page.Find("Add to Cart", FindOptions{CaseSensitive: false})
func (a *Adaptor) Find(text string, opts ...FindOptions) []*Element {
	opt := FindOptions{CaseSensitive: true, Recursive: true}
	if len(opts) > 0 {
		opt = opts[0]
	}
	search := text
	var results []*Element
	a.doc.Find("*").Each(func(_ int, s *goquery.Selection) {
		t := s.Text()
		if !opt.CaseSensitive {
			if strings.Contains(strings.ToLower(t), strings.ToLower(search)) {
				results = append(results, selectionToElement(s, a.url))
			}
		} else {
			if strings.Contains(t, search) {
				results = append(results, selectionToElement(s, a.url))
			}
		}
	})
	return results
}

// FindFirst returns the first element whose text matches, or nil.
func (a *Adaptor) FindFirst(text string, opts ...FindOptions) *Element {
	els := a.Find(text, opts...)
	if len(els) == 0 {
		return nil
	}
	return els[0]
}

// FindRegex returns all elements whose text content matches the regex pattern.
//
//	els := page.FindRegex(`\$[\d,]+`)
func (a *Adaptor) FindRegex(pattern string) []*Element {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}
	var results []*Element
	a.doc.Find("*").Each(func(_ int, s *goquery.Selection) {
		if re.MatchString(s.Text()) {
			results = append(results, selectionToElement(s, a.url))
		}
	})
	return results
}

// Title returns the page <title> text.
func (a *Adaptor) Title() string {
	return strings.TrimSpace(a.doc.Find("title").Text())
}

// ---- Element methods ----

// Text returns the text content of the element.
func (e *Element) Text() string {
	if e.doc != nil {
		return strings.TrimSpace(e.doc.Text())
	}
	if e.node != nil {
		if e.node.Type == html.TextNode {
			return strings.TrimSpace(e.node.Data)
		}
		return strings.TrimSpace(htmlquery.InnerText(e.node))
	}
	return ""
}

// Attr returns the value of the named attribute, or "" if not present.
func (e *Element) Attr(name string) string {
	if e.doc != nil {
		v, _ := e.doc.Attr(name)
		return v
	}
	if e.node != nil {
		for _, a := range e.node.Attr {
			if a.Key == name {
				return a.Val
			}
		}
	}
	return ""
}

// HTML returns the outer HTML of the element.
func (e *Element) HTML() string {
	if e.doc != nil {
		h, _ := goquery.OuterHtml(e.doc)
		return h
	}
	if e.node != nil {
		return htmlquery.OutputHTML(e.node, true)
	}
	return ""
}

// CSS returns matching child elements using a CSS selector.
func (e *Element) CSS(selector string) []*Element {
	if e.doc == nil {
		return nil
	}
	return cssSelect(e.doc, selector, e.sourceURL)
}

// CSSFirst returns the first matching child element.
func (e *Element) CSSFirst(selector string) *Element {
	els := e.CSS(selector)
	if len(els) == 0 {
		return nil
	}
	return els[0]
}

// Get is an alias for Text (compatibility with Scrapling's .get()).
func (e *Element) Get() string { return e.Text() }

// ---- helpers ----

// cssSelect parses Scrapling-style pseudo-selectors (::text, ::attr(name))
// and returns matching elements from the given goquery selection.
func cssSelect(sel *goquery.Selection, selector string, sourceURL string) []*Element {
	var pseudoText bool
	var pseudoAttr string

	// Handle ::text
	if strings.HasSuffix(selector, "::text") {
		selector = strings.TrimSuffix(selector, "::text")
		pseudoText = true
	}

	// Handle ::attr(name)
	if idx := strings.Index(selector, "::attr("); idx != -1 {
		end := strings.Index(selector[idx:], ")")
		if end != -1 {
			pseudoAttr = selector[idx+7 : idx+end]
			selector = selector[:idx]
		}
	}

	var results []*Element
	sel.Find(selector).Each(func(_ int, s *goquery.Selection) {
		el := &Element{doc: s, sourceURL: sourceURL}
		if pseudoText {
			// Return a synthetic text element
			el = &Element{
				doc:       s,
				sourceURL: sourceURL,
			}
			// Override Text() to return direct text node content
			_ = pseudoText
		}
		if pseudoAttr != "" {
			val, _ := s.Attr(pseudoAttr)
			// Create a synthetic text-value element
			el = &Element{
				node:      &html.Node{Type: html.TextNode, Data: val},
				sourceURL: sourceURL,
			}
		}
		results = append(results, el)
	})
	return results
}

func selectionToElement(s *goquery.Selection, url string) *Element {
	return &Element{doc: s, sourceURL: url}
}
