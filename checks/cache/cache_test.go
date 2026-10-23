package cache

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/ratelimit"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
)

// fakeCache emulates a cache layer: the first request for a URL is a
// miss and stores the render; later ones are hits with an Age. Query
// strings are part of the key, so ?soundcheck= renders fresh.
type fakeCache struct {
	mu     sync.Mutex
	stored map[string]time.Time
}

func (c *fakeCache) seen(key string) (time.Duration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stored == nil {
		c.stored = map[string]time.Time{}
	}
	at, ok := c.stored[key]
	if !ok {
		c.stored[key] = time.Now()
	}
	return time.Since(at), ok
}

// layered serves a page through one emulated cache layer, setting the
// headers that layer sends on a miss and on a hit.
func layered(c *fakeCache, body string, miss, hit func(h http.Header, age int)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		age, ok := c.seen(r.URL.String())
		if ok {
			hit(w.Header(), int(age.Seconds())+1)
		} else {
			miss(w.Header(), 0)
		}
		fmt.Fprint(w, body)
	}
}

func page(title, main string) string {
	return `<!doctype html><html><head><title>` + title + `</title><link rel="https://api.w.org/" href="/wp-json/"></head><body class="page">` +
		`<nav>Home Shop</nav><main><h1>` + title + `</h1>` + main + `</main></body></html>`
}

func set(kv ...string) func(http.Header, int) {
	return func(h http.Header, _ int) {
		for i := 0; i+1 < len(kv); i += 2 {
			h.Set(kv[i], kv[i+1])
		}
	}
}

func withAge(name, value string) func(http.Header, int) {
	return func(h http.Header, age int) {
		h.Set(name, value)
		h.Set("Age", strconv.Itoa(age))
	}
}

