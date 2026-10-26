// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package a11y

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// result is one rule axe found violated, trimmed to what the issues use.
type result struct {
	ID      string   `json:"id"`
	Impact  string   `json:"impact"`
	Help    string   `json:"help"`
	HelpURL string   `json:"helpUrl"`
	Tags    []string `json:"tags"`  // axe's tags for the rule: wcag143, EN-9.1.4.3, cat.color...
	Total   int      `json:"total"` // elements that fail the rule, of which Nodes are the first
	Nodes   []node   `json:"nodes"`
}

type node struct {
	Path    [][]Elem `json:"path"` // see Selector
	HTML    string   `json:"html"`
	Summary string   `json:"failureSummary"`
	Impact  string   `json:"impact"`
}

// frameRef is an iframe in an audited document: the host it loads from
// and whether it has the document's origin (axe could reach into it).
type frameRef struct {
	Host string `json:"host"`
	Same bool   `json:"same"`
}

// docReport is what runJS sends back for one document.
type docReport struct {
	Violations []result   `json:"violations"`
	Frames     []frameRef `json:"frames"`
	Rules      []string   `json:"rules"` // the rule ids run, when auditOptions.Rules asked
	// Fields are the fields named only by their placeholder, which
	// the module's own check finds after axe (see fields).
	Fields []field `json:"-"`
	// FieldsBeyond counts such fields past the ones looked at.
	FieldsBeyond beyond `json:"-"`
	// Notes are warnings about the module's own checks in the document.
	Notes []string `json:"-"`
}

// report is the audit of a page and its frames.
type report struct {
	Violations []result
	Frames     []string // hosts of the frames not audited, sorted
	Rules      []string // as in docReport, from the page's own document
	Fields     []field  // as in docReport, from every document audited
	Beyond     beyond   // docReport.FieldsBeyond, from every document audited
	Notes      []string // as in docReport
}

// auditOptions are runJS's argument.
type auditOptions struct {
	Tags  []string `json:"tags"`  // the axe tags to run, from tags
	Max   int      `json:"max"`   // elements kept per rule
	Rules bool     `json:"rules"` // also list the rules run
	// Inner lists the frames inside same-origin frames too, for a page
	// whose frames aren't audited: a cross-origin frame inside one is
	// just as untested.
	Inner bool `json:"inner"`
}

// Limits on what axe sends back. runJS trims to them before the result
// is turned into JSON, so a page with 20,000 failing elements costs
// neither the browser nor Soundcheck much memory; parse refuses anything
// bigger than maxResult all the same.
const (
	maxResult  = 2 << 20 // bytes of JSON from one document
	maxRules   = 200
	maxHTML    = 400  // characters of an element's HTML
	maxSummary = 1000 // characters of a failure summary
	maxDepth   = 3    // levels of same-origin frames audited or listed
)

// pathJS defines path(el): the element's path for Selector, one list of
// elements per tree from the document down, crossing shadow roots.
const pathJS = `const path = el => {
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
  };`

// runJS runs axe in a document and returns its violations as JSON, with
// each element's path for Selector and the document's iframes. Only the
// rules with o.tags run, the WCAG A and AA rules of the run's version;
// best practices, AAA and experimental rules are opinions a site owner
// hasn't signed up for (axe leaves experimental rules out unless asked).
// axe itself never enters iframes: audit audits same-origin ones in
// their own world when a11y.iframes asks. Frames from other sites are
// someone else's (maps, videos, consent banners).
var runJS = strings.NewReplacer("MAX_DEPTH", strconv.Itoa(maxDepth), "MAX_RULES", strconv.Itoa(maxRules),
	"MAX_HTML", strconv.Itoa(maxHTML), "MAX_SUMMARY", strconv.Itoa(maxSummary)).Replace(`async (o) => {
  ` + pathJS + `
  const frames = [];
  const list = (doc, depth) => {
    for (const f of doc.querySelectorAll('iframe, frame')) {
      let inner = null;
      try { inner = f.contentDocument; } catch (e) {}
      let host = location.host;
      if (f.src) {
        try {
          const u = new URL(f.src);
          host = u.host || (u.protocol === 'about:' ? location.host : u.protocol);
        } catch (e) {}
      }
      frames.push({host, same: !!inner});
      if (inner && o.inner && depth < MAX_DEPTH) list(inner, depth + 1);
    }
  };
  list(document, 1);
  const r = await axe.run(document, {
    runOnly: {type: 'tag', values: o.tags},
    resultTypes: ['violations'],
    iframes: false,
    elementRef: true,
  });
  const violations = r.violations.slice(0, MAX_RULES).map(v => ({
    id: v.id, impact: v.impact || '', help: v.help, helpUrl: v.helpUrl, tags: v.tags, total: v.nodes.length,
    nodes: v.nodes.slice(0, o.max).map(n => ({
      path: n.element ? path(n.element) : [],
      html: (n.html || '').slice(0, MAX_HTML),
      failureSummary: (n.failureSummary || '').slice(0, MAX_SUMMARY),
      impact: n.impact || '',
    })),
  }));
  const rules = o.rules ? axe.getRules(o.tags).filter(x => !x.tags.includes('experimental')).map(x => x.ruleId).sort() : null;
  return JSON.stringify({violations, frames, rules});
}`)

