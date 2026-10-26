// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package a11y is the accessibility module: after each page loads it runs
// axe-core (the A and AA rules of the run's WCAG version) and reports
// every violation as a page issue, a11y/<rule-id>, keyed by the rule and
// a stable selector of the element. Each issue names the WCAG success
// criteria and EN 301 549 clauses it fails. A comparison then shows only
// what a change broke: a form that lost its labels or a button whose
// contrast dropped is NEW, the violations the snapshot already had stay
// quiet.
//
// axe-core is vendored in vendor/ (axe.min.js with its MPL-2.0 license,
// unmodified, from the npm package; see axeVersion) and pinned by
// SHA-256 in the tests, so a run never fetches code and two runs with the
// same Soundcheck always apply the same rules. The run records the rules
// it applied (model.Options.A11y) and whether each page's audit finished
// (model.Page.Audits): a page whose audit timed out isn't a clean page.
//
// The module is off by default: it's slower than the other Live hooks on
// big pages. Most sites also have a backlog of violations nobody wants to
// see on every run.
package a11y

import (
	"cmp"
	"context"
	"crypto/sha256"
	_ "embed" // axe-core
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"

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

// axeDigest is the hex SHA-256 of axeSource, recorded with the rules a
// run applied.
var axeDigest = func() string {
	sum := sha256.Sum256([]byte(axeSource))
	return hex.EncodeToString(sum[:])
}()

// timeout bounds one page's audit. axe takes well under a second on a
// typical page; a huge DOM can take several. A variable so the tests
// can make a page run out of time.
var timeout = 45 * time.Second

// Module is the accessibility module. It keeps no state: its settings
// come from the run's config and what it records goes on the run.
type Module struct{}

// Name is the module's name in --modules and the config file.
func (Module) Name() string { return "a11y" }

// Live audits the loaded page. axe runs in an isolated world: it shares
// the page's DOM but not its globals. A site's AMD loader or a script
// that redefines Array or Promise can't break it. The page can't see or
// tamper with axe either.
//
// The page's Audits["a11y"] always says how the audit went, so a page
// whose audit timed out or failed is never taken for a clean one.
func (Module) Live(ctx context.Context, cc *check.Context, tab *rod.Page, p *model.Page) {
	if p.Status >= 400 {
		// A 404 or 500 page is the server's template, not the page.
		setAudit(p, model.Audit{Status: model.AuditSkipped, Reason: "status " + strconv.Itoa(p.Status)})
		return
	}
	s := settingsOf(cc.Config)
	version := runVersion(cc.Run)
	opts := auditOptions{Tags: tags(version), Max: s.MaxElements, Rules: needRules(cc)}
	rep, err := audit(ctx, tab, opts, s.IFrames)
	if err != nil {
		a := failed(ctx, err)
		setAudit(p, a)
		cc.Warn(fmt.Sprintf("a11y: %s: axe-core: %s; the page's a11y results are missing; the statement marks it", p.Path, strings.TrimPrefix(a.Reason, "axe-core ")))
		return
	}
	if rep.Rules != nil {
		recordRules(cc, opts, rep.Rules)
	}
	a := model.Audit{Status: model.AuditOK, Frames: rep.Frames}
	for _, v := range rep.Violations {
		if v.Total > s.MaxElements {
			if a.CappedRules == nil {
				a.CappedRules = map[string]int{}
			}
			a.CappedRules[v.ID] = v.Total
		}
	}
	found := issues(rep.Violations, s.MaxElements, version)
	placeholder, capped := placeholderIssues(rep.Fields, found, s.MaxElements)
	if capped > 0 {
		if a.CappedRules == nil {
			a.CappedRules = map[string]int{}
		}
		a.CappedRules["placeholder-label"] = capped
	}
	for _, n := range rep.Notes {
		cc.Warn(fmt.Sprintf("a11y: %s: %s", p.Path, secrets.Redact(n)))
	}
	setAudit(p, a)
	p.Issues = append(p.Issues, found...)
	p.Issues = append(p.Issues, placeholder...)
	if reflows(version) {
		i, err := reflow(ctx, tab)
		switch {
		case err != nil && ctx.Err() == nil:
			cc.Warn(fmt.Sprintf("a11y: %s: reflow check at 320 px: %s", p.Path, secrets.Redact(err.Error())))
		case i != nil:
			p.Issues = append(p.Issues, *i)
		}
	}
}

// reflows reports whether the WCAG version has 1.4.10 Reflow, which 2.1
// added.
func reflows(version string) bool { return version == "2.1" || version == "2.2" }

// runVersion is the WCAG version run's rules are for. A run made before
// 1.3 recorded none and used the 2.0 rules. So does a compare of it.
func runVersion(run *model.Run) string {
	if run == nil || run.Options.WCAG == "" {
		return "2.0"
	}
	return run.Options.WCAG
}

// needRules reports whether the run still has to record the rules axe
// applies. The first page to finish records them; asking on every page
// until then costs one list of ids.
func needRules(cc *check.Context) bool {
	if cc.Run == nil {
		return false
	}
	cc.Lock()
	defer cc.Unlock()
	return cc.Run.Options.A11y == nil
}

// recordRules sets the run's A11y options once: pages audited in
// parallel all get the same rules, so the first one wins.
func recordRules(cc *check.Context, opts auditOptions, rules []string) {
	cc.Lock()
	defer cc.Unlock()
	if cc.Run.Options.A11y != nil {
		return
	}
	all := append(slices.Clone(rules), ownRules(opts.Tags)...)
	slices.Sort(all)
	cc.Run.Options.A11y = &model.A11yRules{Axe: axeVersion, AxeSHA256: axeDigest, Tags: slices.Clone(opts.Tags),
		Rules: slices.Compact(all), MaxElements: opts.Max}
}

// ownRules are the module's own checks that run with axe's rules of
// tags, by the name after "a11y/". They're recorded with axe's, so a
// comparison with a snapshot made before a check existed doesn't call
// everything it finds NEW (compare's rule-set guard).
func ownRules(tags []string) []string {
	out := []string{"placeholder-label"}
	if slices.Contains(tags, "wcag21aa") {
		out = append(out, "reflow")
	}
	return out
}

func setAudit(p *model.Page, a model.Audit) {
	if p.Audits == nil {
		p.Audits = map[string]model.Audit{}
	}
	p.Audits["a11y"] = a
}

// maxReason caps an error's text in Audit.Reason.
const maxReason = 200

// failed is the audit of a page whose run of axe returned err: a
// timeout when the audit's own time ran out, otherwise an error with
// the error's text, redacted and shortened.
func failed(ctx context.Context, err error) model.Audit {
	if ctx.Err() == nil && isTimeout(err) {
		return model.Audit{Status: model.AuditTimeout, Reason: "axe-core timed out after " + timeout.String()}
	}
	reason := secrets.Redact(err.Error())
	if r := []rune(reason); len(r) > maxReason {
		reason = string(r[:maxReason-1]) + "…"
	}
	return model.Audit{Status: model.AuditError, Reason: reason}
}

// issues turns axe's violations into page issues, one per rule and
// element, at most keep per rule. The key is the check and the stable
// selector alone (see model.IssueKey): axe's help text can be reworded
// in a new release without making every violation look new. version is
// the run's WCAG version, which decides the criteria an issue names.
func issues(found []result, keep int, version string) []model.Issue {
	var out []model.Issue
	for _, v := range found {
		total := max(v.Total, len(v.Nodes))
		wcag, en := criteria(v.Tags, version)
		seen := map[string]bool{}
		for n, el := range v.Nodes {
			if n == keep {
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
			if total > keep {
				detail += fmt.Sprintf("\nThe first %d of %d elements that fail this rule on the page are reported.", keep, total)
			}
			impact := firstOf(el.Impact, v.Impact)
			i := model.Issue{Check: "a11y/" + v.ID, Level: level(impact), Title: v.Help,
				Detail: detail, Source: sel, Fix: fix(v.HelpURL),
				WCAG: slices.Clone(wcag), EN301549: slices.Clone(en), Impact: impact}
			i.Identify()
			out = append(out, i)
		}
	}
	return out
}

var (
	// criterionTag is axe's tag for a WCAG success criterion: wcag1412
	// is 1.4.12 (principle, guideline, then the criterion's number).
	criterionTag = regexp.MustCompile(`^wcag(\d)(\d)(\d+)$`)
	// clauseTag is axe's tag for an EN 301 549 clause: EN-9.1.3.5.
	clauseTag = regexp.MustCompile(`^EN-9\.[0-9.]+$`)
)

// wcag22Only are the criteria WCAG 2.2 added. axe tags its rules with
// them whatever version runs, so a 2.1 run leaves them out: they aren't
// part of the standard it tests against.
var wcag22Only = []string{"2.4.11", "2.4.12", "2.4.13", "2.5.7", "2.5.8", "3.2.6", "3.3.7", "3.3.8", "3.3.9"}

// criteria reads the WCAG success criteria ("1.4.12") and EN 301 549
// clauses ("9.1.4.12") from a rule's axe tags, each sorted by number and
// without repeats. Tags of neither kind (cat.forms, wcag2aa) are left
// out. So are the criteria version doesn't have.
func criteria(tags []string, version string) (wcag, en []string) {
	dropped := func(id string) bool { return version != "2.2" && slices.Contains(wcag22Only, id) }
	for _, t := range tags {
		if m := criterionTag.FindStringSubmatch(t); m != nil {
			if id := m[1] + "." + m[2] + "." + m[3]; !dropped(id) {
				wcag = append(wcag, id)
			}
		} else if clauseTag.MatchString(t) {
			if c := strings.TrimPrefix(t, "EN-"); !dropped(strings.TrimPrefix(c, "9.")) {
				en = append(en, c)
			}
		}
	}
	return sortedIDs(wcag), sortedIDs(en)
}

// sortedIDs sorts dotted numbers part by part (1.4.3 comes before
// 1.4.12) and drops repeats.
func sortedIDs(ids []string) []string {
	slices.SortFunc(ids, func(a, b string) int {
		as, bs := strings.Split(a, "."), strings.Split(b, ".")
		for i := range min(len(as), len(bs)) {
			x, _ := strconv.Atoi(as[i])
			y, _ := strconv.Atoi(bs[i])
			if c := cmp.Compare(x, y); c != 0 {
				return c
			}
		}
		return cmp.Compare(len(as), len(bs))
	})
	return slices.Compact(ids)
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

// snippet shortens the element's HTML to its opening tag. The values of
// filled attributes become "***". So do the query of every link and any
// long opaque path segment in it. Credentials the kit knows are redacted.
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
