// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wpupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
)

// TestMain makes the test binary stand in for WP-CLI ("fake-wp ...")
// and for Soundcheck ("snapshot ...", "compare ..."). Both share a
// state file named by $WPUPDATE_FAKE: the installed plugins, which
// updates break the site and every call made.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "fake-wp":
			os.Exit(fakeWP(os.Args[2:]))
		case "snapshot", "compare":
			os.Exit(fakeSoundcheck(os.Args[1:]))
		}
	}
	os.Exit(m.Run())
}

type fakePlugin struct {
	Name, Installed, Old, New string
}

type fakeState struct {
	Plugins  []fakePlugin
	Breaks   []string // plugins whose new version breaks the site
	Stuck    []string // plugins whose install --force succeeds but changes nothing
	Quiet    []string // plugins in Breaks whose break names no file (a page that's gone)
	Flaky    []string // plugins whose new version breaks the site in the first compare only
	WP       [][]string
	Compares int
	Snapshot []string // the snapshot's argv
}

func loadState() (*fakeState, func()) {
	path := os.Getenv("WPUPDATE_FAKE")
	var st fakeState
	data, _ := os.ReadFile(path)
	_ = json.Unmarshal(data, &st)
	return &st, func() {
		b, _ := json.Marshal(st)
		_ = os.WriteFile(path, b, 0o600)
	}
}

func (s *fakeState) plugin(name string) *fakePlugin {
	for i := range s.Plugins {
		if s.Plugins[i].Name == name {
			return &s.Plugins[i]
		}
	}
	return nil
}

func fakeWP(args []string) int {
	st, save := loadState()
	defer save()
	st.WP = append(st.WP, args)
	switch {
	case len(args) >= 2 && args[0] == "plugin" && args[1] == "list":
		rows := []map[string]string{}
		for _, p := range st.Plugins {
			if p.Installed != p.New {
				rows = append(rows, map[string]string{"name": p.Name, "version": p.Installed, "update_version": p.New})
			}
		}
		b, _ := json.Marshal(rows)
		fmt.Print("PHP Notice: something noisy\n" + string(b))
	case len(args) >= 3 && args[0] == "plugin" && args[1] == "update":
		for _, s := range args[2:] {
			p := st.plugin(s)
			if p == nil {
				fmt.Fprintf(os.Stderr, "Warning: something\nError: The '%s' plugin could not be found.\n", s)
				return 1
			}
			p.Installed = p.New
		}
	case len(args) == 5 && args[0] == "plugin" && args[1] == "install" && args[4] == "--force":
		p := st.plugin(args[2])
		v, ok := strings.CutPrefix(args[3], "--version=")
		if p == nil || !ok {
			return 1
		}
		if !slices.Contains(st.Stuck, p.Name) {
			p.Installed = v
		}
	default:
		fmt.Fprintln(os.Stderr, "Error: unknown command")
		return 1
	}
	return 0
}

func fakeSoundcheck(args []string) int {
	st, save := loadState()
	defer save()
	if args[0] == "snapshot" {
		st.Snapshot = args
		fmt.Print(`{"schema":"` + model.Schema + `","pages":[]}`)
		return 0
	}
	st.Compares++
	c := model.Comparison{Schema: model.Schema, Changes: []model.Change{}}
	for _, f := range st.Flaky {
		if p := st.plugin(f); p != nil && p.Installed == p.New && st.Compares == 1 {
			c.Changes = append(c.Changes, model.Change{Class: model.Broke, Check: "health/php-error", Title: "PHP Warning: Undefined index",
				Source: "plugins/" + f + "/flaky.php:7", Pages: []string{"/"}})
		}
	}
	for _, b := range st.Breaks {
		if p := st.plugin(b); p != nil && p.Installed == p.New && slices.Contains(st.Quiet, b) {
			c.Changes = append(c.Changes, model.Change{Class: model.Broke, Check: "health/status", Title: "Page not found (404)", Pages: []string{"/shop/"}})
			continue
		}
		if p := st.plugin(b); p != nil && p.Installed == p.New {
			c.Changes = append(c.Changes,
				model.Change{Class: model.Changed, Check: "visual/page", Title: "Looks different", Pages: []string{"/"}},
				model.Change{Class: model.Broke, Check: "health/php-error", Title: "PHP Fatal error: Uncaught TypeError",
					Source: "plugins/" + b + "/includes/submit.php:412", Pages: []string{"/contact/"}})
		}
	}
	b, _ := json.Marshal(c)
	_, _ = os.Stdout.Write(b)
	return 1 // a fail-on threshold from the config is still a result
}

