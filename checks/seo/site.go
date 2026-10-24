// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package seo

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/RhythmShahriar/soundcheck/check"
)

// Site is what the site tells search engines as a whole.
type Site struct {
	Robots  Robots  `json:"robots"`
	Sitemap Sitemap `json:"sitemap"`
}

// Robots is the site's robots.txt, as it applies to every crawler (the
// "User-agent: *" group).
type Robots struct {
	Status    int      `json:"status"`               // 0 when it couldn't be fetched
	Disallow  []string `json:"disallow,omitempty"`   // sorted
	BlocksAll bool     `json:"blocks_all,omitempty"` // Disallow: / without an Allow that opens it again
	Sitemaps  []string `json:"sitemaps,omitempty"`
}

// Sitemap is the site's XML sitemap and how many pages it lists, sitemap
// indexes followed.
type Sitemap struct {
	URL    string `json:"url,omitempty"`
	Status int    `json:"status"`
	Pages  int    `json:"pages"`
	Files  int    `json:"files,omitempty"` // sitemap files read
	// Capped is set when the run stopped at maxFiles requests or maxTotal with
	// sitemaps left to read, so Pages is a lower bound that depends on
	// the order the index lists them in.
	Capped bool `json:"capped,omitempty"`
}

const (
	// maxFiles caps how many sitemap files one run reads; a big shop's
	// index can list hundreds while the count is only compared.
	maxFiles = 50
	// maxBytes caps one robots.txt or sitemap file. Google reads at most
	// 50 MB of a sitemap, so a bigger one is broken anyway.
	maxBytes = 50 << 20
	// maxTotal caps the sitemap bytes one run downloads, so a big index
	// can't make it fetch maxFiles times maxBytes.
	maxTotal = 100 << 20
)

// CheckSite fetches robots.txt and the sitemap and stores them with the
// run.
func (Module) CheckSite(ctx context.Context, cc *check.Context) {
	if cc.Save == nil || cc.Base == nil {
		return
	}
	root := &url.URL{Scheme: cc.Base.Scheme, Host: cc.Base.Host, Path: "/"}
	var s Site
	s.Robots = robots(ctx, cc, root)
	s.Sitemap = sitemap(ctx, cc, root, s.Robots.Sitemaps)
	if err := save(cc, siteFile, s); err != nil {
		cc.Warn("seo: " + err.Error())
	}
}

// fetch GETs raw and returns the status and up to maxBytes of the body.
func fetch(ctx context.Context, cc *check.Context, raw string) (int, []byte) {
	resp, err := cc.Get(ctx, http.MethodGet, raw)
	if err != nil {
		return 0, nil
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	return resp.StatusCode, body
}

func robots(ctx context.Context, cc *check.Context, root *url.URL) Robots {
	status, body := fetch(ctx, cc, root.JoinPath("robots.txt").String())
	r := Robots{Status: status}
	if status == http.StatusOK {
		r.Disallow, r.BlocksAll, r.Sitemaps = parseRobots(body)
	}
	return r
}

// parseRobots reads the rules of the groups for every crawler
// ("User-agent: *") and every Sitemap line. It follows Google's reading:
// consecutive User-agent lines share the rules after them. An empty
// Disallow allows everything.
func parseRobots(body []byte) (disallow []string, blocksAll bool, sitemaps []string) {
	var allow []string
	star, inAgents := false, false
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 64<<10), maxBytes)
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, val = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(val)
		switch key {
		case "user-agent":
			if !inAgents {
				star = false // a new group starts
			}
			inAgents = true
			star = star || val == "*"
			continue
		case "sitemap":
			if val != "" && !slices.Contains(sitemaps, val) {
				sitemaps = append(sitemaps, val)
			}
		case "disallow":
			if star && val != "" && !slices.Contains(disallow, val) {
				disallow = append(disallow, val)
			}
		case "allow":
			if star && val != "" {
				allow = append(allow, val)
			}
		}
		inAgents = false
	}
	slices.Sort(disallow)
	blocksAll = (slices.Contains(disallow, "/") || slices.Contains(disallow, "/*")) &&
		!slices.ContainsFunc(allow, func(a string) bool { return a == "/" || a == "/*" || a == "/$" })
	return disallow, blocksAll, sitemaps
}

