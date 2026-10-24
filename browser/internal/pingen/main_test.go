// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeGoogle serves the two version lists and an archive per platform
// whose body is its own path, so each hash is predictable.
func fakeGoogle(t *testing.T, skip string, badURL bool) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entry := func(v string) string {
			var d []string
			for _, p := range Platforms {
				if p == skip {
					continue
				}
				u := fmt.Sprintf("%s/store/%s/%s/chrome-%s.zip", srv.URL, v, p, p)
				if badURL {
					u = "https://mirror.example/chrome.zip"
				}
				d = append(d, fmt.Sprintf(`{"platform":%q,"url":%q}`, p, u))
			}
			return fmt.Sprintf(`{"version":%q,"downloads":{"chrome":[%s]}}`, v, strings.Join(d, ","))
		}
		switch r.URL.Path {
		case "/idx/last-known-good-versions-with-downloads.json":
			fmt.Fprintf(w, `{"channels":{"Stable":%s,"Beta":%s}}`, entry("153.0.1.2"), entry("154.0.0.1"))
		case "/idx/known-good-versions-with-downloads.json":
			fmt.Fprintf(w, `{"versions":[%s,%s]}`, entry("150.0.0.9"), entry("153.0.1.2"))
		default:
			if strings.HasPrefix(r.URL.Path, "/store/") {
				_, _ = io.WriteString(w, r.URL.Path)
				return
			}
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func hashOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestRunPinsStable(t *testing.T) {
	srv := fakeGoogle(t, "", false)
	g := gen{client: srv.Client(), index: srv.URL + "/idx", storage: srv.URL + "/store", log: io.Discard}
	src, err := g.run(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	out := string(src)
	if !strings.Contains(out, `const cftVersion = "153.0.1.2"`) || !strings.Contains(out, "DO NOT EDIT") {
		t.Errorf("source:\n%s", out)
	}
	path := "/store/153.0.1.2/mac-arm64/chrome-mac-arm64.zip"
	want := fmt.Sprintf(`"mac-arm64":   {SHA256: %q, Size: %d},`, hashOf(path), len(path))
	if !strings.Contains(out, want) {
		t.Errorf("missing %s in:\n%s", want, out)
	}
	if strings.Count(out, "SHA256:") != len(Platforms) {
		t.Errorf("want %d pins:\n%s", len(Platforms), out)
	}
}

func TestRunPinsAVersion(t *testing.T) {
	srv := fakeGoogle(t, "linux-arm64", false)
	g := gen{client: srv.Client(), index: srv.URL + "/idx", storage: srv.URL + "/store", log: io.Discard}
	src, err := g.run(t.Context(), "150.0.0.9")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `"150.0.0.9"`) || strings.Contains(string(src), "linux-arm64") {
		t.Errorf("source:\n%s", src)
	}
	if _, err := g.run(t.Context(), "1.0.0.0"); err == nil {
		t.Error("pinned a version that isn't listed")
	}
}

func TestRunRefuses(t *testing.T) {
	srv := fakeGoogle(t, "win64", false)
	g := gen{client: srv.Client(), index: srv.URL + "/idx", storage: srv.URL + "/store", log: io.Discard}
	if _, err := g.run(t.Context(), ""); err == nil || !strings.Contains(err.Error(), "win64") {
		t.Errorf("missing platform: err = %v", err)
	}
	srv = fakeGoogle(t, "", true)
	g = gen{client: srv.Client(), index: srv.URL + "/idx", storage: srv.URL + "/store", log: io.Discard}
	if _, err := g.run(t.Context(), ""); err == nil || !strings.Contains(err.Error(), "mirror.example") {
		t.Errorf("archive off Google's storage: err = %v", err)
	}
}
