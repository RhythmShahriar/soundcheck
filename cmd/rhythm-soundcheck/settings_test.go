// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

func TestInherit(t *testing.T) {
	snap := model.Options{Modules: []string{"health", "interact"}, Levels: []string{"inspect", "click", "submit"},
		Viewports: []string{"desktop"}}
	echoOn := func(c *config.Config) { c.Modules = append(c.Modules, "echo") }
	for _, tc := range []struct {
		name    string
		snap    model.Options
		args    []string // flags given to compare
		file    string   // config file
		adjust  func(*config.Config)
		skip    []string
		modules string // effective
		levels  string
		views   string
		changes string
	}{
		{name: "nothing set takes the snapshot's", snap: snap,
			modules: "health,interact", levels: "inspect,click,submit", views: "desktop"},
		{name: "a flag wins", snap: snap, args: []string{"--level", "inspect,click"},
			modules: "health,interact", levels: "inspect,click", views: "desktop",
			changes: "levels inspect,click,submit -> inspect,click (--level)"},
		{name: "the config file wins", snap: snap, file: "viewports: [desktop, mobile]\n",
			modules: "health,interact", levels: "inspect,click,submit", views: "desktop,mobile",
			changes: "viewports desktop -> desktop,mobile (CONFIG)"},
		{name: "the same set in another order isn't a change", snap: snap, args: []string{"--modules", "interact,health"},
			modules: "interact,health", levels: "inspect,click,submit", views: "desktop"},
		{name: "a flag and the file", snap: snap, args: []string{"--modules", "health"}, file: "interactions:\n  levels: [inspect]\n",
			modules: "health", levels: "inspect", views: "desktop",
			changes: "modules health,interact -> health (--modules); levels inspect,click,submit -> inspect (CONFIG)"},
		{name: "--skip applies to inherited modules", snap: snap, skip: []string{"interact"},
			modules: "health", levels: "inspect,click,submit", views: "desktop",
			changes: "modules health,interact -> health"},
		{name: "echo follows today's settings: on", snap: snap, adjust: echoOn,
			modules: "health,interact,echo", levels: "inspect,click,submit", views: "desktop",
			changes: "modules health,interact -> health,interact,echo"},
		{name: "echo follows today's settings: off",
			snap:    model.Options{Modules: []string{"health", "echo"}, Levels: snap.Levels, Viewports: snap.Viewports},
			modules: "health", levels: "inspect,click,submit", views: "desktop",
			changes: "modules health,echo -> health"},
		{name: "values this version doesn't know are dropped",
			snap:    model.Options{Modules: []string{"health", "telepathy"}, Levels: []string{"inspect", "dance"}, Viewports: []string{"watch", "mobile"}},
			modules: "health", levels: "inspect", views: "mobile",
			changes: "modules health,telepathy -> health; levels inspect,dance -> inspect; viewports watch,mobile -> mobile"},
		{name: "an old snapshot without options changes nothing",
			modules: "health,visual,interact,cache,speed", levels: "inspect,click", views: "desktop,mobile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			if tc.file != "" {
				writeConfig(t, tc.file)
			}
			p, err := parse(append([]string{"compare"}, tc.args...))
			if err != nil {
				t.Fatal(err)
			}
			if tc.adjust != nil {
				tc.adjust(&p.cfg)
			}
			changes := inherit(&p.cfg, tc.snap, p.chosen, tc.skip)
			got := strings.Join(p.cfg.Modules, ",") + " | " + strings.Join(p.cfg.Interactions.Levels, ",") + " | " + strings.Join(p.cfg.Viewports, ",")
			if want := tc.modules + " | " + tc.levels + " | " + tc.views; got != want {
				t.Errorf("settings\n got %s\nwant %s", got, want)
			}
			wantChanges := strings.ReplaceAll(tc.changes, "CONFIG", filepath.Join(config.Dir, "config.yaml"))
			if got := joinChanges(changes); got != wantChanges {
				t.Errorf("changes\n got %q\nwant %q", got, wantChanges)
			}
		})
	}
}

func TestSnapshotSettings(t *testing.T) {
	got := snapshotSettings("forms", model.Options{Modules: []string{"health", "interact"}, Levels: []string{"inspect", "submit"}, Viewports: []string{"desktop"}})
	if want := `from snapshot "forms" (modules health,interact; levels inspect,submit; viewports desktop)`; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// formSite serves a contact form and counts what it receives.
func formSite(posts *atomic.Int32) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPost {
			posts.Add(1)
			fmt.Fprint(w, `<!doctype html><html><head><title>Thanks</title><link rel="icon" href="data:,"></head><body><p>Thanks, we got your message.</p></body></html>`)
			return
		}
		fmt.Fprint(w, `<!doctype html><html><head><title>Contact</title><link rel="icon" href="data:,"></head><body><h1>Contact</h1>
<form method="post" action="/"><label>Name <input name="name" required></label><label>Email <input type="email" name="email" required></label>
<label>Message <textarea name="message" required></textarea></label><button type="submit">Send</button></form></body></html>`)
	})
	return httptest.NewServer(mux)
}

