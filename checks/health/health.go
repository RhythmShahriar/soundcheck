// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package health checks that every page loads and renders without errors:
// HTTP status, redirects, PHP errors printed into the page, the WordPress
// "critical error" screen, white screens, framework error pages, JS
// errors, failed requests, broken links, mixed content and indexing.
package health

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/RhythmShahriar/rhythm/kit/crawl"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Module is the health check module.
type Module struct{}

func (Module) Name() string { return "health" }

var (
	// PHP errors printed into the page: "Warning: Undefined array key "id"
	// in /var/www/html/wp-content/themes/x/functions.php on line 212".
	phpError     = regexp.MustCompile(`(?i)\b(Fatal error|Parse error|Recoverable fatal error|Warning|Notice|Deprecated|Strict Standards)\s*:\s+(.{3,300}?)\s+in\s+(/?[^\s<>"']+\.(?:php|inc|phtml))(?:\(\d+\))?\s+on\s+line\s+(\d+)`)
	wpCritical   = regexp.MustCompile(`(?i)There has been a critical error on (this|your) website`)
	frameworkErr = regexp.MustCompile(`(?i)Whoops, looks like something went wrong|Symfony\\Component\\ErrorHandler|Ignition|Stack trace:\s*#0|Uncaught (Error|Exception|TypeError|ArgumentCountError)`)
	dbError      = regexp.MustCompile(`(?i)Error establishing a database connection|SQLSTATE\[[0-9A-Z]+\]`)
	scriptLoc    = regexp.MustCompile(`\((https?://[^)\s]+?):(\d+)\)$`)
	digits       = regexp.MustCompile(`\b\d{2,}\b`)
)