// ownerJS is called on a frame's owner element (<iframe>) in its
// parent's world and returns the element's path as JSON.
const ownerJS = `function () {
  ` + pathJS + `
  return JSON.stringify(path(this));
}`

// errTimedOut is the audit running out of its time (timeout).
var errTimedOut = errors.New("axe-core timed out")

func isTimeout(err error) bool { return errors.Is(err, errTimedOut) }

// audit runs axe on the page and, with iframes, on its same-origin
// frames, each in a fresh isolated world.
func audit(parent context.Context, tab *rod.Page, opts auditOptions, iframes bool) (report, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	page := tab.Context(ctx)
	opts.Inner = !iframes
	main, world, err := auditFrame(page, page.FrameID, opts, nil)
	if err != nil {
		if parent.Err() == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return report{}, errTimedOut
		}
		return report{}, err
	}
	rep := report{Violations: main.Violations, Rules: main.Rules, Fields: main.Fields, Beyond: main.FieldsBeyond, Notes: main.Notes}
	hosts := map[string]bool{}
	for _, f := range main.Frames {
		if !iframes || !f.Same {
			hosts[f.Host] = true
		}
	}
	if iframes {
		tree, err := proto.PageGetFrameTree{}.Call(page)
		if err == nil && tree.FrameTree != nil {
			fa := frameAudit{page: page, opts: opts, origin: tree.FrameTree.Frame.SecurityOrigin, rep: &rep, hosts: hosts}
			fa.opts.Rules = false
			fa.walk(tree.FrameTree, world, nil, 1)
		}
		if parent.Err() == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return report{}, errTimedOut
		}
	}
	delete(hosts, "")
	rep.Frames = slices.Sorted(maps.Keys(hosts))
	return rep, nil
}

// frameAudit audits the same-origin frames of a page.
type frameAudit struct {
	page   *rod.Page
	opts   auditOptions
	origin string // the page's security origin
	rep    *report
	hosts  map[string]bool // frames not audited
}

// walk audits the same-origin children of t, whose document has the
// isolated world world and whose own element path is prefix. A frame
// that can't be audited is listed by host instead; frames from other
// origins were listed by their parent's run of axe.
func (fa *frameAudit) walk(t *proto.PageFrameTree, world proto.RuntimeExecutionContextID, prefix [][]Elem, depth int) {
	for _, c := range t.ChildFrames {
		if c.Frame == nil || c.Frame.SecurityOrigin != fa.origin || fa.origin == "" || fa.origin == "null" {
			continue
		}
		host := frameHost(c.Frame.URL)
		if depth > maxDepth || fa.page.GetContext().Err() != nil {
			fa.hosts[host] = true
			continue
		}
		owner, err := ownerPath(fa.page, c.Frame.ID, world)
		if err != nil {
			fa.hosts[host] = true
			continue
		}
		path := append(slices.Clone(prefix), owner...)
		doc, inner, err := auditFrame(fa.page, c.Frame.ID, fa.opts, path)
		if err != nil {
			fa.hosts[host] = true
			continue
		}
		for _, f := range doc.Frames {
			if !f.Same {
				fa.hosts[f.Host] = true
			}
		}
		fa.rep.Violations = merge(fa.rep.Violations, doc.Violations)
		fa.rep.Fields = append(fa.rep.Fields, doc.Fields...)
		fa.rep.Beyond.add(doc.FieldsBeyond)
		fa.rep.Notes = append(fa.rep.Notes, doc.Notes...)
		fa.walk(c, inner, path, depth+1)
	}
}

