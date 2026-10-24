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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestStoredHeader(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"cache-control", "max-age=600, public", "max-age=600, public"},
		{"x-robots-tag", "noindex", "noindex"},
		{"x-api-key", "sk_live_4eC39HqLyjWDarjtT1zdp7dc", "•••••"},
		{"x-debug-info", "stripe=sk_live_4eC39HqLyjWDarjtT1zdp7dc", "stripe=sk_live_•••••"},
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
	tab, err := sess.Page(protoBlank())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	opts := Options{Timeout: 20 * time.Second}
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