// CheckPage runs the per-page checks.
func (Module) CheckPage(cc *check.Context, p *model.Page) {
	if p.Error != "" {
		check.Page(p, model.Issue{Check: "health/unreachable", Level: model.Error, Title: "Page didn't load: " + p.Error,
			Fix: "Check the server and the URL; the page must answer before anything else can be checked."})
		return
	}
	switch {
	case p.Status >= 500:
		check.Page(p, model.Issue{Check: "health/status", Level: model.Error, Title: fmt.Sprintf("Server error %d", p.Status),
			Fix: "Look at the PHP and web server error logs for this URL."})
	case p.Status == 404 || p.Status == 410:
		check.Page(p, model.Issue{Check: "health/status", Level: model.Error, Title: fmt.Sprintf("Page not found (%d)", p.Status),
			Fix: "Restore the page, or redirect it and update links to it."})
	case p.Status >= 400:
		check.Page(p, model.Issue{Check: "health/status", Level: model.Error, Title: fmt.Sprintf("Page blocked (%d)", p.Status)})
	}
	if len(p.Redirects) > 2 {
		check.Page(p, model.Issue{Check: "health/redirect-chain", Level: model.Notice,
			Title: fmt.Sprintf("Redirects %d times before loading", len(p.Redirects)), Detail: strings.Join(p.Redirects, " → "),
			Fix: "Link to the final URL, and collapse the chain into one redirect."})
	}
	text := p.Text

	for _, m := range phpError.FindAllStringSubmatch(text, 20) {
		kind := strings.ToLower(m[1])
		level := model.Warning
		switch {
		case strings.Contains(kind, "fatal") || strings.Contains(kind, "parse"):
			level = model.Error
		case kind == "notice" || kind == "deprecated" || kind == "strict standards":
			level = model.Notice
		}
		check.Page(p, model.Issue{Check: "health/php-error", Level: level,
			Title:  "PHP " + titleCase(m[1]) + ": " + trimMessage(m[2]),
			Source: shortPath(m[3]) + ":" + m[4],
			Fix:    "Fix the code at the source line, and turn display_errors off in production so visitors never see these."})
	}
	switch {
	case wpCritical.MatchString(text):
		check.Page(p, model.Issue{Check: "health/wp-critical", Level: model.Error, Title: "WordPress \"critical error\" screen",
			Fix: "A plugin or theme threw a fatal error. Check the admin email WordPress sent, or wp-content/debug.log with WP_DEBUG_LOG on."})
	case dbError.MatchString(text):
		check.Page(p, model.Issue{Check: "health/database", Level: model.Error, Title: "Database error on the page",
			Fix: "Check the database server and the credentials in wp-config.php or .env."})
	case frameworkErr.MatchString(text) || strings.Contains(strings.ToLower(p.Title), "whoops"):
		check.Page(p, model.Issue{Check: "health/error-page", Level: model.Error, Title: "Application error page",
			Fix: "Read the exception shown; turn APP_DEBUG/display_errors off in production."})
	case p.Status == 200 && p.TextLength == 0 && !loadsContent(p):
		check.Page(p, model.Issue{Check: "health/white-screen", Level: model.Error, Title: "Blank page (white screen)",
			Fix: "A PHP fatal error with display_errors off usually causes this; check the error log."})
	}

	for _, c := range p.Console {
		msg, src := c, ""
		if m := scriptLoc.FindStringSubmatch(c); m != nil {
			msg = strings.TrimSpace(strings.TrimSuffix(c, m[0]))
			src = stripQuery(m[1]) + ":" + m[2]
			// A script inside the page itself: its line moves with the
			// content, so the same error on every page would never group.
			if u := stripQuery(m[1]); u == stripQuery(p.URL) || u == stripQuery(p.FinalURL) {
				src = "inline script"
			}
		}
		check.Page(p, model.Issue{Check: "health/js-error", Level: model.Warning, Title: "JS error: " + normalize(msg), Source: src,
			Fix: "Open the browser console on this page to see the full stack."})
	}

	for _, r := range p.Failed {
		target := shortURL(cc.Base, r.URL)
		switch {
		case isFavicon(r.URL):
			// The browser asks for /favicon.ico once per session, so which
			// page saw it fail depends on visit order; CheckSite reports it
			// once for the site.
		case r.Error != "":
			check.Page(p, model.Issue{Check: "health/failed-request", Level: model.Warning,
				Title: fmt.Sprintf("Request failed (%s): %s", strings.TrimPrefix(r.Error, "net::"), target)})
		case r.Type == "XHR" || r.Type == "Fetch" || strings.Contains(r.URL, "admin-ajax.php") || strings.Contains(r.URL, "/wp-json/"):
			check.Page(p, model.Issue{Check: "health/failed-request", Level: levelFor(r.Status),
				Title: fmt.Sprintf("%s %s returned %d", orDefault(r.Method, "GET"), target, r.Status),
				Fix:   "The page's own code calls this endpoint; check its server log at the time of the request."})
		default:
			check.Page(p, model.Issue{Check: "health/broken-asset", Level: model.Warning,
				Title: fmt.Sprintf("Broken %s: %s (%d)", strings.ToLower(orDefault(r.Type, "asset")), target, r.Status),
				Fix:   "Restore the file or remove the reference to it."})
		}
	}

	if strings.HasPrefix(p.FinalURL, "https://") {
		for _, r := range p.Resources {
			if strings.HasPrefix(r.URL, "http://") {
				check.Page(p, model.Issue{Check: "health/mixed-content", Level: model.Warning,
					Title: "Insecure (http://) resource on an https page", Source: shortURL(cc.Base, r.URL),
					Fix: "Load it over https, e.g. by updating the URL in the database (wp search-replace)."})
			}
		}
	}

	if p.Status == 200 {
		if p.Title == "" {
			check.Page(p, model.Issue{Check: "health/no-title", Level: model.Notice, Title: "Page has no <title>"})
		}
		if r := strings.ToLower(p.Robots); strings.Contains(r, "noindex") {
			check.Page(p, model.Issue{Check: "health/noindex", Level: model.Notice, Title: "Page is set to noindex",
				Detail: p.Robots, Fix: "If this page should be in search results, remove the noindex (WordPress: Settings → Reading, or your SEO plugin)."})
		}
		if m, ok := p.Shots["mobile"]; ok && m.Width > 500 {
			check.Page(p, model.Issue{Check: "health/not-responsive", Level: model.Notice,
				Title: fmt.Sprintf("Page renders %d px wide on a phone", m.Width),
				Fix:   `Add <meta name="viewport" content="width=device-width, initial-scale=1"> and responsive CSS.`})
		}
	}
}

