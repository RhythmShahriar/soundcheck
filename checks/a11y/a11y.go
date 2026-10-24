// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package a11y is the accessibility module: after each page loads it runs
// axe-core (the WCAG 2 A and AA rules) and reports every violation as a
// page issue, a11y/<rule-id>, keyed by the rule and a stable selector of
// the element. A comparison then shows only what a change broke: a form
// that lost its labels or a button whose contrast dropped is NEW, the
// violations the snapshot already had stay quiet.
//
// axe-core is vendored in vendor/ (axe.min.js with its MPL-2.0 license,
// unmodified, from the npm package; see axeVersion) and pinned by
// SHA-256 in the tests, so a run never fetches code and two runs with the
// same Soundcheck always apply the same rules.
//
// The module is off by default: it's slower than the other Live hooks on
// big pages. Most sites also have a backlog of violations nobody wants to
// see on every run.
package a11y

import (
	"context"
	_ "embed" // axe-core
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/rhythm/kit/secrets"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// axeSource is axe-core's minified build, axe.min.js from the axe-core
// npm package at axeVersion. Its SHA-256 is checked by TestVendoredAxe.
//
//go:embed vendor/axe.min.js
var axeSource string

// axeVersion is the vendored axe-core release.
const axeVersion = "4.14.0"

// maxNodes caps how many elements one rule reports per page: a theme
// with a low-contrast footer would otherwise add hundreds of issues.
// axe lists elements in document order, so the same ones are kept on
// every run.
const maxNodes = 20

// timeout bounds one page's audit. axe takes well under a second on a
// typical page; a huge DOM can take several.
const timeout = 45 * time.Second

// Module is the accessibility module.
type Module struct{}

func (Module) Name() string { return "a11y" }

// result is what runJS sends back: axe's violations, trimmed to what the
// issues use.
type result struct {
	ID      string `json:"id"`
	Impact  string `json:"impact"`
	Help    string `json:"help"`
	HelpURL string `json:"helpUrl"`
	Nodes   []node `json:"nodes"`
}

type node struct {
	Path    [][]Elem `json:"path"` // see Selector
	HTML    string   `json:"html"`
	Summary string   `json:"failureSummary"`
	Impact  string   `json:"impact"`
}

// runJS runs axe in the page and returns its violations as JSON, with
// each element's path for Selector. Only the WCAG 2 level A and AA rules
// run (axe tags them wcag2a and wcag2aa); best practices and AAA rules
// are opinions a site owner hasn't signed up for. iframes are left out:
// each would need its own copy of axe. Embeds (maps, videos, consent
// banners) are someone else's.
const runJS = `(async () => {
  const path = el => {
    const trees = [];
    let tree = [];
    for (let n = el; n && n.nodeType === 1;) {
      const p = n.parentNode;
      const same = p ? Array.from(p.children || []).filter(c => c.localName === n.localName) : [];
      tree.unshift({tag: n.localName, id: n.id || '', name: n.getAttribute('name') || '', nth: same.length > 1 ? same.indexOf(n) + 1 : 0});
      if (p && p.nodeType === 11 && p.host) {
        trees.unshift(tree);
        tree = [];
        n = p.host;
      } else {
        n = n.parentElement;
      }
    }
    trees.unshift(tree);
    return trees;
  };
  const r = await axe.run(document, {
    runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa']},
    resultTypes: ['violations'],
    iframes: false,
    elementRef: true,
  });
  return JSON.stringify(r.violations.map(v => ({
    id: v.id, impact: v.impact || '', help: v.help, helpUrl: v.helpUrl,
    nodes: v.nodes.map(n => ({path: n.element ? path(n.element) : [], html: n.html, failureSummary: n.failureSummary || '', impact: n.impact || ''})),
  })));
})()`

// Live audits the loaded page. axe runs in an isolated world: it shares
// the page's DOM but not its globals. A site's AMD loader or a script
// that redefines Array or Promise can't break it. The page can't see or
// tamper with axe either.
func (Module) Live(ctx context.Context, cc *check.Context, tab *rod.Page, p *model.Page) {
	if p.Status >= 400 {
		return // a 404 or 500 page is the server's template, not the page
	}
	found, err := audit(ctx, tab)
	if err != nil {
		cc.Warn(fmt.Sprintf("a11y: %s: %v", p.Path, err))
		return
	}
	p.Issues = append(p.Issues, issues(found)...)
}

// audit injects axe into a fresh isolated world and runs it.
func audit(ctx context.Context, tab *rod.Page) ([]result, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	page := tab.Context(ctx)
	world, err := proto.PageCreateIsolatedWorld{FrameID: page.FrameID, WorldName: "soundcheck-a11y"}.Call(page)
	if err != nil {
		return nil, fmt.Errorf("can't open an isolated world: %w", err)
	}
	if _, err := eval(page, world.ExecutionContextID, axeSource, false); err != nil {
		return nil, fmt.Errorf("can't load axe-core: %w", err)
	}
	raw, err := eval(page, world.ExecutionContextID, runJS, true)
	if err != nil {
		return nil, fmt.Errorf("axe-core: %w", err)
	}
	var out []result
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("axe-core's result: %w", err)
	}
	return out, nil
}

