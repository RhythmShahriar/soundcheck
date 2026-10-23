package cache

import (
	"regexp"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// WordPress nonces are 10 hex characters. These patterns find the ones a
// cached page can carry; each match is (kind, value).
var nonceFinders = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"Contact Form 7 nonce", regexp.MustCompile(`(?i)name=["']_wpcf7[^"']*nonce["'][^>]*?value=["']([0-9a-f]{10})["']`)},
	{"wpApiSettings nonce", regexp.MustCompile(`wpApiSettings\s*=\s*\{[^}]*?"nonce"\s*:\s*"([0-9a-f]{10})"`)},
	{"WooCommerce nonce", regexp.MustCompile(`(?i)(?:wc|woocommerce|storeApi)[\w-]*nonce["']?\s*(?:[:=,]|value=)\s*\(?["']([0-9a-f]{10})["']`)},
	{"WooCommerce nonce", regexp.MustCompile(`(?i)name=["']woocommerce-[\w-]*nonce["'][^>]*?value=["']([0-9a-f]{10})["']`)},
	{"_wpnonce", regexp.MustCompile(`(?i)name=["']_wpnonce["'][^>]*?value=["']([0-9a-f]{10})["']`)},
	{"_wpnonce", regexp.MustCompile(`(?i)value=["']([0-9a-f]{10})["'][^>]*?name=["']_wpnonce["']`)},
	{"_wpnonce", regexp.MustCompile(`_wpnonce(?:=|%3D|&#0?61;)([0-9a-f]{10})\b`)},
	{"nonce", regexp.MustCompile(`(?i)name=["'][\w-]*nonce[\w-]*["'][^>]*?value=["']([0-9a-f]{10})["']`)},
	{"nonce", regexp.MustCompile(`(?i)"[\w-]*nonce"\s*:\s*"([0-9a-f]{10})"`)},
	{"Contact Form 7 nonce", regexp.MustCompile(`(?i)"?wpcf7[^;]{0,400}?"nonce"\s*:\s*"([0-9a-f]{10})"`)},
}

// Nonce is a nonce found in a page.
type Nonce struct{ Kind, Value string }

// Nonces returns the WordPress nonces in a page, one per value.
func Nonces(body string) []Nonce {
	var out []Nonce
	seen := map[string]bool{}
	for _, f := range nonceFinders {
		for _, m := range f.re.FindAllStringSubmatch(body, 20) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, Nonce{Kind: f.kind, Value: m[1]})
			}
		}
	}
	return out
}

func nonceKinds(ns []Nonce) []string {
	var out []string
	for _, n := range ns {
		if !slices.Contains(out, n.Kind) {
			out = append(out, n.Kind)
		}
	}
	return out
}

// MainText returns the visible text of a page's main content (<main>,
// <article>, role=main, #content…, else <body>), without scripts, styles
// or forms' hidden values.
func MainText(body string) string {
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return ""
	}
	root := find(doc, func(n *html.Node) bool { return n.Data == "main" || attr(n, "role") == "main" })
	if root == nil {
		root = find(doc, func(n *html.Node) bool { return n.Data == "article" })
	}
	if root == nil {
		root = find(doc, func(n *html.Node) bool {
			id := attr(n, "id")
			return id == "content" || id == "primary" || id == "main" || hasClass(n, "entry-content")
		})
	}
	if root == nil {
		root = find(doc, func(n *html.Node) bool { return n.Data == "body" })
	}
	if root == nil {
		root = doc
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "template", "svg", "iframe":
				return
			}
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return strings.Join(strings.Fields(b.String()), " ")
}

func find(n *html.Node, ok func(*html.Node) bool) *html.Node {
	if n.Type == html.ElementNode && ok(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := find(c, ok); f != nil {
			return f
		}
	}
	return nil
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, class string) bool {
	return slices.Contains(strings.Fields(attr(n, "class")), class)
}

// BodyClasses returns the classes on <body>, which WordPress fills with
// page facts: logged-in, woocommerce-cart, woocommerce-checkout…
var bodyTag = regexp.MustCompile(`(?is)<body\b[^>]*?\bclass\s*=\s*["']([^"']*)["']`)

func BodyClasses(body string) []string {
	if m := bodyTag.FindStringSubmatch(body); m != nil {
		return strings.Fields(m[1])
	}
	return nil
}

// volatile matches text that changes between renders of the same
// content: relative times, dates and clock times. Prices and counts stay,
// since a changed price is exactly what a stale cache gets wrong.
var volatile = regexp.MustCompile(`(?i)\b\d+\s+(?:sec|second|min|minute|hour|day|week|month|year)s?\s+ago\b|\b\d{1,4}[-/.]\d{1,2}[-/.]\d{1,4}\b|\b\d{1,2}:\d{2}(?::\d{2})?\s*(?:am|pm)?\b`)

// words returns a text's words without volatile tokens (times, nonces,
// hashes, long IDs), for comparing two renders of the same page.
func words(text string) []string {
	text = volatile.ReplaceAllString(strings.ToLower(text), " ")
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '$' && r != '€' && r != '£'
	})
	out := fields[:0]
	for _, f := range fields {
		digits := strings.IndexFunc(f, unicode.IsDigit) >= 0
		letters := strings.IndexFunc(f, unicode.IsLetter) >= 0
		if (len(f) >= 8 && digits && letters) || (digits && !letters && len(f) >= 5) {
			continue // a nonce, hash, random ID or timestamp
		}
		out = append(out, f)
	}
	return out
}

// wordDiff returns words only in a and words only in b, counting
// repeats, ignoring order (rotating widgets reorder, they don't change).
func wordDiff(a, b []string) (onlyA, onlyB []string) {
	count := map[string]int{}
	for _, w := range a {
		count[w]++
	}
	for _, w := range b {
		count[w]--
	}
	for _, w := range a {
		if count[w] > 0 {
			onlyA = append(onlyA, w)
			count[w]--
		}
	}
	for w, n := range count {
		for range -n {
			onlyB = append(onlyB, w)
		}
	}
	slices.Sort(onlyB)
	return onlyA, onlyB
}