// CheckSite reports a missing favicon once for the site, then tests
// links on visited pages that point at pages the run didn't visit (HEAD,
// falling back to GET) and reports broken ones once with every page that
// links to them.
func (Module) CheckSite(ctx context.Context, cc *check.Context) {
	favicon(cc)
	cc.Lock()
	visited := map[string]int{}
	from := map[string][]string{}
	orig := map[string]string{} // a link as written, to request and show; keys drop the trailing slash
	for _, p := range cc.Run.Pages {
		visited[strip(p.URL)] = p.Status
		if p.FinalURL != "" {
			visited[strip(p.FinalURL)] = p.Status
		}
	}
	for _, p := range cc.Run.Pages {
		for _, l := range p.Links {
			k := strip(l)
			if _, ok := orig[k]; !ok {
				orig[k] = l
			}
			if !slices.Contains(from[k], p.Path) {
				from[k] = append(from[k], p.Path)
			}
		}
	}
	cc.Unlock()

	var todo []string
	for l := range from {
		if _, ok := visited[l]; !ok && linkToCheck(orig[l], cc.Config.Pages.Exclude) {
			todo = append(todo, l)
		}
	}
	slices.Sort(todo)
	// A bounded sample keeps big sites fast; the detail says so, or a
	// clean result would read as every link checked.
	var sampled string
	if found := len(todo); found > maxLinks {
		todo = todo[:maxLinks]
		sampled = fmt.Sprintf("Checked %s of %s links found on the visited pages; the rest weren't requested.",
			thousands(maxLinks), thousands(found))
	}
	status := map[string]int{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, l := range todo {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			code := probe(ctx, cc, orig[l])
			mu.Lock()
			status[l] = code
			mu.Unlock()
		})
	}
	wg.Wait()
	for l, code := range visited {
		status[l] = code
	}
	for l, pages := range from {
		code := status[l]
		if code == 404 || code == 410 || code >= 500 {
			slices.Sort(pages)
			cc.AddIssue(model.Issue{Check: "health/broken-link", Level: model.Warning,
				Title: fmt.Sprintf("Broken link to %s (%d)", shortURL(cc.Base, cmp.Or(orig[l], l)), code), Pages: pages,
				Detail: sampled, Fix: "Update or remove the link, or redirect the old URL."})
		}
	}
}

// maxLinks is how many links to unvisited pages CheckSite requests.
const maxLinks = 300

// thousands writes n with comma separators, e.g. 1,240.
func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// favicon adds one site-level "No favicon" when any page saw the
// browser's /favicon.ico request fail. Its key doesn't depend on which
// pages asked, so two runs that visit pages in a different order agree.
func favicon(cc *check.Context) {
	cc.Lock()
	var pages, targets []string
	for _, p := range cc.Run.Pages {
		for _, r := range p.Failed {
			if isFavicon(r.URL) {
				targets = append(targets, shortURL(cc.Base, r.URL))
				if !slices.Contains(pages, p.Path) {
					pages = append(pages, p.Path)
				}
			}
		}
	}
	cc.Unlock()
	if len(pages) == 0 {
		return
	}
	slices.Sort(pages)
	slices.Sort(targets)
	cc.AddIssue(model.Issue{Check: "health/favicon", Level: model.Notice, Title: "No favicon", Source: targets[0], Pages: pages,
		Fix: "Add a favicon.ico at the site root, or a <link rel=\"icon\"> in every page's <head>."})
}

func isFavicon(raw string) bool { return strings.HasSuffix(stripQuery(raw), "/favicon.ico") }