// shop is a WordPress-like site where each page sits behind a different
// cache layer, plus the cases the module must catch.
func shop(t *testing.T) (*httptest.Server, []string) {
	t.Helper()
	c := &fakeCache{}
	mux := http.NewServeMux()
	var base string
	mux.HandleFunc("/{$}", layered(c, page("Home", "<p>Fresh coffee, roasted daily.</p>"),
		withAge("cf-cache-status", "MISS"), withAge("cf-cache-status", "HIT")))
	mux.HandleFunc("/varnish/", func(w http.ResponseWriter, r *http.Request) {
		_, ok := c.seen(r.URL.String())
		w.Header().Set("X-Varnish", "32770")
		if ok {
			w.Header().Set("X-Varnish", "32771 32770")
		}
		fmt.Fprint(w, page("Varnish", "<p>Served by Varnish.</p>"))
	})
	mux.HandleFunc("/litespeed/", layered(c, page("LiteSpeed", "<p>LiteSpeed page.</p>"),
		set("X-LiteSpeed-Cache", "miss"), set("X-LiteSpeed-Cache", "hit")))
	mux.HandleFunc("/nginx/", layered(c, page("Nginx", "<p>nginx page.</p>"),
		set("X-FastCGI-Cache", "MISS"), set("X-FastCGI-Cache", "HIT")))
	mux.HandleFunc("/rocket/", func(w http.ResponseWriter, r *http.Request) {
		// The cached file was written a minute before the warm-up visit.
		age, ok := c.seen(r.URL.String())
		at := time.Now().Add(-age - time.Minute)
		if !ok || r.URL.RawQuery != "" {
			at = time.Now()
		}
		fmt.Fprintf(w, "%s\n<!-- This website is like a Rocket, isn't it? Performance optimized by WP Rocket. Learn more: https://wp-rocket.me - Debug: cached@%d -->",
			page("Rocket", "<p>Rocket page.</p>"), at.Unix())
	})
	// A WooCommerce cart that a cache serves to everyone.
	mux.HandleFunc("/cart/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Cache", "HIT")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("Age", "600")
		fmt.Fprint(w, strings.Replace(page("Cart", "<p>Your cart is empty.</p>"), `class="page"`, `class="page woocommerce-cart"`, 1))
	})
	// A checkout that's correctly never cached.
	mux.HandleFunc("/checkout/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, must-revalidate, max-age=0, no-store, private")
		w.Header().Set("X-Cache", "MISS")
		fmt.Fprint(w, page("Checkout", "<p>Checkout</p>"))
	})
	// An account page under a custom slug, found by its body class.
	mux.HandleFunc("/konto/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Proxy-Cache", "HIT")
		fmt.Fprint(w, strings.Replace(page("Konto", "<p>Login</p>"), `class="page"`, `class="page woocommerce-account"`, 1))
	})
	// Old cached copies that still carry nonces.
	nonced := page("Contact", `<form><input type="hidden" name="_wpnonce" value="0a1b2c3d4e"></form><p>Write to us.</p>`)
	mux.HandleFunc("/contact/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("cf-cache-status", "HIT")
		w.Header().Set("Age", strconv.Itoa(20*3600))
		fmt.Fprint(w, nonced)
	})
	mux.HandleFunc("/old-shop/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Cache", "HIT")
		w.Header().Set("Age", strconv.Itoa(30*3600))
		fmt.Fprint(w, strings.Replace(page("Old", "<p>Old shop.</p>"), "</head>",
			`<script>var wpApiSettings = {"root":"/wp-json/","nonce":"9a8b7c6d5e"};</script></head>`, 1))
	})
	mux.HandleFunc("/fresh-form/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Cache", "HIT")
		w.Header().Set("Age", "60")
		fmt.Fprint(w, nonced)
	})
	// A stale HIT: the cache still has yesterday's price.
	mux.HandleFunc("/grinder/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			w.Header().Set("X-Proxy-Cache", "MISS")
			fmt.Fprint(w, page("Grinder", "<p>Espresso grinder $149. Sold out. Updated 10:02 am.</p>"))
			return
		}
		w.Header().Set("X-Proxy-Cache", "HIT")
		w.Header().Set("Age", "7200")
		fmt.Fprint(w, page("Grinder", "<p>Espresso grinder $129. In stock. Updated 08:12 am.</p>"))
	})
	// Random content on every render isn't stale.
	n := 0
	var nmu sync.Mutex
	mux.HandleFunc("/quotes/", func(w http.ResponseWriter, r *http.Request) {
		nmu.Lock()
		n++
		q := []string{"Coffee is life", "Tea is calm", "Cocoa is joy"}[n%3]
		nmu.Unlock()
		w.Header().Set("X-Proxy-Cache", "HIT")
		if r.URL.RawQuery != "" {
			w.Header().Set("X-Proxy-Cache", "MISS")
		}
		fmt.Fprint(w, page("Quotes", "<p>"+q+"</p>"))
	})
	// WordPress' login page, which the crawl never visits, and a basket
	// URL announced in WooCommerce's script settings.
	mux.HandleFunc("/wp-login.php", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("cf-cache-status", "HIT")
		w.Header().Set("Age", "30")
		fmt.Fprint(w, `<html><body class="login"><form name="loginform"></form></body></html>`)
	})
	mux.HandleFunc("/shop/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Proxy-Cache", "MISS")
		fmt.Fprintf(w, `%s<script>var wc_add_to_cart_params = {"cart_url":"%s\/basket\/","is_cart":""};</script>`,
			page("Shop", "<p>Beans.</p>"), strings.ReplaceAll(base, "/", `\/`))
	})
	mux.HandleFunc("/basket/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprint(w, page("Basket", "<p>Empty</p>"))
	})
	srv := httptest.NewServer(mux)
	base = srv.URL
	t.Cleanup(srv.Close)
	return srv, []string{"/", "/varnish/", "/litespeed/", "/nginx/", "/rocket/", "/cart/", "/checkout/", "/konto/",
		"/contact/", "/old-shop/", "/fresh-form/", "/grinder/", "/quotes/", "/shop/"}
}

func context4(t *testing.T, srv *httptest.Server, paths []string) *check.Context {
	t.Helper()
	base, _ := url.Parse(srv.URL + "/")
	run := &model.Run{}
	for _, p := range paths {
		run.Pages = append(run.Pages, model.Page{URL: srv.URL + p, Path: p, Group: p, Status: 200,
			Headers: map[string]string{"content-type": "text/html; charset=UTF-8"}})
	}
	return &check.Context{Config: config.Default(), Base: base, Run: run, HTTP: srv.Client(), Hosts: ratelimit.NewHosts(0)}
}