// eval runs js in the given execution context and returns its string
// result.
func eval(page *rod.Page, id proto.RuntimeExecutionContextID, js string, await bool) (string, error) {
	res, err := proto.RuntimeEvaluate{Expression: js, ContextID: id, AwaitPromise: await, ReturnByValue: true}.Call(page)
	if err != nil {
		return "", err
	}
	if res.ExceptionDetails != nil {
		msg := res.ExceptionDetails.Text
		if e := res.ExceptionDetails.Exception; e != nil && e.Description != "" {
			msg, _, _ = strings.Cut(e.Description, "\n")
		}
		return "", errors.New(msg)
	}
	if res.Result == nil {
		return "", nil
	}
	return res.Result.Value.Str(), nil
}

// issues turns axe's violations into page issues, one per rule and
// element. The key is the check and the stable selector alone: axe's
// help text can be reworded in a new release without making every
// violation look new.
func issues(found []result) []model.Issue {
	var out []model.Issue
	for _, v := range found {
		seen := map[string]bool{}
		for n, el := range v.Nodes {
			if n == maxNodes {
				break
			}
			sel := Selector(el.Path)
			if sel == "" || seen[sel] {
				continue
			}
			seen[sel] = true
			detail := clean(el.Summary)
			if h := snippet(el.HTML); h != "" {
				detail = strings.TrimSpace(h + "\n" + detail)
			}
			if len(v.Nodes) > maxNodes {
				detail += fmt.Sprintf("\nThe first %d of %d elements that fail this rule on the page are reported.", maxNodes, len(v.Nodes))
			}
			i := model.Issue{Check: "a11y/" + v.ID, Level: level(firstOf(el.Impact, v.Impact)), Title: v.Help,
				Detail: detail, Source: sel, Fix: fix(v.HelpURL)}
			i.Key = i.Check + "|" + i.Source
			out = append(out, i)
		}
	}
	return out
}

// level maps axe's impact to an issue level: critical and serious
// violations stop some visitors from using the page.
func level(impact string) model.Level {
	switch impact {
	case "critical", "serious":
		return model.Warning
	}
	return model.Notice
}

func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// fix points at Deque's page for the rule, which explains it and how to
// fix it. axe adds a tracking query to the link; it isn't needed.
func fix(help string) string {
	u, err := url.Parse(help)
	if err != nil || u.Host == "" {
		return ""
	}
	u.RawQuery = ""
	return "See " + u.String() + " for what the rule asks and how to fix it."
}

var spaces = regexp.MustCompile(`\s+`)

