// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package seo

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/rhythm/kit/ratelimit"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

// serve starts h on the first free port of 127.0.0.1:20320-20339, the
// range set aside for these fixtures.
func serve(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	for port := 20320; port < 20340; port++ {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		srv := httptest.NewUnstartedServer(h)
		_ = srv.Listener.Close()
		srv.Listener = l
		srv.Start()
		t.Cleanup(srv.Close)
		return srv
	}
	t.Fatal("no free port in 20320-20339")
	return nil
}

func TestTypes(t *testing.T) {
	got, bad := types([]string{
		`{"@context":"https://schema.org","@type":"Product","offers":{"@type":"Offer"},"aggregateRating":{"@type":"AggregateRating"}}`,
		`{"@context":"https://schema.org","@graph":[{"@type":["WebPage","ItemPage"]},{"@type":"http://schema.org/BreadcrumbList"}]}`,
		`[{"@type":"schema:Organization"},{"@type":"Product"}]`,
		`{"@type": "Product",}`, // a trailing comma: invalid
		"  ",
	})
	want := []string{"AggregateRating", "BreadcrumbList", "ItemPage", "Offer", "Organization", "Product", "WebPage"}
	if !slices.Equal(got, want) || bad != 1 {
		t.Errorf("types = %v (%d invalid), want %v (1 invalid)", got, bad, want)
	}
}

func TestParseRobots(t *testing.T) {
	for _, c := range []struct {
		body     string
		disallow []string
		all      bool
		sitemaps []string
	}{
		{"User-agent: *\nDisallow: /wp-admin/\nAllow: /wp-admin/admin-ajax.php\n\nSitemap: https://shop.test/wp-sitemap.xml\n",
			[]string{"/wp-admin/"}, false, []string{"https://shop.test/wp-sitemap.xml"}},
		{"User-agent: *\nDisallow: /\n", []string{"/"}, true, nil},
		{"user-agent: *\ndisallow: /  # staging\nallow: /wp-admin/admin-ajax.php\n", []string{"/"}, true, nil},
		// Consecutive User-agent lines share their rules.
		{"User-agent: Googlebot\nUser-agent: *\nDisallow: /*\n", []string{"/*"}, true, nil},
		// Rules for one crawler aren't the site's.
		{"User-agent: BadBot\nDisallow: /\n\nUser-agent: *\nDisallow:\n", nil, false, nil},
		{"User-agent: *\nDisallow: /\nAllow: /$\n", []string{"/"}, false, nil},
		{"", nil, false, nil},
	} {
		d, all, sm := parseRobots([]byte(c.body))
		if !slices.Equal(d, c.disallow) || all != c.all || !slices.Equal(sm, c.sitemaps) {
			t.Errorf("parseRobots(%q) = %v %v %v, want %v %v %v", c.body, d, all, sm, c.disallow, c.all, c.sitemaps)
		}
	}
}

func TestParseSitemap(t *testing.T) {
	n, kids := parseSitemap([]byte(`<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
<url><loc>https://shop.test/</loc></url><url><loc>https://shop.test/a/</loc><image:image><image:loc>x</image:loc></image:image></url></urlset>`))
	if n != 2 || kids != nil {
		t.Errorf("urlset = %d %v", n, kids)
	}
	n, kids = parseSitemap([]byte(`<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
<sitemap><loc> https://shop.test/post-sitemap.xml </loc></sitemap><sitemap><loc>https://shop.test/page-sitemap.xml</loc></sitemap></sitemapindex>`))
	if n != 0 || !slices.Equal(kids, []string{"https://shop.test/post-sitemap.xml", "https://shop.test/page-sitemap.xml"}) {
		t.Errorf("index = %d %v", n, kids)
	}
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	_, _ = w.Write([]byte(`<urlset><url><loc>https://shop.test/</loc></url></urlset>`))
	_ = w.Close()
	if n, _ := parseSitemap(gz.Bytes()); n != 1 {
		t.Errorf("gzipped = %d", n)
	}
	if n, _ := parseSitemap([]byte("<html><body>Not found</body></html>")); n != 0 {
		t.Errorf("html = %d", n)
	}
}

// site serves robots.txt and sitemaps as an SEO plugin does. {site} in
// a body is the server's own address; "500" answers with that error.
func site(t *testing.T, robots string, routes map[string]string) *httptest.Server {
	t.Helper()
	return serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		self := "http://" + r.Host
		if r.URL.Path == "/robots.txt" && robots != "" {
			_, _ = fmt.Fprint(w, strings.ReplaceAll(robots, "{site}", self))
			return
		}
		if body, ok := routes[r.URL.Path]; ok {
			body = strings.ReplaceAll(body, "{site}", self)
			if body == "500" {
				http.Error(w, "oops", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/xml")
			_, _ = fmt.Fprint(w, body)
			return
		}
		http.NotFound(w, r)
	}))
}