// frameHost is the host a frame's URL loads from, for Audit.Frames.
func frameHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

// ownerPath is the element path of the <iframe> that holds frame, read
// in its parent's isolated world.
func ownerPath(page *rod.Page, frame proto.PageFrameID, world proto.RuntimeExecutionContextID) ([][]Elem, error) {
	owner, err := proto.DOMGetFrameOwner{FrameID: frame}.Call(page)
	if err != nil {
		return nil, err
	}
	obj, err := proto.DOMResolveNode{BackendNodeID: owner.BackendNodeID, ExecutionContextID: world}.Call(page)
	if err != nil {
		return nil, err
	}
	res, err := proto.RuntimeCallFunctionOn{FunctionDeclaration: ownerJS, ObjectID: obj.Object.ObjectID, ReturnByValue: true}.Call(page)
	if err != nil {
		return nil, err
	}
	if res.ExceptionDetails != nil {
		return nil, errors.New(res.ExceptionDetails.Text)
	}
	var out [][]Elem
	if err := json.Unmarshal([]byte(res.Result.Value.Str()), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// auditFrame injects axe into a fresh isolated world of frame and runs
// it. prefix is the frame's own element path, put before each element's
// so its selector says which frame it's in.
func auditFrame(page *rod.Page, frame proto.PageFrameID, opts auditOptions, prefix [][]Elem) (docReport, proto.RuntimeExecutionContextID, error) {
	world, err := proto.PageCreateIsolatedWorld{FrameID: frame, WorldName: "soundcheck-a11y"}.Call(page)
	if err != nil {
		return docReport{}, 0, fmt.Errorf("can't open an isolated world: %w", err)
	}
	if _, err := eval(page, world.ExecutionContextID, axeSource, false); err != nil {
		return docReport{}, 0, fmt.Errorf("can't load axe-core: %w", err)
	}
	arg, err := json.Marshal(opts)
	if err != nil {
		return docReport{}, 0, err
	}
	raw, err := eval(page, world.ExecutionContextID, "("+runJS+")("+string(arg)+")", true)
	if err != nil {
		return docReport{}, 0, err
	}
	doc, err := parse(raw)
	if err != nil {
		return docReport{}, 0, err
	}
	// The placeholder check runs in the same world, after axe: it reads
	// the same DOM and needs nothing axe left behind.
	found, more, err := fields(page, world.ExecutionContextID, prefix)
	switch {
	case page.GetContext().Err() != nil:
		return docReport{}, 0, page.GetContext().Err()
	case err != nil:
		doc.Notes = append(doc.Notes, "placeholder check: "+err.Error())
	default:
		doc.Fields, doc.FieldsBeyond = found, more
	}
	if len(prefix) > 0 {
		for i := range doc.Violations {
			for j := range doc.Violations[i].Nodes {
				n := &doc.Violations[i].Nodes[j]
				n.Path = append(slices.Clone(prefix), n.Path...)
			}
		}
	}
	return doc, world.ExecutionContextID, nil
}

// parse reads runJS's result. A result over maxResult is refused: runJS
// trims what it sends, so one that big isn't axe's.
func parse(raw string) (docReport, error) {
	if len(raw) > maxResult {
		return docReport{}, errors.New("axe-core result too large")
	}
	var out docReport
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return docReport{}, fmt.Errorf("axe-core's result: %w", err)
	}
	if len(out.Violations) > maxRules {
		out.Violations = out.Violations[:maxRules]
	}
	return out, nil
}

// merge adds a frame's violations to the page's: the elements of a rule
// both have follow the page's and the counts add up.
func merge(into, more []result) []result {
	for _, v := range more {
		i := slices.IndexFunc(into, func(r result) bool { return r.ID == v.ID })
		if i < 0 {
			into = append(into, v)
			continue
		}
		into[i].Nodes = append(into[i].Nodes, v.Nodes...)
		into[i].Total += max(v.Total, len(v.Nodes))
	}
	return into
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
