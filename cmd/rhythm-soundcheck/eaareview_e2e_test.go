// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// silentWAV is a WAV file of seconds of silence: 8 kHz, 8-bit mono.
func silentWAV(seconds int) []byte {
	const rate = 8000
	n := rate * seconds
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+n))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(rate), uint32(rate), uint16(1), uint16(8)} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(n))
	b.Write(bytes.Repeat([]byte{128}, n))
	return b.Bytes()
}

// reviewOnlySite serves a page that fails 1.4.2 and 2.4.1 in ways only
// axe's review-only rules see: 20 seconds of audio that plays on load
// with no controls; and 30 links with no skip link, landmark or heading
// to get past them. Its two frames share a title (axe's review-only
// frame-title-unique, a 4.1.2 rule).
func reviewOnlySite(t *testing.T) *httptest.Server {
	t.Helper()
	wav := silentWAV(20)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		var links strings.Builder
		for i := range 30 {
			fmt.Fprintf(&links, `<a href="/#l%d">Link %d</a> `, i, i)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Radio</title><link rel="icon" href="data:,"></head><body>
<div>%s</div><audio autoplay src="/long.wav"></audio>
<iframe title="Same" src="/frame.html"></iframe><iframe title="Same" src="/frame.html?b"></iframe>
<p>Now playing.</p></body></html>`, links.String())
	})
	mux.HandleFunc("/frame.html", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html lang="en"><head><title>Frame</title></head><body><p>Frame</p></body></html>`)
	})
	mux.HandleFunc("/long.wav", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		http.ServeContent(w, r, "long.wav", time.Time{}, bytes.NewReader(wav))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// A page with autoplaying audio and no way past its links: axe's
// no-autoplay-audio and bypass can only ask for review, so they find no
// failure. The statement draft must not read that as "no failures found
// by these tests" for 1.4.2 and 2.4.1: both need a person. 4.1.2 doesn't
// claim the review-only rules as its tests.
func TestEAAReviewOnlyCriteria(t *testing.T) {
	needBrowser(t)
	if testing.Short() {
		t.Skip("runs Chrome")
	}
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_NO_SANDBOX", "")
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("CI", "1")
	site := reviewOnlySite(t)
	writeConfig(t, `statement:
  owner: Example Radio Ltd
  website: https://radio.example
  contact:
    email: access@radio.example
`)
	code, out, errOut := runCLI(t, append([]string{"run", site.URL + "/", "--eaa", "--limit", "3"}, eaaCommon...)...)
	if code != exitOK && code != exitFindings {
		t.Fatalf("run: exit %d\n%s\n%s", code, out, errOut)
	}
	// The keyboard walk saw the long run of stops: the page was tested.
	if !strings.Contains(out, "No skip link before a long run of stops") {
		t.Errorf("no skip-link notice, so the page wasn't walked:\n%s", out)
	}
	code, out, errOut = runCLI(t, "statement", "-o", "s.md")
	if code != exitOK {
		t.Fatalf("statement: exit %d\n%s\n%s", code, out, errOut)
	}
	data, err := os.ReadFile("s.md")
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]string{}
	for line := range strings.SplitSeq(string(data), "\n") {
		for _, id := range []string{"1.4.2 ", "2.4.1 ", "4.1.2 "} {
			if strings.HasPrefix(line, "| "+id) {
				rows[strings.TrimSpace(id)] = line
			}
		}
	}
	for id, want := range map[string]string{
		"1.4.2": "| not tested: needs a person | not tested: needs a person |",
		"2.4.1": "| partly: keyboard/skip-link-broken; a person checks the rest | not tested: needs a person |",
	} {
		if !strings.HasSuffix(rows[id], want) {
			t.Errorf("%s row %q, want it to end %q", id, rows[id], want)
		}
	}
	for _, rule := range []string{"frame-title-unique", "duplicate-id-aria", "aria-braille-equivalent"} {
		if r := rows["4.1.2"]; r == "" || strings.Contains(r, rule) {
			t.Errorf("4.1.2 row %q lists %s, which can't fail", r, rule)
		}
	}
}