func checkSite(t *testing.T, srv *httptest.Server) Site {
	t.Helper()
	base, _ := url.Parse(srv.URL + "/blog/")
	dir := t.TempDir()
	cc := &check.Context{Base: base, Run: &model.Run{}, HTTP: srv.Client(), Hosts: ratelimit.NewHosts(0), Save: store.Saver(dir)}
	Module{}.CheckSite(context.Background(), cc)
	var s Site
	data, err := os.ReadFile(filepath.Join(dir, "seo", "site.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	if len(cc.Run.Warnings) > 0 {
		t.Errorf("warnings: %v", cc.Run.Warnings)
	}
	return s
}

func TestCheckSite(t *testing.T) {
	// The index also names sitemaps on another host, which are never
	// requested.
	srv := site(t, "User-agent: *\nDisallow: /wp-admin/\n\nSitemap: {site}/sitemap_index.xml\nSitemap: https://elsewhere.test/sitemap.xml\n",
		map[string]string{
			"/sitemap_index.xml": `<sitemapindex><sitemap><loc>{site}/post-sitemap.xml</loc></sitemap>` +
				`<sitemap><loc>{site}/page-sitemap.xml</loc></sitemap><sitemap><loc>https://elsewhere.test/x.xml</loc></sitemap></sitemapindex>`,
			"/post-sitemap.xml": `<urlset><url><loc>a</loc></url><url><loc>b</loc></url><url><loc>c</loc></url></urlset>`,
			"/page-sitemap.xml": `<urlset><url><loc>d</loc></url></urlset>`,
		})
	s := checkSite(t, srv)
	if s.Robots.Status != 200 || !slices.Equal(s.Robots.Disallow, []string{"/wp-admin/"}) || s.Robots.BlocksAll {
		t.Errorf("robots = %+v", s.Robots)
	}
	if s.Sitemap.URL != srv.URL+"/sitemap_index.xml" || s.Sitemap.Status != 200 || s.Sitemap.Pages != 4 || s.Sitemap.Files != 3 {
		t.Errorf("sitemap = %+v", s.Sitemap)
	}
}

// An index with more sitemaps than one run reads stops at maxFiles and
// says so.
func TestCheckSiteCapsTheSitemaps(t *testing.T) {
	files := map[string]string{}
	var index strings.Builder
	index.WriteString("<sitemapindex>")
	for i := range maxFiles + 5 {
		path := fmt.Sprintf("/s%d.xml", i)
		index.WriteString("<sitemap><loc>{site}" + path + "</loc></sitemap>")
		files[path] = `<urlset><url><loc>a</loc></url></urlset>`
	}
	files["/sitemap_index.xml"] = index.String() + "</sitemapindex>"
	s := checkSite(t, site(t, "Sitemap: {site}/sitemap_index.xml\n", files))
	if !s.Sitemap.Capped || s.Sitemap.Files != maxFiles || s.Sitemap.Pages != maxFiles-1 {
		t.Errorf("sitemap = %+v", s.Sitemap)
	}
	// A sitemap read in full isn't capped.
	s = checkSite(t, site(t, "", map[string]string{"/sitemap.xml": `<urlset><url><loc>a</loc></url></urlset>`}))
	if s.Sitemap.Capped {
		t.Errorf("small sitemap = %+v", s.Sitemap)
	}
}

func TestCheckSiteFindsTheSitemap(t *testing.T) {
	// No robots.txt: WordPress's own sitemap is found by its usual name.
	s := checkSite(t, site(t, "", map[string]string{"/wp-sitemap.xml": `<urlset><url><loc>a</loc></url></urlset>`}))
	if s.Robots.Status != 404 || s.Sitemap.Status != 200 || s.Sitemap.Pages != 1 {
		t.Errorf("site = %+v", s)
	}
	// A failing /sitemap.xml is the result, not a reason to look elsewhere.
	s = checkSite(t, site(t, "User-agent: *\nDisallow: /\n", map[string]string{"/sitemap.xml": "500", "/wp-sitemap.xml": `<urlset/>`}))
	if !s.Robots.BlocksAll || s.Sitemap.Status != 500 || s.Sitemap.Pages != 0 {
		t.Errorf("site = %+v", s)
	}
	s = checkSite(t, site(t, "", nil))
	if s.Sitemap.Status != 404 || s.Sitemap.Pages != 0 {
		t.Errorf("no sitemap = %+v", s)
	}
}

// A received run.json can't make Compare read files outside its folder.
func TestLoadStaysInTheRunFolder(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "run")
	if err := os.MkdirAll(filepath.Join(dir, "seo"), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{filepath.Join(parent, "secret.json"), filepath.Join(dir, "seo", "ok.json")} {
		if err := os.WriteFile(f, []byte(`{"h1": 1}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var f Facts
	if !load(dir, "seo/ok.json", &f) || f.H1 != 1 {
		t.Error("a file in the run folder must load")
	}
	if load(dir, "seo/../../secret.json", &f) {
		t.Error("a path out of the run folder must not load")
	}
}