func issuesOf(p *model.Page) map[string]model.Issue {
	m := map[string]model.Issue{}
	for _, i := range p.Issues {
		m[i.Title] = i
	}
	return m
}

func TestDoubleFetch(t *testing.T) {
	srv, paths := shop(t)
	cc := context4(t, srv, paths)
	// The browser visit warms each cache once before the module runs.
	for _, p := range paths {
		resp, err := srv.Client().Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	Module{}.CheckSite(context.Background(), cc)

	want := map[string][2]string{ // status, layer
		"/":           {"hit", "Cloudflare"},
		"/varnish/":   {"hit", "Varnish"},
		"/litespeed/": {"hit", "LiteSpeed"},
		"/nginx/":     {"hit", "nginx"},
		"/rocket/":    {"hit", "WP Rocket"},
		"/checkout/":  {"miss", "proxy"},
		"/shop/":      {"miss", "nginx"},
	}
	for path, w := range want {
		p := cc.Run.Page(path)
		if p.Cache["status"] != w[0] || p.Cache["layer"] != w[1] {
			t.Errorf("%s: cache = %v, want %v", path, p.Cache, w)
		}
	}
	if home := cc.Run.Page("/").Cache; home["age"] == "" || home["ttfb_cached_ms"] == "" || home["ttfb_uncached_ms"] == "" || home["header"] != "cf-cache-status: HIT" {
		t.Errorf("home facts = %v", home)
	}

	// Private pages.
	if i, ok := issuesOf(cc.Run.Page("/cart/"))["Private page is served from cache"]; !ok || i.Level != model.Error || !strings.Contains(i.Detail, "x-cache: HIT") {
		t.Errorf("cart: %v", cc.Run.Page("/cart/").Issues)
	}
	if _, ok := issuesOf(cc.Run.Page("/konto/"))["Private page is served from cache"]; !ok {
		t.Errorf("account by body class: %v", cc.Run.Page("/konto/").Issues)
	}
	if len(cc.Run.Page("/checkout/").Issues) != 0 {
		t.Errorf("uncached checkout: %v", cc.Run.Page("/checkout/").Issues)
	}
	var login, basket bool
	for _, i := range cc.Run.Issues {
		if i.Title == "Private page is served from cache" && strings.Join(i.Pages, ",") == "/wp-login.php" {
			login = true
		}
		if strings.Join(i.Pages, ",") == "/basket/" {
			basket = true
		}
	}
	if !login || basket {
		t.Errorf("probed private pages: login cached %v, basket reported %v: %v", login, basket, cc.Run.Issues)
	}

	// Expired nonces.
	const expired = "Cached page carries an expired security token"
	if i, ok := issuesOf(cc.Run.Page("/contact/"))[expired]; !ok || i.Level != model.Warning || !strings.Contains(i.Detail, "_wpnonce") {
		t.Errorf("contact (20 h): %v", cc.Run.Page("/contact/").Issues)
	}
	if i, ok := issuesOf(cc.Run.Page("/old-shop/"))[expired]; !ok || i.Level != model.Error || !strings.Contains(i.Detail, "wpApiSettings nonce") {
		t.Errorf("old shop (30 h): %v", cc.Run.Page("/old-shop/").Issues)
	}
	if _, ok := issuesOf(cc.Run.Page("/fresh-form/"))[expired]; ok {
		t.Error("a 1 minute old copy was reported")
	}

	// Stale cache.
	const stale = "Cached page is out of date"
	if i, ok := issuesOf(cc.Run.Page("/grinder/"))[stale]; !ok || !strings.Contains(i.Detail, "$129") || !strings.Contains(i.Detail, "$149") {
		t.Errorf("grinder: %v", cc.Run.Page("/grinder/").Issues)
	}
	for _, p := range []string{"/quotes/", "/", "/rocket/", "/nginx/"} {
		if _, ok := issuesOf(cc.Run.Page(p))[stale]; ok {
			t.Errorf("%s reported stale", p)
		}
	}

	if cc.Run.Cache["hit_rate"] == "" || cc.Run.Cache["pages"] == "" {
		t.Errorf("summary = %v", cc.Run.Cache)
	}
	if len(cc.Run.Warnings) != 1 || !strings.Contains(cc.Run.Warnings[0], "SOUNDCHECK_USER") {
		t.Errorf("warnings = %v", cc.Run.Warnings)
	}
}

func TestSiteVerdicts(t *testing.T) {
	cases := []struct {
		name  string
		h     map[string]string
		title string
		level model.Level
	}{
		{"no cache", map[string]string{"Cache-Control": "no-cache"}, "No page cache detected", model.Notice},
		{"server cache never hits", map[string]string{"X-FastCGI-Cache": "BYPASS"}, "Pages are never served from the page cache", model.Warning},
		{"cdn passes html", map[string]string{"cf-cache-status": "DYNAMIC"}, "Pages are never served from the page cache", model.Notice},
		{"working", map[string]string{"X-LiteSpeed-Cache": "hit"}, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/wp-login.php" {
					http.NotFound(w, r)
					return
				}
				for k, v := range c.h {
					w.Header().Set(k, v)
				}
				fmt.Fprint(w, page("A", "<p>a</p>"))
			}))
			defer srv.Close()
			cc := context4(t, srv, []string{"/", "/a/"})
			Module{}.CheckSite(context.Background(), cc)
			if c.title == "" {
				if len(cc.Run.Issues) != 0 {
					t.Errorf("issues: %v", cc.Run.Issues)
				}
				if cc.Run.Cache["hit_rate"] != "100" {
					t.Errorf("summary = %v", cc.Run.Cache)
				}
				return
			}
			if len(cc.Run.Issues) != 1 || cc.Run.Issues[0].Title != c.title || cc.Run.Issues[0].Level != c.level {
				t.Errorf("issues: %+v", cc.Run.Issues)
			}
		})
	}
}

