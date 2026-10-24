// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package seo is the SEO module: it records what search engines read on
// each page (meta description, canonical, robots meta, <h1> count,
// hreflang, Open Graph and the JSON-LD types) and for the site
// (robots.txt and the sitemap's page count). A comparison then reports
// what an update changed: a canonical or JSON-LD an SEO plugin stopped
// printing, a robots.txt that now disallows /, a sitemap that lost a
// third of its pages, a page that now redirects elsewhere.
//
// The check ids (seo/canonical, seo/description, seo/robots-meta,
// seo/robots-txt, seo/h1, seo/hreflang, seo/og, seo/jsonld, seo/sitemap
// and seo/redirect) are a contract: testbed scenarios expect them.
//
// Nothing here is an issue on its own (a site may have no hreflang on
// purpose), so the module only compares. Its facts are stored next to
// the run: seo/<page id>.json per page and seo/site.json.
//
// The module is off by default: most checks are only worth it on sites
// that care about search. robots.txt and the sitemap also cost requests.
package seo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Module is the SEO module.
type Module struct{}

func (Module) Name() string { return "seo" }

// Facts are what one page tells search engines.
type Facts struct {
	Description string            `json:"description,omitempty"`
	Canonical   string            `json:"canonical,omitempty"` // absolute
	H1          int               `json:"h1"`
	Hreflang    map[string]string `json:"hreflang,omitempty"` // language → absolute URL
	OG          map[string]string `json:"og,omitempty"`       // og:title → content; og:url and og:image absolute
	JSONLD      []string          `json:"jsonld,omitempty"`   // the @type values, sorted
	// JSONLDErrors counts JSON-LD blocks that aren't valid JSON, which
	// search engines skip.
	JSONLDErrors int `json:"jsonld_errors,omitempty"`
}

// factsJS reads the page's head. JSON-LD is sent raw and parsed in Go.
const factsJS = `() => {
  const abs = h => { try { return h ? new URL(h, document.baseURI).href : ''; } catch (e) { return h; } };
  const desc = document.querySelector('meta[name="description" i]');
  const canon = document.querySelector('link[rel="canonical" i]');
  const hreflang = {};
  for (const l of document.querySelectorAll('link[rel="alternate" i][hreflang]')) {
    const lang = l.getAttribute('hreflang').trim().toLowerCase();
    if (lang && !(lang in hreflang)) hreflang[lang] = abs(l.getAttribute('href'));
  }
  const og = {};
  for (const m of document.querySelectorAll('meta[property^="og:" i]')) {
    const k = m.getAttribute('property').trim().toLowerCase();
    const v = (m.getAttribute('content') || '').trim();
    if (!(k in og)) og[k] = k === 'og:url' || k === 'og:image' ? abs(v) : v;
  }
  return {
    description: desc ? (desc.getAttribute('content') || '').trim() : '',
    canonical: canon ? abs(canon.getAttribute('href')) : '',
    h1: document.querySelectorAll('h1').length,
    hreflang, og,
    ld: [...document.querySelectorAll('script[type="application/ld+json" i]')].map(s => s.textContent),
  };
}`

// Live reads the loaded page's SEO facts and stores them with the run.
func (Module) Live(ctx context.Context, cc *check.Context, tab *rod.Page, p *model.Page) {
	if p.Status >= 400 || cc.Save == nil {
		return
	}
	res, err := tab.Context(ctx).Eval(factsJS)
	if err != nil {
		cc.Warn(fmt.Sprintf("seo: %s: %v", p.Path, err))
		return
	}
	var raw struct {
		Facts
		LD []string `json:"ld"`
	}
	if err := res.Value.Unmarshal(&raw); err != nil {
		cc.Warn(fmt.Sprintf("seo: %s: %v", p.Path, err))
		return
	}
	f := raw.Facts
	f.JSONLD, f.JSONLDErrors = types(raw.LD)
	if err := save(cc, pageFile(p), f); err != nil {
		cc.Warn(fmt.Sprintf("seo: %s: %v", p.Path, err))
	}
}

func pageFile(p *model.Page) string { return "seo/" + p.ID + ".json" }

const siteFile = "seo/site.json"

func save(cc *check.Context, name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return cc.Save(name, data)
}

// load reads a stored fact file; ok is false when the run has none (seo
// didn't run, or the page failed). A page ID comes from run.json, which
// may come from someone else, so the file is opened inside dir only.
func load(dir, name string, v any) bool {
	f, err := os.OpenInRoot(dir, filepath.FromSlash(name))
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	return json.NewDecoder(f).Decode(v) == nil
}

// types returns the schema.org types the JSON-LD blocks declare, nested
// ones included (an Offer inside a Product, the items of an @graph):
// losing AggregateRating loses the stars in search results just as
// losing Product does.
func types(blocks []string) ([]string, int) {
	var out []string
	bad := 0
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				if k == "@type" {
					for _, t := range strs(val) {
						if t = shortType(t); t != "" && !slices.Contains(out, t) {
							out = append(out, t)
						}
					}
					continue
				}
				walk(val)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	for _, b := range blocks {
		if strings.TrimSpace(b) == "" {
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(b), &v); err != nil {
			bad++
			continue
		}
		walk(v)
	}
	slices.Sort(out)
	return out, bad
}

func strs(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		var out []string
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// shortType drops a schema.org prefix: "https://schema.org/Product" and
// "schema:Product" are both Product.
func shortType(t string) string {
	t = strings.TrimSpace(t)
	for _, p := range []string{"http://schema.org/", "https://schema.org/", "schema:"} {
		if len(t) > len(p) && strings.EqualFold(t[:len(p)], p) {
			return t[len(p):]
		}
	}
	return t
}

func sameHost(a, b *url.URL) bool {
	return strings.EqualFold(strings.TrimPrefix(a.Host, "www."), strings.TrimPrefix(b.Host, "www."))
}
