// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
)

// titled serves pages whose titles can be changed while it runs.
type titled struct {
	*httptest.Server
	mu     sync.Mutex
	titles map[string]string
}

func newTitled(t *testing.T) *titled {
	s := &titled{titles: map[string]string{"/": "Home", "/team/": "Team", "/shop/": "Shop"}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		title, ok := s.titles[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `<!doctype html><html><head><title>%s</title><link rel="icon" href="data:,"></head>
<body><a href="/">Home</a> <a href="/team/">Team</a> <a href="/shop/">Shop</a><p>%s</p></body></html>`, title, strings.Repeat("Words. ", 80))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *titled) set(path, title string) {
	s.mu.Lock()
	s.titles[path] = title
	s.mu.Unlock()
}

// compareJSON runs compare and returns the comparison it wrote.
func compareJSON(t *testing.T, args ...string) (int, *model.Comparison) {
	t.Helper()
	code, out, errOut := runCLI(t, append([]string{"compare", "--json"}, args...)...)
	var c model.Comparison
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		t.Fatalf("compare: exit %d: %v\n%s\n%s", code, err, out, errOut)
	}
	return code, &c
}

func changedPages(c *model.Comparison) map[string]string {
	got := map[string]string{}
	for _, ch := range c.Changes {
		if ch.Class == model.Changed && ch.Check == "health/title" {
			got[ch.Pages[0]] = ch.ID
		}
	}
	return got
}

// The plan's end-to-end test: snapshot, change two pages, compare, accept
// one by id, compare again: that one is clean, the other still reports.
// Then an ignore rule hides it until its date. After that it counts again.
func TestAcceptAndIgnore(t *testing.T) {
	needBrowser(t)
	t.Chdir(t.TempDir())
	site := newTitled(t)
	common := []string{"--no-download", "--viewport", "desktop", "--modules", "health", "--rate", "50", "--plain"}
	if code, out, errOut := runCLI(t, append([]string{"snapshot", site.URL, "--name", "good"}, common...)...); code != exitOK {
		t.Fatalf("snapshot: exit %d\n%s\n%s", code, out, errOut)
	}
	site.set("/team/", "Our team")
	site.set("/shop/", "Our shop")
	_, c := compareJSON(t, common...)
	got := changedPages(c)
	if len(got) != 2 {
		t.Fatalf("want two title changes, got %+v", c.Changes)
	}
	code, out, errOut := runCLI(t, "accept", got["/team/"])
	if code != exitOK || !strings.Contains(out, "Accepted "+got["/team/"]+" Page title changed (/team/)") {
		t.Fatalf("accept: exit %d\n%s\n%s", code, out, errOut)
	}
	_, c = compareJSON(t, common...)
	if got := changedPages(c); len(got) != 1 || got["/shop/"] == "" {
		t.Errorf("after accept: %+v", c.Changes)
	}
	// The plain view says how to accept what's left.
	_, out, _ = runCLI(t, append([]string{"compare"}, common...)...)
	if !strings.Contains(out, "rhythm-soundcheck accept ") || !strings.Contains(out, " if this is expected") {
		t.Errorf("no accept hint:\n%s", out)
	}

	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	writeConfig(t, "ignore:\n  - check: health/title\n    pages: [/shop/*]\n    until: \""+tomorrow+"\"\n    reason: new shop name\n")
	_, c = compareJSON(t, common...)
	if len(changedPages(c)) != 0 || len(c.Ignored) != 1 {
		t.Errorf("ignored: changes %+v ignored %+v", c.Changes, c.Ignored)
	}
	writeConfig(t, "ignore:\n  - check: health/title\n    pages: [/shop/*]\n    until: \"2020-01-01\"\n")
	_, c = compareJSON(t, common...)
	if len(changedPages(c)) != 1 || !strings.Contains(strings.Join(c.Warnings, "\n"), "expired on 2020-01-01") {
		t.Errorf("expired rule: changes %+v warnings %q", c.Changes, c.Warnings)
	}
}