func probe(ctx context.Context, cc *check.Context, raw string) int {
	for _, m := range []string{http.MethodHead, http.MethodGet} {
		resp, err := cc.Get(ctx, m, raw)
		if err != nil {
			return 0
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed && resp.StatusCode != http.StatusNotImplemented {
			return resp.StatusCode
		}
	}
	return 0
}

// Compare reports pages whose title changed, that gained or lost a
// redirect, or that lost most of their content.
func (Module) Compare(_ context.Context, cmp *check.CompareContext) []model.Change {
	var out []model.Change
	for _, pair := range cmp.Pairs() {
		b, a := pair[0], pair[1]
		if a.Error != "" || a.Status >= 400 || b.Status >= 400 {
			continue // status problems are reported as issues
		}
		// A title that went away is the NEW "Page has no <title>" issue;
		// saying it changed to nothing too would be two lines for one cause.
		if b.Title != "" && a.Title != "" && a.Title != b.Title {
			out = append(out, model.Change{Class: model.Changed, Check: "health/title", Title: "Page title changed",
				Pages: []string{a.Path}, Before: b.Title, After: a.Title})
		}
		if b.TextLength > 400 && a.TextLength < b.TextLength/2 {
			out = append(out, model.Change{Class: model.Changed, Check: "health/content-lost", Title: "Page lost most of its text",
				Pages: []string{a.Path}, Before: strconv.Itoa(b.TextLength) + " characters", After: strconv.Itoa(a.TextLength) + " characters",
				Score: float64(b.TextLength-a.TextLength) * 100 / float64(b.TextLength)})
		}
		if bf, af := finalPath(b), finalPath(a); bf != af {
			out = append(out, model.Change{Class: model.Changed, Check: "health/redirect", Title: "Page now ends somewhere else",
				Pages: []string{a.Path}, Before: bf, After: af})
		}
	}
	return out
}

// FilterChanges drops the pair a snapshot taken before "No favicon" was
// site-level makes: the same issue FIXED on a page and NEW for the site.
func (Module) FilterChanges(changes []model.Change) []model.Change {
	i := slices.IndexFunc(changes, func(c model.Change) bool { return c.Class == model.New && c.Check == "health/favicon" })
	if i < 0 {
		return changes
	}
	j := slices.IndexFunc(changes, func(c model.Change) bool {
		return c.Class == model.Fixed && c.Check == changes[i].Check && c.Title == changes[i].Title && c.Source == changes[i].Source
	})
	if j < 0 {
		return changes
	}
	return slices.Delete(slices.Delete(changes, max(i, j), max(i, j)+1), min(i, j), min(i, j)+1)
}

func finalPath(p *model.Page) string {
	if u, err := url.Parse(p.FinalURL); err == nil && p.FinalURL != "" {
		return u.RequestURI()
	}
	return p.Path
}

func levelFor(status int) model.Level {
	if status >= 500 || status == 403 {
		return model.Error
	}
	return model.Warning
}

// shortPath trims a server path to the part people recognize:
// /var/www/html/wp-content/themes/x/functions.php → themes/x/functions.php
func shortPath(p string) string {
	for _, marker := range []string{"/wp-content/", "/wp-includes/", "/wp-admin/", "/app/", "/vendor/", "/public/", "/src/"} {
		if i := strings.LastIndex(p, marker); i >= 0 {
			if marker == "/wp-content/" {
				return p[i+len(marker):]
			}
			return p[i+1:]
		}
	}
	return path.Base(p)
}

// trimMessage keeps PHP messages short and stable across runs.
func trimMessage(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 140 {
		s = string(r[:139]) + "…"
	}
	return s
}

// normalize removes numbers (timestamps, IDs) from a JS error so the same
// error groups across pages and runs.
func normalize(s string) string {
	s = trimMessage(s)
	return digits.ReplaceAllString(s, "N")
}

func titleCase(s string) string {
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

func shortURL(base *url.URL, raw string) string {
	u, err := url.Parse(raw)
	if err != nil || base == nil {
		return raw
	}
	if strings.EqualFold(strings.TrimPrefix(u.Hostname(), "www."), strings.TrimPrefix(base.Hostname(), "www.")) {
		return stripQuery(u.RequestURI())
	}
	return stripQuery(raw)
}

func stripQuery(s string) string {
	s, _, _ = strings.Cut(s, "?")
	return s
}

func strip(raw string) string {
	raw, _, _ = strings.Cut(raw, "#")
	return strings.TrimSuffix(raw, "/")
}

// linkToCheck reports whether a link is a page worth requesting: the same
// rules as discovery, so links that act (add to cart, log out) are never
// requested, not even with HEAD.
func linkToCheck(raw string, exclude []string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && crawl.IsPage(u) &&
		!crawl.Excluded(u, crawl.DefaultExcludes) && !crawl.Excluded(u, exclude)
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// loadsContent reports whether an empty-text page still loaded something
// that could show content (styles, scripts, images). The browser's own
// favicon fetch doesn't count: WordPress redirects /favicon.ico to its
// logo, so a blank WordPress page always "loads" one image.
func loadsContent(p *model.Page) bool {
	for _, r := range p.Resources {
		if r.Type == "Other" || strings.Contains(r.URL, "favicon") || strings.Contains(r.URL, "/wp-includes/images/w-logo") {
			continue
		}
		return true
	}
	return false
}