func TestCompareInheritsSnapshotSettings(t *testing.T) {
	needBrowser(t)
	t.Chdir(t.TempDir())
	var posts atomic.Int32
	srv := formSite(&posts)
	defer srv.Close()
	common := []string{"--no-download", "--rate", "50", "--plain", "-v"}
	code, out, errOut := runCLI(t, append([]string{"snapshot", srv.URL, "--name", "forms", "--modules", "health,interact",
		"--level", "inspect,click,submit", "--viewport", "desktop"}, common...)...)
	if code != exitOK {
		t.Fatalf("snapshot: exit %d\n%s\n%s", code, out, errOut)
	}
	if posts.Load() == 0 {
		t.Fatalf("the snapshot submitted no form\n%s", out)
	}

	// A plain compare submits the form again, as the snapshot did.
	posts.Store(0)
	code, out, errOut = runCLI(t, append([]string{"compare"}, common...)...)
	if code != exitOK {
		t.Fatalf("compare: exit %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, `Settings: from snapshot "forms" (modules health,interact; levels inspect,click,submit; viewports desktop)`) {
		t.Errorf("no Settings line:\n%s", out)
	}
	if strings.Contains(out, "You changed") {
		t.Errorf("nothing was changed:\n%s", out)
	}
	if posts.Load() == 0 {
		t.Errorf("compare didn't submit the form\n%s", out)
	}
	after := latestRun(t)
	if got := strings.Join(after.Options.Levels, ","); got != "inspect,click,submit" {
		t.Errorf("compare ran levels %s", got)
	}
	if got := strings.Join(after.Options.Modules, ",") + " " + strings.Join(after.Options.Viewports, ","); got != "health,interact desktop" {
		t.Errorf("compare ran %s", got)
	}

	// An explicit flag wins and says so before any page is checked.
	posts.Store(0)
	code, out, errOut = runCLI(t, append([]string{"compare", "--level", "inspect,click"}, common...)...)
	if code != exitOK {
		t.Fatalf("compare --level: exit %d\n%s\n%s", code, out, errOut)
	}
	changed := strings.Index(out, "You changed: levels inspect,click,submit -> inspect,click (--level)")
	page := strings.Index(out, "[1/") // the -v line for the first page
	if changed < 0 || page < changed {
		t.Errorf("want the You changed line before any page line:\n%s", out)
	}
	if posts.Load() != 0 {
		t.Errorf("--level inspect,click submitted a form")
	}

	// --strict refuses the mismatch before checking anything.
	runs, _ := filepath.Glob(".soundcheck/runs/*")
	code, _, errOut = runCLI(t, append([]string{"compare", "--level", "inspect,click", "--strict"}, common...)...)
	if code != exitUsage || !strings.Contains(errOut, `--strict: this comparison wouldn't use snapshot "forms"'s settings: levels inspect,click,submit -> inspect,click (--level)`) {
		t.Errorf("--strict: exit %d\n%s", code, errOut)
	}
	if now, _ := filepath.Glob(".soundcheck/runs/*"); len(now) != len(runs) {
		t.Errorf("--strict left a run folder: %v", now)
	}
}

func TestStrict(t *testing.T) {
	t.Chdir(t.TempDir())
	snap := store.Store{Root: config.Dir}
	dir, err := snap.NewDir("snapshots", "forms")
	if err != nil {
		t.Fatal(err)
	}
	run := &model.Run{Schema: model.Schema, Name: "forms", Target: deadSite(t), Options: model.Options{Modules: []string{"health"},
		Levels: []string{"inspect", "click"}, Viewports: []string{"desktop", "mobile"}}}
	if err := store.SaveRun(dir, run); err != nil {
		t.Fatal(err)
	}
	if err := snap.SetLatest("forms"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"compare", "--strict", "--viewport", "mobile"}, "viewports desktop,mobile -> mobile (--viewport)"},
		{[]string{"compare", "--strict", "--skip", "health", "--modules", "health,visual"}, "modules health -> visual (--modules)"},
		{[]string{"snapshot", "a.test", "--strict"}, "--strict works with compare against a snapshot"},
		{[]string{"run", "a.test", "--strict"}, "--strict works with compare"},
		{[]string{"compare", "b.test", "--against", "a.test", "--strict"}, "--strict works with compare"},
		{[]string{"compare", "--skip", "health"}, "no modules left to run: the snapshot ran health"},
	} {
		code, _, errOut := runCLI(t, tc.args...)
		if code != exitUsage || !strings.Contains(errOut, tc.want) {
			t.Errorf("%v: exit %d, stderr %q; want 2 and %q", tc.args, code, errOut, tc.want)
		}
	}
	if _, err := os.Stat(filepath.Join(config.Dir, "runs")); err == nil {
		t.Error("a refused compare left a run folder")
	}
}

// latestRun loads the newest run.
func latestRun(t *testing.T) *model.Run {
	t.Helper()
	dir, err := store.Store{Root: config.Dir}.LatestRun()
	if err != nil {
		t.Fatal(err)
	}
	r, err := store.LoadRun(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