// A super cache stamp in local time is read once the REST index gives
// the site's offset.
func TestLocalStampThroughREST(t *testing.T) {
	stamp := time.Now().UTC().Add(2*time.Hour - 30*time.Hour) // UTC+2 site, cached 30 h ago
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wp-json/" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"name":"Shop","gmt_offset":"2","timezone_string":"Europe/Berlin"}`)
			return
		}
		fmt.Fprintf(w, "%s\n<!-- Cached page generated by WP-Super-Cache on %s -->", page("Form", `<input name="_wpnonce" value="aaaaaaaaaa">`), stamp.Format(time.DateTime))
	}))
	defer srv.Close()
	cc := context4(t, srv, []string{"/form/"})
	Module{}.CheckSite(context.Background(), cc)
	p := cc.Run.Page("/form/")
	if p.Cache["status"] != "hit" || p.Cache["layer"] != "WP Super Cache" {
		t.Fatalf("cache = %v", p.Cache)
	}
	if age, _ := strconv.Atoi(p.Cache["age"]); age < 29*3600 || age > 31*3600 {
		t.Errorf("age = %d", age)
	}
	if i, ok := issuesOf(p)["Cached page carries an expired security token"]; !ok || i.Level != model.Error {
		t.Errorf("issues = %v", p.Issues)
	}
}

func TestCompare(t *testing.T) {
	mk := func(statuses ...string) *model.Run {
		r := &model.Run{}
		for i, s := range statuses {
			r.Pages = append(r.Pages, model.Page{Path: fmt.Sprintf("/%d/", i), Cache: map[string]string{"status": s, "layer": "LiteSpeed"}})
		}
		return r
	}
	got := Module{}.Compare(context.Background(), &check.CompareContext{Before: mk("hit", "hit", "miss"), After: mk("miss", "bypass", "miss")})
	if len(got) != 1 || got[0].Class != model.Changed || got[0].Title != "Page cache stopped working" ||
		got[0].Before != "2 of 3 pages cached" || got[0].After != "0 of 3 pages cached" || len(got[0].Pages) != 2 {
		t.Errorf("changes = %+v", got)
	}
	for _, after := range []*model.Run{mk("hit", "miss", "miss"), mk("miss")} {
		before := mk("hit", "hit", "miss")
		if after.Pages[0].Cache["status"] == "miss" && len(after.Pages) == 1 {
			before = mk("miss")
		}
		if got := (Module{}).Compare(context.Background(), &check.CompareContext{Before: before, After: after}); len(got) != 0 {
			t.Errorf("unexpected %+v", got)
		}
	}
}
