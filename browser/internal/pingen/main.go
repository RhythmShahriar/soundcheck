// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Command pingen writes browser/cft_pins.go: the Chrome for Testing
// version Soundcheck downloads and the SHA-256 of each platform's
// archive. Run it when preparing a release (go generate ./browser); it
// reads Google's version list, downloads every archive once and hashes
// it, so the pins never come from the same place a user downloads from
// at run time.
//
//	go run ./browser/internal/pingen -o browser/cft_pins.go            # current Stable
//	go run ./browser/internal/pingen -o browser/cft_pins.go -version 153.0.8010.52
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"text/template"
	"time"
)

// Platforms are the Chrome for Testing builds Soundcheck pins. Each
// must have an archive in the chosen version, except linux-arm64, which
// Chrome for Testing only publishes from version 153.
var Platforms = []string{"linux64", "linux-arm64", "mac-arm64", "mac-x64", "win32", "win64"}

var optional = map[string]bool{"linux-arm64": true}

const (
	defaultIndex   = "https://googlechromelabs.github.io/chrome-for-testing"
	defaultStorage = "https://storage.googleapis.com/chrome-for-testing-public"
)

func main() {
	out := flag.String("o", "cft_pins.go", "file to write")
	version := flag.String("version", "", "pin this version instead of the current Stable")
	index := flag.String("index", defaultIndex, "where the version lists are published")
	storage := flag.String("storage", defaultStorage, "where the archives are published")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	g := gen{client: http.DefaultClient, index: *index, storage: *storage, log: os.Stderr}
	src, err := g.run(ctx, *version)
	if err == nil {
		err = os.WriteFile(*out, src, 0o600)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pingen:", err)
		os.Exit(1)
	}
}

type gen struct {
	client  *http.Client
	index   string
	storage string
	log     io.Writer
}

type download struct {
	Platform string `json:"platform"`
	URL      string `json:"url"`
}

type entry struct {
	Version   string `json:"version"`
	Downloads struct {
		Chrome []download `json:"chrome"`
	} `json:"downloads"`
}

// Pin is one platform's line in the generated file.
type Pin struct {
	Platform string
	SHA256   string
	Size     int64
}

// run picks the version, hashes its archives and returns the Go source.
func (g gen) run(ctx context.Context, version string) ([]byte, error) {
	e, err := g.pick(ctx, version)
	if err != nil {
		return nil, err
	}
	var pins []Pin
	for _, p := range Platforms {
		i := slices.IndexFunc(e.Downloads.Chrome, func(d download) bool { return d.Platform == p })
		if i < 0 {
			if optional[p] {
				continue
			}
			return nil, fmt.Errorf("version %s has no Chrome for Testing build for %s", e.Version, p)
		}
		// Only pin archives on the host the browser package downloads
		// from, at the path it builds.
		want := fmt.Sprintf("%s/%s/%s/chrome-%s.zip", strings.TrimSuffix(g.storage, "/"), e.Version, p, p)
		if got := e.Downloads.Chrome[i].URL; got != want {
			return nil, fmt.Errorf("%s: the list says %s, expected %s", p, got, want)
		}
		fmt.Fprintf(g.log, "hashing %s\n", want)
		sum, n, err := g.hash(ctx, want)
		if err != nil {
			return nil, err
		}
		pins = append(pins, Pin{Platform: p, SHA256: sum, Size: n})
	}
	return render(e.Version, pins)
}

// pick returns the requested version from the full list, or the current
// Stable one.
func (g gen) pick(ctx context.Context, version string) (entry, error) {
	if version != "" {
		var all struct {
			Versions []entry `json:"versions"`
		}
		if err := g.getJSON(ctx, g.index+"/known-good-versions-with-downloads.json", &all); err != nil {
			return entry{}, err
		}
		for _, e := range all.Versions {
			if e.Version == version {
				return e, nil
			}
		}
		return entry{}, fmt.Errorf("version %s isn't in the known-good list", version)
	}
	var last struct {
		Channels map[string]entry `json:"channels"`
	}
	if err := g.getJSON(ctx, g.index+"/last-known-good-versions-with-downloads.json", &last); err != nil {
		return entry{}, err
	}
	e, ok := last.Channels["Stable"]
	if !ok || e.Version == "" {
		return entry{}, errors.New("the version list has no Stable channel")
	}
	return e, nil
}

func (g gen) getJSON(ctx context.Context, url string, v any) error {
	body, err := g.get(ctx, url)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	if err := json.NewDecoder(body).Decode(v); err != nil {
		return fmt.Errorf("%s: %w", url, err)
	}
	return nil
}

func (g gen) hash(ctx context.Context, url string) (string, int64, error) {
	body, err := g.get(ctx, url)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = body.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, body)
	if err != nil {
		return "", 0, fmt.Errorf("%s: %w", url, err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func (g gen) get(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := g.client.Do(req) //nolint:gosec // URLs come from the flags and the version list
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return resp.Body, nil
}

var tmpl = template.Must(template.New("pins").Parse(`// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Code generated by browser/internal/pingen; DO NOT EDIT.

package browser

// cftVersion is the Chrome for Testing version this build downloads.
const cftVersion = "{{.Version}}"

// cftArchives holds the SHA-256 and size of each platform's archive.
var cftArchives = map[string]Archive{
{{- range .Pins}}
	"{{.Platform}}": {SHA256: "{{.SHA256}}", Size: {{.Size}}},
{{- end}}
}
`))

func render(version string, pins []Pin) ([]byte, error) {
	var b strings.Builder
	if err := tmpl.Execute(&b, struct {
		Version string
		Pins    []Pin
	}{version, pins}); err != nil {
		return nil, err
	}
	return format.Source([]byte(b.String()))
}