// sitemap counts the pages in the sitemaps robots.txt names on the site's
// host, or in the first of the usual locations that answers. A 5xx at
// /sitemap.xml is the result: trying elsewhere would hide it.
func sitemap(ctx context.Context, cc *check.Context, root *url.URL, listed []string) Sitemap {
	var start []string
	for _, l := range listed {
		if u, err := url.Parse(l); err == nil && sameHost(u, root) && (u.Scheme == "http" || u.Scheme == "https") {
			start = append(start, l)
		}
	}
	if len(start) == 0 {
		for _, p := range []string{"sitemap.xml", "sitemap_index.xml", "wp-sitemap.xml"} {
			u := root.JoinPath(p).String()
			status, _ := head(ctx, cc, u)
			if status == http.StatusOK || status >= 500 {
				start = []string{u}
				break
			}
		}
		if len(start) == 0 {
			start = []string{root.JoinPath("sitemap.xml").String()}
		}
	}
	s := Sitemap{URL: start[0]}
	// Every request counts toward maxFiles, read or not: an index that
	// lists thousands of missing sitemaps must not get them all asked for.
	queue, seen, total, requests := slices.Clone(start), map[string]bool{}, 0, 0
	for len(queue) > 0 {
		u := queue[0]
		if seen[u] {
			queue = queue[1:]
			continue
		}
		if requests >= maxFiles || total >= maxTotal {
			s.Capped = true
			break
		}
		queue = queue[1:]
		seen[u] = true
		requests++
		status, body := fetch(ctx, cc, u)
		total += len(body)
		if u == s.URL {
			s.Status = status
		}
		if status != http.StatusOK {
			continue
		}
		s.Files++
		pages, children := parseSitemap(body)
		s.Pages += pages
		for _, c := range children {
			if cu, err := url.Parse(c); err == nil && sameHost(cu, root) {
				queue = append(queue, c)
			}
		}
	}
	return s
}

// head asks for a sitemap's status without its body, falling back to GET
// for servers that don't answer HEAD.
func head(ctx context.Context, cc *check.Context, raw string) (int, error) {
	for _, m := range []string{http.MethodHead, http.MethodGet} {
		resp, err := cc.Get(ctx, m, raw)
		if err != nil {
			return 0, err
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed && resp.StatusCode != http.StatusNotImplemented {
			return resp.StatusCode, nil
		}
	}
	return 0, fmt.Errorf("%s: no answer to HEAD or GET", raw)
}

// parseSitemap counts a urlset's <url> entries, or returns a sitemap
// index's <sitemap> locations. A gzipped file (sitemap.xml.gz) is
// unpacked first.
func parseSitemap(body []byte) (pages int, children []string) {
	var r io.Reader = bytes.NewReader(body)
	if len(body) > 2 && body[0] == 0x1f && body[1] == 0x8b {
		gz, err := gzip.NewReader(r)
		if err != nil {
			return 0, nil
		}
		r = io.LimitReader(gz, maxBytes)
	}
	dec := xml.NewDecoder(r)
	dec.Strict = false
	var path []string
	for {
		tok, err := dec.Token()
		if err != nil {
			return pages, children
		}
		switch t := tok.(type) {
		case xml.StartElement:
			path = append(path, t.Name.Local)
			if len(path) == 2 && path[0] == "urlset" && t.Name.Local == "url" {
				pages++
			}
		case xml.EndElement:
			if len(path) > 0 {
				path = path[:len(path)-1]
			}
		case xml.CharData:
			if len(path) == 3 && path[0] == "sitemapindex" && path[1] == "sitemap" && path[2] == "loc" {
				if loc := strings.TrimSpace(string(t)); loc != "" {
					children = append(children, loc)
				}
			}
		}
	}
}
