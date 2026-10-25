// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package capture

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/internal/testenv"
)

func TestStoredHeader(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"cache-control", "max-age=600, public", "max-age=600, public"},
		{"x-robots-tag", "noindex", "noindex"},
		{"www-authenticate", `Basic realm="Staging"`, `Basic realm="Staging"`},
		{"x-api-key", "sk_live_4eC39HqLyjWDarjtT1zdp7dc", "•••••"},
		{"x-debug-info", "stripe=sk_live_4eC39HqLyjWDarjtT1zdp7dc", "stripe=sk_l•••••"},
		{"x-debug", "Bearer abcdef123456", "Bearer abcdef123456"},
		{"x-forwarded-for", "10.0.0.1", "10.0.0.1"},
		{"x-auth-token", "plain-looking-value", "•••••"},
		{"x-session-id", "42", "•••••"},
		{"x-upstream", "https://admin:hunter2@origin.test/", "https://admin:hu•••••@origin.test/"},
		{"set-cookie", "sid=abc123; Path=/; HttpOnly\nwp_lang=en; Path=/", "sid=•••••; Path=/; HttpOnly\nwp_lang=•••••; Path=/"},
	} {
		if got := storedHeader(c.name, c.in); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// Every header a check reads from a page's stored headers must be kept
// as it came, or the check would read a masked value.
func TestKeptHeadersCoverChecks(t *testing.T) {
	read := regexp.MustCompile(`Headers\["([^"]+)"\]`)
	found := 0
	for _, dir := range []string{"../checks", "../compare", "../report", "../lighthouse"} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range read.FindAllStringSubmatch(string(src), -1) {
				found++
				if !keptHeaders[strings.ToLower(m[1])] {
					t.Errorf("%s reads the %q header, which capture stores masked: add it to keptHeaders", path, m[1])
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if found == 0 {
		t.Fatal("found no header reads at all: has the pattern changed?")
	}
}

// A key the site leaks in a response header doesn't reach run.json.
func TestVisitMasksHeaders(t *testing.T) {
	b := launch(t)
	const key = "sk_live_4eC39HqLyjWDarjtT1zdp7dc"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "max-age=600")
		w.Header().Set("X-Api-Key", key)
		w.Header().Set("Set-Cookie", "sid=s3cr3t-session; Path=/")
		fmt.Fprint(w, `<!doctype html><title>Leaky</title><p>Hello.</p>`)
	}))
	defer srv.Close()
	sess, err := b.Session()
	if err != nil {
		t.Fatal(err)
	}
	tab := newTab(t, sess)
	ctx := context.Background()
	opts := Options{Timeout: testenv.Timeout(20 * time.Second)}
	rec, err := Start(ctx, tab, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Stop()
	p, err := Visit(ctx, rec, srv.URL+"/", opts)
	if err != nil {
		t.Fatal(err)
	}
	if p.Headers["cache-control"] != "max-age=600" || p.Headers["x-api-key"] == "" {
		t.Errorf("headers: %v", p.Headers)
	}
	data, _ := json.Marshal(p)
	for _, secret := range []string{key, "s3cr3t-session"} {
		if strings.Contains(string(data), secret) {
			t.Errorf("the stored page has %q: %v", secret, p.Headers)
		}
	}
}

func TestHostError(t *testing.T) {
	site, _ := url.Parse("http://10.0.0.5:8080/")
	for _, c := range []struct {
		headers map[string]string
		fails   bool
	}{
		{nil, false},
		{map[string]string{"Authorization": "Bearer t"}, false},
		{map[string]string{"Host": "10.0.0.5:8080"}, false},
		{map[string]string{"host": " 10.0.0.5 "}, false},
		{map[string]string{"Host": "staging.example.com"}, true},
		{map[string]string{"HOST": "10.0.0.5:9090"}, true},
	} {
		if err := HostError(c.headers, site); (err != nil) != c.fails {
			t.Errorf("%v: %v", c.headers, err)
		}
	}
}

func TestHeaderNotes(t *testing.T) {
	got := HeaderNotes(map[string]string{"Authorization": "a", "Host": "h", "Cookie": "c=1", "Proxy-Authorization": "p", "connection": "close"})
	if len(got) != 2 || !strings.HasPrefix(got[0], "-H Proxy-Authorization is not sent") || !strings.HasPrefix(got[1], "-H connection is not sent") {
		t.Errorf("%q", got)
	}
	if got := HeaderNotes(map[string]string{"Authorization": "a"}); got != nil {
		t.Errorf("%q", got)
	}
}

// --cookie goes to the site's host and its www. or bare counterpart,
// which the run treats as the same site.
func TestCookieHosts(t *testing.T) {
	for raw, want := range map[string]string{
		"https://shop.test/":           "shop.test www.shop.test",
		"https://www.shop.test/":       "www.shop.test shop.test",
		"http://WWW.Shop.test:8080/":   "WWW.Shop.test:8080 Shop.test:8080",
		"http://staging.shop.test/":    "staging.shop.test www.staging.shop.test",
		"http://127.0.0.1:21200/":      "127.0.0.1:21200",
		"http://[::1]:21200/":          "[::1]:21200",
		"http://localhost:21200/":      "localhost:21200",
		"http://www.localhost:21200/":  "www.localhost:21200",
		"https://shop.test:443/a/b?c=": "shop.test:443 www.shop.test:443",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(cookieHosts(u), " "); got != want {
			t.Errorf("%s: %q, want %q", raw, got, want)
		}
	}
}