// A page that fails once and then works is UNSTABLE after the confirm
// pass, so --fail-on broke doesn't fail; --confirm=off reports it BROKE.
// One that fails on the first confirm visit too gets a second.
func TestConfirmPass(t *testing.T) {
	needBrowser(t)
	t.Chdir(t.TempDir())
	var fails atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/flaky/":
		default:
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/flaky/" && fails.Add(-1) >= 0 {
			http.Error(w, "busy", http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, `<!doctype html><title>Brew</title><link rel="icon" href="data:,"><a href="/flaky/">Flaky</a><p>Coffee.</p>`)
	}))
	defer srv.Close()
	common := []string{"--no-download", "--viewport", "desktop", "--modules", "health", "--rate", "50", "--plain"}
	if code, out, errOut := runCLI(t, append([]string{"snapshot", srv.URL, "--name", "good"}, common...)...); code != exitOK {
		t.Fatalf("snapshot: exit %d\n%s\n%s", code, out, errOut)
	}
	common = append(common, "--fail-on", "broke")
	fails.Store(1)
	code, out, errOut := runCLI(t, append([]string{"compare"}, common...)...)
	if code != exitOK || !strings.Contains(out, "Confirming: visiting 2 pages again") || !strings.Contains(out, "UNSTABLE") {
		t.Errorf("with confirm: exit %d\n%s\n%s", code, out, errOut)
	}
	// Failing on the first visit again too, it gets one more: a server
	// that fails one load in five isn't broken.
	fails.Store(2)
	code, out, errOut = runCLI(t, append([]string{"compare"}, common...)...)
	if code != exitOK || !strings.Contains(out, "Confirming: visiting 1 page once more") || !strings.Contains(out, "UNSTABLE") {
		t.Errorf("failing twice: exit %d\n%s\n%s", code, out, errOut)
	}
	// A page that fails every time is BROKE after both visits.
	fails.Store(100)
	code, out, errOut = runCLI(t, append([]string{"compare"}, common...)...)
	if code != exitFindings || !strings.Contains(out, "once more") || !strings.Contains(out, "[BROKE]") {
		t.Errorf("failing every time: exit %d\n%s\n%s", code, out, errOut)
	}
	fails.Store(1)
	code, out, _ = runCLI(t, append([]string{"compare", "--confirm=off"}, common...)...)
	if code != exitFindings || !strings.Contains(out, "[BROKE]") || strings.Contains(out, "Confirming") {
		t.Errorf("--confirm=off: exit %d\n%s", code, out)
	}
}