// setup writes a state with n plugins p01..pNN at 1.0 with 1.1 to update
// to, where the plugins in breaks break the site at 1.1.
func setup(t *testing.T, n int, breaks ...string) (Options, string) {
	t.Helper()
	dir := t.TempDir()
	st := fakeState{Breaks: breaks}
	for i := 1; i <= n; i++ {
		name := fmt.Sprintf("p%02d", i)
		st.Plugins = append(st.Plugins, fakePlugin{Name: name, Installed: "1.0", Old: "1.0", New: "1.1"})
	}
	path := filepath.Join(dir, "state.json")
	b, _ := json.Marshal(st)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WPUPDATE_FAKE", path)
	// Under -race every child would wait a second before exiting.
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	return Options{Target: "https://staging.shop.test", WP: []string{os.Args[0], "fake-wp"}, Bin: os.Args[0], Dir: dir}, path
}

func readState(t *testing.T, path string) fakeState {
	t.Helper()
	var st fakeState
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func installed(st fakeState) map[string]string {
	m := map[string]string{}
	for _, p := range st.Plugins {
		m[p.Name] = p.Installed
	}
	return m
}

func byStatus(l *Log, status string) []string {
	var out []string
	for _, p := range l.Plugins {
		if p.Status == status {
			out = append(out, p.Slug)
		}
	}
	return out
}

func text(t *testing.T, l *Log) string {
	t.Helper()
	var b bytes.Buffer
	if err := l.Text(&b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestOneByOneRollsBackTheCulprit(t *testing.T) {
	o, path := setup(t, 10, "p07")
	o.Rollback = true
	var out bytes.Buffer
	o.Out = &out
	l, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if got := byStatus(l, RolledBack); !slices.Equal(got, []string{"p07"}) {
		t.Fatalf("rolled back %v\n%s", got, out.String())
	}
	if got := byStatus(l, Updated); len(got) != 9 {
		t.Errorf("updated %v", got)
	}
	p := l.Plugins[slices.IndexFunc(l.Plugins, func(p Plugin) bool { return p.Slug == "p07" })]
	want := "PHP Fatal error: Uncaught TypeError on /contact/, plugins/p07/includes/submit.php:412"
	if p.Verdict != model.Broke || p.Evidence != want {
		t.Errorf("p07: %+v", p)
	}
	st := readState(t, path)
	if !slices.ContainsFunc(st.WP, func(a []string) bool {
		return slices.Equal(a, []string{"plugin", "install", "p07", "--version=1.0", "--force"})
	}) {
		t.Errorf("no rollback command in %v", st.WP)
	}
	inst := installed(st)
	if inst["p07"] != "1.0" || inst["p06"] != "1.1" || inst["p10"] != "1.1" {
		t.Errorf("installed %v", inst)
	}
	// One compare per plugin and one to check the rollback.
	if st.Compares != 11 || l.Compares != 11 || !l.Works {
		t.Errorf("compares %d/%d works %v", st.Compares, l.Compares, l.Works)
	}
	// The snapshot and compares run fast, against the snapshot taken.
	snap := strings.Join(st.Snapshot, " ")
	if !strings.Contains(snap, "--name "+l.Snapshot) || !strings.Contains(snap, "--viewport desktop --modules health,interact") ||
		!strings.HasSuffix(snap, o.Target) {
		t.Errorf("snapshot argv %q", snap)
	}
	txt := text(t, l)
	for _, w := range []string{
		"WordPress updates for https://staging.shop.test",
		"Updated 9 plugins, 1 rolled back: p07 1.0 → 1.1 broke the site (" + want + ").",
		"The site still works.",
		"rolled back: broke the site, " + want,
		"11 compares in",
	} {
		if !strings.Contains(txt, w) {
			t.Errorf("log lacks %q:\n%s", w, txt)
		}
	}
	for _, f := range l.Files {
		if _, err := os.Stat(f); err != nil {
			t.Error(err)
		}
	}
	if len(l.Files) != 2 || !strings.HasSuffix(l.Files[1], ".md") || !strings.Contains(l.Files[0], filepath.Join(config.Dir, LogDir)) {
		t.Errorf("files %v", l.Files)
	}
}

// A rollback that leaves the site broken (a partial reinstall) doesn't
// pass as a site that works: the loop stops there and says so.
func TestOneByOneChecksTheRollback(t *testing.T) {
	o, path := setup(t, 10, "p03")
	st := readState(t, path)
	st.Stuck = []string{"p03"}
	b, _ := json.Marshal(st)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	o.Rollback = true
	l, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if l.Works || l.Compares != 4 {
		t.Errorf("works %v after %d compares", l.Works, l.Compares)
	}
	p := l.Plugins[slices.IndexFunc(l.Plugins, func(p Plugin) bool { return p.Slug == "p03" })]
	if p.Status != RolledBack || p.Error != "the site still breaks after the rollback" {
		t.Errorf("p03: %+v", p)
	}
	if got := byStatus(l, NotUpdated); len(got) != 7 {
		t.Errorf("not updated %v", got)
	}
	if want := "the site still breaks after rolling p03 back to 1.0; check it by hand"; l.Note != want {
		t.Errorf("note %q", l.Note)
	}
	if txt := text(t, l); strings.Contains(txt, "still works") {
		t.Errorf("a broken site still works:\n%s", txt)
	}
}

// The roll-back hint goes through the --wp command the loop ran.
func TestRollbackCommandUsesTheWPPrefix(t *testing.T) {
	p := Plugin{Slug: "p04", Old: "1.0"}
	for _, tc := range []struct {
		wp   []string
		want string
	}{
		{nil, "wp plugin install p04 --version=1.0 --force"},
		{[]string{"ssh", "deploy@staging", "--", "wp", "--path=/srv/www"}, "ssh deploy@staging -- wp --path=/srv/www plugin install p04 --version=1.0 --force"},
		{[]string{"/home/me/bin/wp cli.sh"}, "'/home/me/bin/wp cli.sh' plugin install p04 --version=1.0 --force"},
	} {
		if got := (&Log{WP: tc.wp}).rollbackCommand(p); got != tc.want {
			t.Errorf("%q: got %s\nwant %s", tc.wp, got, tc.want)
		}
	}
}

func TestOneByOneStopsWithoutRollback(t *testing.T) {
	o, path := setup(t, 10, "p04")
	l, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if got := byStatus(l, Kept); !slices.Equal(got, []string{"p04"}) {
		t.Fatalf("kept %v", got)
	}
	if got := byStatus(l, NotUpdated); len(got) != 6 {
		t.Errorf("not updated %v", got)
	}
	st := readState(t, path)
	if inst := installed(st); inst["p04"] != "1.1" || inst["p05"] != "1.0" {
		t.Errorf("installed %v", inst)
	}
	if st.Compares != 4 || l.Works {
		t.Errorf("compares %d works %v", st.Compares, l.Works)
	}
	txt := text(t, l)
	for _, w := range []string{
		"Updated 4 plugins.",
		"p04 1.0 → 1.1 broke the site (PHP Fatal error: Uncaught TypeError on /contact/, plugins/p04/includes/submit.php:412) and is still installed. Roll it back with: " +
			shellQuote(os.Args[0]) + " fake-wp plugin install p04 --version=1.0 --force",
		"not updated: stopped at p04",
	} {
		if !strings.Contains(txt, w) {
			t.Errorf("log lacks %q:\n%s", w, txt)
		}
	}
	if strings.Contains(txt, "still works") {
		t.Errorf("a broken site still works:\n%s", txt)
	}
}

func TestBisectFindsEveryCulpritInFourCompares(t *testing.T) {
	for i := 1; i <= 10; i++ {
		culprit := fmt.Sprintf("p%02d", i)
		t.Run(culprit, func(t *testing.T) {
			o, path := setup(t, 10, culprit)
			o.Bisect, o.Rollback = true, true
			l, err := Run(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			got := byStatus(l, RolledBack)
			if !slices.Equal(got, []string{culprit}) {
				t.Fatalf("rolled back %v", got)
			}
			p := l.Plugins[i-1]
			if p.Compares > 4 || p.Compares < 1 || !strings.Contains(p.Evidence, "plugins/"+culprit+"/") {
				t.Errorf("culprit %+v", p)
			}
			st := readState(t, path)
			inst := installed(st)
			for name, v := range inst {
				if want := map[bool]string{true: "1.0", false: "1.1"}[name == culprit]; v != want {
					t.Errorf("%s at %s", name, v)
				}
			}
			// One compare with everything, the search, one without the culprit.
			if !l.Works || l.Compares != p.Compares+2 || st.Compares != l.Compares {
				t.Errorf("works %v compares %d (search %d, fake %d)", l.Works, l.Compares, p.Compares, st.Compares)
			}
		})
	}
}

// A culprit found by elimination is named from a compare that had more
// updates than its own: its evidence leaves out what another plugin
// there broke (here a flaky warning in p03's files), even when its own
// change names no file.
func TestBisectEvidenceIsTheCulprits(t *testing.T) {
	o, path := setup(t, 10, "p10")
	st := readState(t, path)
	st.Quiet, st.Flaky = []string{"p10"}, []string{"p03"}
	b, _ := json.Marshal(st)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	o.Bisect, o.Rollback = true, true
	l, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if got := byStatus(l, RolledBack); !slices.Equal(got, []string{"p10"}) {
		t.Fatalf("rolled back %v", got)
	}
	if p := l.Plugins[9]; p.Evidence != "Page not found (404) on /shop/" {
		t.Errorf("evidence %q", p.Evidence)
	}
}

func TestBisectClean(t *testing.T) {
	o, path := setup(t, 10)
	o.Bisect = true
	l, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	st := readState(t, path)
	if st.Compares != 1 || len(byStatus(l, Updated)) != 10 || !l.Works {
		t.Errorf("compares %d, %+v", st.Compares, l.Plugins)
	}
	// One WP-CLI call updates them all.
	if n := len(st.WP); n != 2 || len(st.WP[1]) != 12 {
		t.Errorf("wp calls %v", st.WP)
	}
	if txt := text(t, l); !strings.Contains(txt, "Updated 10 plugins.\nThe site still works.") {
		t.Errorf("log:\n%s", txt)
	}
}

func TestBisectTwoCulprits(t *testing.T) {
	o, path := setup(t, 10, "p02", "p09")
	o.Bisect, o.Rollback = true, true
	l, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if got := byStatus(l, RolledBack); !slices.Equal(got, []string{"p02", "p09"}) {
		t.Fatalf("rolled back %v", got)
	}
	inst := installed(readState(t, path))
	if inst["p02"] != "1.0" || inst["p09"] != "1.0" || inst["p05"] != "1.1" || !l.Works {
		t.Errorf("installed %v works %v", inst, l.Works)
	}
}

func TestBisectWithoutRollbackKeepsEveryUpdate(t *testing.T) {
	o, path := setup(t, 6, "p05")
	o.Bisect = true
	l, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if got := byStatus(l, Kept); !slices.Equal(got, []string{"p05"}) {
		t.Fatalf("kept %v", got)
	}
	for name, v := range installed(readState(t, path)) {
		if v != "1.1" {
			t.Errorf("%s at %s", name, v)
		}
	}
	if l.Works {
		t.Error("works")
	}
}

func TestFromTunerGoesFirst(t *testing.T) {
	o, _ := setup(t, 5)
	report := filepath.Join(t.TempDir(), "tuner.json")
	_ = os.WriteFile(report, []byte(`{"schema":"rhythm-tuner/v0","findings":[
		{"id":"vuln-plugin-p04","severity":"medium"},
		{"id":"vuln-plugin-p02","severity":"critical"},
		{"id":"vuln-plugin-gone","severity":"high"},
		{"id":"wp-core-outdated","severity":"low"}]}`), 0o600)
	o.FromTuner = report
	l, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, p := range l.Plugins {
		order = append(order, p.Slug)
	}
	if !slices.Equal(order, []string{"p02", "p04", "p01", "p03", "p05"}) {
		t.Errorf("order %v", order)
	}
	txt := text(t, l)
	if !strings.Contains(txt, "2 vulnerable plugins fixed, site still works.") || !strings.Contains(txt, "updated (security fix)") {
		t.Errorf("log:\n%s", txt)
	}
}

func TestFromTunerVulnerableRolledBack(t *testing.T) {
	o, _ := setup(t, 3, "p03")
	o.Rollback = true
	report := filepath.Join(t.TempDir(), "tuner.json")
	_ = os.WriteFile(report, []byte(`[{"schema":"rhythm-tuner/v0","findings":[{"id":"vuln-plugin-p03","severity":"high"}]}]`), 0o600)
	o.FromTuner = report
	l, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if txt := text(t, l); !strings.Contains(txt, "Still vulnerable: p03.") || strings.Contains(txt, "vulnerable plugin fixed") {
		t.Errorf("log:\n%s", txt)
	}
}

func TestPluginsFilter(t *testing.T) {
	o, path := setup(t, 4)
	o.Plugins = []string{"p03", "nope"}
	l, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Plugins) != 1 || l.Plugins[0].Slug != "p03" || !slices.Equal(l.Missing, []string{"nope"}) {
		t.Errorf("plugins %+v missing %v", l.Plugins, l.Missing)
	}
	if inst := installed(readState(t, path)); inst["p03"] != "1.1" || inst["p01"] != "1.0" {
		t.Errorf("installed %v", inst)
	}
	if txt := text(t, l); !strings.Contains(txt, "No update available for nope.") {
		t.Errorf("log:\n%s", txt)
	}
}

func TestNoUpdates(t *testing.T) {
	o, path := setup(t, 0)
	l, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if st := readState(t, path); st.Snapshot != nil || st.Compares != 0 {
		t.Errorf("ran soundcheck with nothing to update: %+v", st)
	}
	if txt := text(t, l); !strings.Contains(txt, "No plugin updates available.") {
		t.Errorf("log:\n%s", txt)
	}
}

func TestSlugWithShellCharactersIsOneArgument(t *testing.T) {
	o, path := setup(t, 1)
	evil := "evil;touch pwned $(touch pwned2) `id`"
	st := readState(t, path)
	st.Plugins = append(st.Plugins, fakePlugin{Name: evil, Installed: "1.0", Old: "1.0", New: "2.0"},
		fakePlugin{Name: "--path=/elsewhere", Installed: "1.0", Old: "1.0", New: "2.0"})
	b, _ := json.Marshal(st)
	_ = os.WriteFile(path, b, 0o600)
	t.Chdir(o.Dir)
	l, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	st = readState(t, path)
	if !slices.ContainsFunc(st.WP, func(a []string) bool { return slices.Equal(a, []string{"plugin", "update", evil}) }) {
		t.Errorf("wp calls %q", st.WP)
	}
	for _, f := range []string{"pwned", "pwned2"} {
		if _, err := os.Stat(filepath.Join(o.Dir, f)); err == nil {
			t.Errorf("a shell ran %s", f)
		}
	}
	// A name WP-CLI would read as an option is never passed.
	if got := byStatus(l, Skipped); !slices.Equal(got, []string{"--path=/elsewhere"}) {
		t.Errorf("skipped %v", got)
	}
	if inst := installed(st); inst["--path=/elsewhere"] != "1.0" || inst[evil] != "2.0" {
		t.Errorf("installed %v", inst)
	}
}

func TestProductionRefused(t *testing.T) {
	o, path := setup(t, 3)
	for _, target := range []string{"https://shop.test/", "shop.test", "http://shop.test/blog/"} {
		o.Target = target
		o.Config = config.Config{Production: []string{"https://shop.test", "http://shop.test"}}
		_, err := Run(context.Background(), o)
		if err == nil || !strings.Contains(err.Error(), "is a production site") {
			t.Errorf("%s: %v", target, err)
		}
	}
	if st := readState(t, path); len(st.WP) != 0 || st.Snapshot != nil {
		t.Errorf("touched a production site: %+v", st)
	}
}

func TestOptionErrors(t *testing.T) {
	o, _ := setup(t, 1)
	for _, c := range []struct {
		edit func(*Options)
		want string
	}{
		{func(o *Options) { o.Target = "" }, "the site is missing"},
		{func(o *Options) { o.WP = nil }, "--wp is missing"},
		{func(o *Options) { o.Bin = "" }, "binary is missing"},
		{func(o *Options) { o.OneByOne, o.Bisect = true, true }, "not both"},
		{func(o *Options) { o.StopOn = "slower" }, `--stop-on "slower": want broke, new or changed`},
		{func(o *Options) { o.FromTuner = filepath.Join(o.Dir, "missing.json") }, "--from-tuner"},
		{func(o *Options) { o.WP = []string{filepath.Join(o.Dir, "no-wp")} }, "listing plugin updates"},
	} {
		oo := o
		c.edit(&oo)
		if _, err := Run(context.Background(), oo); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("want %q, got %v", c.want, err)
		}
	}
}

func TestStopOnNew(t *testing.T) {
	o, _ := setup(t, 2, "p02")
	o.StopOn, o.Rollback = model.Changed, true
	l, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	// The visual change is enough with stop-on changed; the evidence
	// still prefers the change in the plugin's own files.
	if got := byStatus(l, RolledBack); !slices.Equal(got, []string{"p02"}) || !strings.Contains(l.Plugins[1].Evidence, "plugins/p02/") {
		t.Errorf("rolled back %v, %+v", got, l.Plugins)
	}
}

func TestMarkdownLog(t *testing.T) {
	l := &Log{Target: "https://staging.shop.test", Mode: ModeOneByOne, StopOn: model.Broke, Compares: 2, Works: true,
		Plugins: []Plugin{
			{Slug: "form|<b>", Old: "1.0", New: "1.1", Status: RolledBack, Verdict: model.Broke, Evidence: "Server error 500 on /contact/"},
			{Slug: "seo", Old: "2.0", New: "2.1", Status: Updated},
		}}
	var b bytes.Buffer
	if err := l.Markdown(&b); err != nil {
		t.Fatal(err)
	}
	md := b.String()
	for _, w := range []string{
		"# WordPress updates for https://staging.shop.test",
		"| Plugin | Version | Result |",
		`| form\|&lt;b&gt; | 1.0 → 1.1 | rolled back: broke the site, Server error 500 on /contact/ |`,
		"| seo | 2.0 → 2.1 | updated |",
	} {
		if !strings.Contains(md, w) {
			t.Errorf("markdown lacks %q:\n%s", w, md)
		}
	}
}

func TestSplitCommand(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"wp", []string{"wp"}},
		{"  docker compose exec -T wp wp ", []string{"docker", "compose", "exec", "-T", "wp", "wp"}},
		{"ssh deploy@staging -- wp --path=/srv/www", []string{"ssh", "deploy@staging", "--", "wp", "--path=/srv/www"}},
		{`wp --path='/srv/my site'`, []string{"wp", "--path=/srv/my site"}},
		{`wp --path="/srv/my \"site\" \x"`, []string{"wp", `--path=/srv/my "site" \x`}},
		{`wp --path=/srv/my\ site`, []string{"wp", "--path=/srv/my site"}},
		{`wp '' x`, []string{"wp", "", "x"}},
		{`wp "a;b" 'c|d'`, []string{"wp", "a;b", "c|d"}},
		{`wp $HOME ~ *`, []string{"wp", "$HOME", "~", "*"}},
		{"wp \\\n--allow-root", []string{"wp", "--allow-root"}},
	} {
		got, err := SplitCommand(c.in)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("SplitCommand(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	for _, c := range []struct{ in, want string }{
		{"", "empty"},
		{"   ", "empty"},
		{`wp 'x`, "unterminated ' quote"},
		{`wp "x`, `unterminated " quote`},
		{`wp x\`, "ends with a backslash"},
		{"cd /srv && wp", `"&" needs a shell`},
		{"wp; rm -rf /", `";" needs a shell`},
		{"wp | tee", `"|" needs a shell`},
		{"wp $(id)", `"$(" needs a shell`},
		{"wp `id`", "\"`\" needs a shell"},
		{"wp > log", `">" needs a shell`},
	} {
		if _, err := SplitCommand(c.in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("SplitCommand(%q): want %q, got %v", c.in, c.want, err)
		}
	}
}

func TestWPArgvQuotesForSSH(t *testing.T) {
	evil := "a b;c'd"
	local := wpArgv([]string{"docker", "compose", "exec", "-T", "wp", "wp"}, "plugin", "update", evil)
	if local[len(local)-1] != evil {
		t.Errorf("local argv %q", local)
	}
	remote := wpArgv([]string{"/usr/bin/ssh", "deploy@staging", "--", "wp", "--path=/srv/www"}, "plugin", "update", evil, "--version=1.0")
	want := []string{"/usr/bin/ssh", "deploy@staging", "--", "wp", "--path=/srv/www", "plugin", "update", `'a b;c'\''d'`, "--version=1.0"}
	if !slices.Equal(remote, want) {
		t.Errorf("ssh argv %q, want %q", remote, want)
	}
}

func TestReadTunerRejectsOtherJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.json")
	_ = os.WriteFile(p, []byte(`{"schema":"rhythm-soundcheck/v1"}`), 0o600)
	if _, err := readTuner(p); err == nil || !strings.Contains(err.Error(), "isn't a rhythm-tuner JSON report") {
		t.Errorf("got %v", err)
	}
	_ = os.WriteFile(p, []byte(`nope`), 0o600)
	if _, err := readTuner(p); err == nil {
		t.Error("read garbage")
	}
}