// clean puts axe's failure summary ("Fix any of the following:\n
// Element does not have ...") on as few lines as reads well.
func clean(s string) string {
	var lines []string
	for l := range strings.SplitSeq(s, "\n") {
		if l = strings.TrimSpace(spaces.ReplaceAllString(l, " ")); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

// filled matches attributes that can carry what a visitor typed or what
// the site knows about them (a prefilled email on a page behind a login):
// value and data-*. Their contents never reach run.json or a report.
var filled = regexp.MustCompile(`(?i)(\s(?:value|data-[\w.:-]+)\s*=\s*)("[^"]*"|'[^']*'|[^\s"'=<>` + "`" + `]+)`)

// linked matches the URL attributes of an element, whose query (a reset
// link's token and email) or long path segments may be a credential.
var linked = regexp.MustCompile(`(?i)(\s(?:href|src|action|formaction)\s*=\s*)("[^"]*"|'[^']*'|[^\s"'<>` + "`" + `]+)`)

// opaque is a path segment long and random enough to be a token.
var opaque = regexp.MustCompile(`/[A-Za-z0-9_-]{24,}`)

// snippet shortens the element's HTML to its opening tag, with the values
// of filled attributes replaced by "***", the query of every link and
// any long opaque path segment in it masked, and credentials the kit
// knows redacted.
func snippet(html string) string {
	html = strings.TrimSpace(spaces.ReplaceAllString(html, " "))
	html = filled.ReplaceAllStringFunc(html, func(m string) string {
		sub := filled.FindStringSubmatch(m)
		if v := strings.Trim(sub[2], `"'`); v == "" {
			return m
		}
		return sub[1] + `"***"`
	})
	html = linked.ReplaceAllStringFunc(html, func(m string) string {
		sub := linked.FindStringSubmatch(m)
		v := strings.Trim(sub[2], `"'`)
		if q := strings.IndexAny(v, "?#"); q >= 0 && q < len(v)-1 {
			v = v[:q+1] + "***"
		}
		v = opaque.ReplaceAllString(v, "/***")
		return sub[1] + `"` + v + `"`
	})
	html = secrets.Redact(html)
	if r := []rune(html); len(r) > 160 {
		html = string(r[:159]) + "…"
	}
	return html
}

// Elem is one element on the way from the page to a violating element:
// its tag, id, name attribute and, when its parent has other children
// with the same tag, its position among them (from 1).
type Elem struct {
	Tag  string `json:"tag"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	Nth  int    `json:"nth,omitempty"`
}

var (
	// generated matches ids a framework or plugin makes up per render or
	// per post (":r1:", "el-4f9a2b7c", "post-12345"): they don't anchor.
	generated = regexp.MustCompile(`\d{3,}|[0-9a-f]{8,}|^:|^ember\d|^react-|^yui_|^uid-`)
	plainID   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)
)

// Selector builds a selector for an element that stays the same across
// runs, from its path: one list of elements from <html> down, or for an
// element in a shadow root, one list per tree joined with " >>> ".
//
// axe's own selector is the shortest unique one, so adding a second
// button to a form turns "button" into "#signup > button" and the same
// violation would look fixed and new at once. Here a path starts at its
// last element with a stable id or a name instead, so the steps above
// it (where widgets come and go) don't count. Only the steps below
// it have :nth-of-type(), for elements whose parent has siblings of the
// same tag. Without such an element the path starts below <body>.
func Selector(path [][]Elem) string {
	var parts []string
	for _, tree := range path {
		start := 0
		for i, e := range tree {
			if e.Tag == "html" || e.Tag == "body" {
				start = i + 1
			}
		}
		for i := len(tree) - 1; i >= start; i-- {
			if stableID(tree[i].ID) || tree[i].Name != "" {
				start = i
				break
			}
		}
		var steps []string
		for _, e := range tree[start:] {
			steps = append(steps, e.css())
		}
		if len(steps) > 0 {
			parts = append(parts, strings.Join(steps, " > "))
		}
	}
	return strings.Join(parts, " >>> ")
}

func stableID(id string) bool { return id != "" && !generated.MatchString(id) }

// css writes one step. An id or a name is enough to find the element,
// so only the other steps carry a position.
func (e Elem) css() string {
	switch {
	case stableID(e.ID) && plainID.MatchString(e.ID):
		return "#" + e.ID
	case stableID(e.ID):
		return e.Tag + `[id="` + quote(e.ID) + `"]`
	case e.Name != "":
		return e.Tag + `[name="` + quote(e.Name) + `"]`
	case e.Nth > 0:
		return fmt.Sprintf("%s:nth-of-type(%d)", e.Tag, e.Nth)
	}
	return e.Tag
}

func quote(s string) string { return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) }