// A form that loses its label is a NEW a11y issue the confirm visit sees
// again, so --fail-on new fails; one that lost it for a single load is
// UNSTABLE. The second visit runs axe too; its issue has to match.
func TestConfirmA11y(t *testing.T) {
	needBrowser(t)
	t.Chdir(t.TempDir())
	var unlabeled atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		label := `<label for="email">Email</label>`
		// Only the browser's loads count: it asks for HTML, the
		// reachability check before a run doesn't.
		if strings.Contains(r.Header.Get("Accept"), "text/html") && unlabeled.Add(-1) >= 0 {
			label = ""
		}
		fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta name="viewport" content="width=device-width"><title>Brew</title>
<link rel="icon" href="data:,"></head><body><main><h1>Brew</h1><form>%s<input id="email" name="email"></form></main></body></html>`, label)
	}))
	defer srv.Close()
	common := []string{"--no-download", "--viewport", "desktop", "--modules", "health,a11y", "--rate", "50", "--plain"}
	if code, out, errOut := runCLI(t, append([]string{"snapshot", srv.URL, "--name", "good"}, common...)...); code != exitOK {
		t.Fatalf("snapshot: exit %d\n%s\n%s", code, out, errOut)
	}
	common = append(common, "--fail-on", "new")
	unlabeled.Store(100)
	code, out, errOut := runCLI(t, append([]string{"compare"}, common...)...)
	if code != exitFindings || !strings.Contains(out, "Confirming: visiting 1 page again") ||
		!strings.Contains(out, "[NEW] Form elements must have labels") || strings.Contains(out, "UNSTABLE") {
		t.Errorf("label removed: exit %d\n%s\n%s", code, out, errOut)
	}
	unlabeled.Store(1)
	code, out, errOut = runCLI(t, append([]string{"compare"}, common...)...)
	if code != exitOK || !strings.Contains(out, "[UNSTABLE] Form elements must have labels") {
		t.Errorf("label gone for one load: exit %d\n%s\n%s", code, out, errOut)
	}
}

// A snapshot stopped halfway keeps the pages it finished; --resume
// carries on without visiting them again.
func TestSnapshotResume(t *testing.T) {
	if runtime.GOOS == "windows" {
		// os.Process.Signal can't send os.Interrupt on Windows, and the
		// test stops the snapshot that way. TestResume* cover the
		// resume itself everywhere.
		t.Skip("Windows can't send os.Interrupt to a child process")
	}
	needBrowser(t)
	var slow atomic.Bool
	slow.Store(true)
	hits := map[string]int{}
	var mu sync.Mutex
	hit := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only the browser's visits count: the run's own check of the
		// address (resolve) isn't a page visit.
		if r.Header.Get("Sec-Fetch-Mode") == "navigate" {
			mu.Lock()
			hits[r.URL.Path]++
			mu.Unlock()
		}
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<!doctype html><title>Home</title><link rel="icon" href="data:,"><a href="/slow/">Slow</a>`)
		case "/slow/":
			if slow.Load() {
				select {
				case hit <- struct{}{}:
				default:
				}
				select {
				case <-r.Context().Done():
				case <-time.After(time.Minute):
				}
				return
			}
			fmt.Fprint(w, `<!doctype html><title>Slow</title><link rel="icon" href="data:,"><p>Done.</p>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	args := []string{"snapshot", srv.URL, "--name", "big", "--plain", "--no-download", "--viewport", "desktop",
		"--modules", "health", "--rate", "50", "--tabs", "1", "--timeout", "50s"}
	cmd, stdout, stderr := cliCommand(t, dir, args...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-hit:
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("/slow/ was never asked for\n%s\n%s", stdout, stderr)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != exitCanceled || !strings.Contains(stderr.String(), "--name big --resume") {
		t.Fatalf("exit %v\n%s\n%s", err, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, ".soundcheck", ".incoming", "big", "pages.jsonl")); err != nil {
		t.Fatalf("the stopped snapshot wasn't kept: %v", err)
	}
	slow.Store(false)
	mu.Lock()
	before := hits["/"]
	mu.Unlock()
	t.Chdir(dir)
	code, out, errOut := runCLI(t, append(args[:1], append([]string{"--resume"}, args[1:]...)...)...)
	if code != exitOK || !strings.Contains(out, "Resume: keeping 1 page the stopped snapshot finished") {
		t.Fatalf("resume: exit %d\n%s\n%s", code, out, errOut)
	}
	mu.Lock()
	again := hits["/"] - before
	mu.Unlock()
	if again != 0 {
		t.Errorf("/ was visited %d more times", again)
	}
	data, err := os.ReadFile(filepath.Join(".soundcheck", "snapshots", "big", "pages.jsonl"))
	if err != nil || !strings.Contains(string(data), `"/slow/"`) || !strings.Contains(string(data), `"path":"/"`) {
		t.Errorf("snapshot pages: %v\n%s", err, data)
	}
}

// --follow-host checks the site a cross-site redirect leads to; without
// it the run stops and says why.
func TestFollowHost(t *testing.T) {
	needBrowser(t)
	t.Chdir(t.TempDir())
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `<!doctype html><title>New home</title><link rel="icon" href="data:,"><p>Moved here.</p>`)
	}))
	defer other.Close()
	moved := httptest.NewServer(http.RedirectHandler(other.URL+"/", http.StatusMovedPermanently))
	defer moved.Close()
	common := []string{"--no-download", "--viewport", "desktop", "--modules", "health", "--rate", "50", "--plain"}
	code, _, errOut := runCLI(t, append([]string{"run", moved.URL}, common...)...)
	if code != exitError || !strings.Contains(errOut, "which is another site") {
		t.Errorf("without --follow-host: exit %d %q", code, errOut)
	}
	code, out, errOut := runCLI(t, append([]string{"run", moved.URL, "--follow-host"}, common...)...)
	if code != exitOK || !strings.Contains(out+errOut, strings.TrimPrefix(other.URL, "http://")) {
		t.Errorf("--follow-host: exit %d\n%s\n%s", code, out, errOut)
	}
}
