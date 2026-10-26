// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/soundcheck/compare"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/evidence"
	"github.com/RhythmShahriar/soundcheck/login"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/report"
	"github.com/RhythmShahriar/soundcheck/statement"
	"github.com/RhythmShahriar/soundcheck/store"
	"github.com/RhythmShahriar/soundcheck/wcag"
)

// The statement and evidence steps of an --eaa run, as variables so the
// tests can stand in for them.
var (
	decodeOwner      = statement.DecodeOwner
	buildStatement   = statement.Build
	statementMD      = statement.Markdown
	writeEvidence    = writePack
	previousEvidence = evidence.Previous
	staleIgnores     = compare.StaleIgnores
)

// evidenceExtra is what an evidence pack records that the run itself
// doesn't: the browser's version and whether TLS was verified (-k). Its
// fields match evidence.Extra, so writePack converts it as it is.
type evidenceExtra struct {
	Chrome   string
	Insecure bool
}

// writePack writes the evidence pack of run with extra, so a pack of a
// run made with -k says so and evidence sign refuses it.
func writePack(dir string, run *model.Run, prev *evidence.Ref, extra evidenceExtra) error {
	return evidence.WriteWith(dir, run, prev, evidence.Extra(extra))
}

// axeVersion is the axe-core release checks/a11y vendors, for the
// header, which is printed before the a11y module records it in the
// run's options. wcag's table is generated from the vendored file and a
// test keeps the two in step, so a bump can't leave the header behind.
const axeVersion = wcag.AxeVersion

// Files an --eaa run writes into its folder.
const (
	eaaDir        = "eaa"
	draftFile     = "statement-draft.md"
	evidenceFile  = "evidence.json"
	progressFile  = "progress.json" // an interrupted run's page count; see interruptedRun
	statementHint = "accessibility-statement.md"
)

// Placeholders for the statement: keys the draft needs. A draft is
// written after every --eaa run; until the owner fills in statement: in
// the config file it says where.
const (
	todoOwner   = "[TO DO: who runs the website; set statement.owner]"
	todoWebsite = "[TO DO: the public address of the website; set statement.website]"
	todoEmail   = "[TO DO: an email address for accessibility feedback; set statement.contact.email]"
)

// eaaSteps are the header lines about the accessibility and keyboard
// tests, when those modules run: what axe and the walk do, the key
// flows and, under --eaa, what the run never does.
func (s *session) eaaSteps() {
	cfg := s.cfg
	if cfg.Has("a11y") {
		s.view.Step("Accessibility", fmt.Sprintf("axe-core %s, WCAG %s A and AA rules, reflow at 320 px", axeVersion, cmpOr(s.wcag, "2.0")))
	}
	if cfg.Has("keyboard") {
		maxTabs := 150
		if s.keyboard != nil {
			maxTabs = s.keyboard.MaxTabs
		}
		s.view.Step("Keyboard", fmt.Sprintf("Tab walk on every page (up to %d stops), menus and dialogs; %s",
			maxTabs, keyFlowsLine(keyFlows(cfg.Flows), cfg.Level("flows"))))
	}
	// Forms are submitted only by interact's submit level, which a user
	// can add to --eaa; then the promise isn't true.
	if cfg.EAA && (!cfg.Has("interact") || !cfg.Level("submit")) {
		s.view.Step("Never", "submits a form, places an order or types into fields outside key flows")
	}
}

// keyFlowsLine describes the key flows for the Keyboard header line:
// "1 key flow (keyboard-checkout.yaml) needs --level flows, not run".
func keyFlowsLine(files []string, flows bool) string {
	if len(files) == 0 {
		return "no key flows"
	}
	n := plural(len(files), "key flow")
	verb := "run"
	if !flows {
		verb = "need --level flows, not run"
	}
	if len(files) == 1 {
		verb = "runs"
		if !flows {
			verb = "needs --level flows, not run"
		}
	}
	return fmt.Sprintf("%s (%s) %s", n, strings.Join(files, ", "), verb)
}

// keyFlows lists the key flows in the flows folder: the flows whose
// keyboard: is true or send. A file that can't be read is left to the
// keyboard module, which reports it.
func keyFlows(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if e.IsDir() || (ext != ".yaml" && ext != ".yml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // a flow file of the project
		if err != nil {
			continue
		}
		var head struct {
			Keyboard yaml.Node `yaml:"keyboard"`
		}
		if yaml.Unmarshal(data, &head) != nil {
			continue
		}
		var on bool
		if v := head.Keyboard.Value; v == "send" || (head.Keyboard.Decode(&on) == nil && on) {
			out = append(out, e.Name())
		}
	}
	slices.Sort(out)
	return out
}

// eaaResult fills in the EAA summary of an --eaa result for every
// output format. After a check (s.checked) it also writes the
// statement draft and the evidence pack into the run's folder and
// returns the hint that points at the draft.
func (s *session) eaaResult(res *report.Result) string {
	pages, sum, ok := eaaSummary(res)
	if !ok {
		return ""
	}
	run := res.Run
	var ignored []model.Change
	if res.Comparison != nil {
		ignored = res.Comparison.Ignored
	}
	if !s.checked {
		return ""
	}
	interrupted := run.Interrupted || s.ctx.Err() != nil || (res.Comparison != nil && res.Comparison.Interrupted)
	if err := eaaFolder(res.Dir); err != nil {
		fmt.Fprintf(s.stderr, "%s %v\n", prefix(), err)
		return ""
	}
	if err := s.writeEvidence(res.Dir, run, interrupted); err != nil {
		fmt.Fprintf(s.stderr, "%s writing %s: %v\n", prefix(), filepath.Join(res.Dir, eaaDir, evidenceFile), err)
	}
	// A draft from part of the site would leave problems out; a rule
	// may match a page the run didn't reach.
	if interrupted {
		s.writeProgress(res.Dir)
		return ""
	}
	s.staleNotes(pages, run.Issues)
	path := filepath.Join(res.Dir, eaaDir, draftFile)
	if err := s.writeDraft(path, pages, run, sum, ignored); err != nil {
		fmt.Fprintf(s.stderr, "%s writing %s: %v\n", prefix(), path, err)
		return ""
	}
	return fmt.Sprintf("statement: %s (a draft for you to finish:\n           %s statement -o %s)", rel(path), cmdName(), statementHint)
}

// eaaSummary fills the accessibility summary of an --eaa run (run.EAA
// and the comparison's EAA) from its stored pages. It isn't kept in
// run.json, so report fills it again before any report is written. ok
// is false for a run made without --eaa.
func eaaSummary(res *report.Result) (pages func(func(*model.Page) bool), sum model.EAASummary, ok bool) {
	run := res.Run
	if run == nil || !run.Options.EAA {
		return nil, sum, false
	}
	var ignored []model.Change
	if res.Comparison != nil {
		ignored = res.Comparison.Ignored
	}
	pages = storedPages(res.Dir, run)
	sum = wcag.Summarize(run, pages, run.Issues, ignored)
	run.EAA = &sum
	if res.Comparison != nil {
		res.Comparison.EAA = &sum
	}
	return pages, sum, true
}

// staleNotes names the ignore rules that matched nothing in the run:
// the problem they hide looks fixed, so the committed baseline should
// lose them.
func (s *session) staleNotes(pages func(func(*model.Page) bool), site []model.Issue) {
	if s.o.quiet {
		return
	}
	for _, i := range staleIgnores(ignoreRules(s.cfg), time.Now(), pages, site) {
		if i < 0 || i >= len(s.cfg.Ignore) {
			continue
		}
		fmt.Fprintf(s.stderr, "note: ignore rule %d (%s) matched nothing; the problem looks fixed: remove it\n", i+1, report.Term(ruleText(s.cfg.Ignore[i])))
	}
}

// ruleText names an ignore rule by what it matches: "a11y/color-contrast
// on /legal/*".
func ruleText(r config.IgnoreRule) string {
	what := r.Check
	if what == "" {
		what = fmt.Sprintf("%q", r.Title)
	}
	if len(r.Pages) == 0 {
		return what + " on every page"
	}
	return what + " on " + strings.Join(r.Pages, ", ")
}

// writeEvidence writes the evidence pack, chained to the last --eaa run
// of the same site. An interrupted run records that it was. A run made
// with -k records that TLS wasn't verified: a signature would otherwise
// vouch for pages that may not have come from the site.
func (s *session) writeEvidence(dir string, run *model.Run, interrupted bool) error {
	prev, err := previousEvidence(s.st.Root, run.Target)
	if err != nil {
		return err
	}
	r := *run
	r.Interrupted = r.Interrupted || interrupted
	return writeEvidence(dir, &r, prev, evidenceExtra{Chrome: s.chrome, Insecure: s.o.insecure})
}

// progress is eaa/progress.json: how many pages an interrupted run set
// out to visit. The run's pages file holds only the ones it reached, so
// the statement command's refusal would otherwise say "17 of 17".
type progress struct {
	Pages int `json:"pages"`
}

// writeProgress records how many pages the interrupted check set out to
// visit, when the runner said.
func (s *session) writeProgress(dir string) {
	n := int(s.discovered.Load())
	if n <= 0 || dir == "" {
		return
	}
	data, err := json.Marshal(progress{Pages: n})
	if err == nil {
		err = writePrivate(filepath.Join(dir, eaaDir, progressFile), func(w io.Writer) error { _, err := w.Write(data); return err })
	}
	if err != nil {
		fmt.Fprintf(s.stderr, "%s writing %s: %v\n", prefix(), filepath.Join(dir, eaaDir, progressFile), err)
	}
}

// writeDraft writes the statement draft to path, with [TO DO]s where
// statement: in the config file lacks what the draft needs.
func (s *session) writeDraft(path string, pages func(func(*model.Page) bool), run *model.Run, sum model.EAASummary, ignored []model.Change) error {
	owner, err := decodeOwner(s.cfg)
	if err != nil {
		return err
	}
	owner.Owner = cmpOr(owner.Owner, todoOwner)
	owner.Website = cmpOr(owner.Website, todoWebsite)
	owner.Contact.Email = cmpOr(owner.Contact.Email, todoEmail)
	d, err := buildStatement(statement.Input{Run: run, Pages: pages, Ignored: ignored, Summary: sum, Owner: owner,
		Version: version, Layout: "model", Now: time.Now(), Insecure: s.o.insecure, Hide: statementHide()})
	if err != nil {
		return err
	}
	return writePrivate(path, func(w io.Writer) error { return statementMD(w, d) })
}

// statementHide is what a draft must never show besides the site's own
// host, which statement.Build hides itself: the test account's user
// name, which a logged-in page's path or a page title can hold.
func statementHide() []string {
	if user := os.Getenv(login.EnvUser); user != "" {
		return []string{user}
	}
	return nil
}

// storedPages yields the run's pages from its folder one at a time, so
// a big site's run needn't be held twice, or the ones in memory when the
// folder can't be read.
func storedPages(dir string, run *model.Run) func(func(*model.Page) bool) {
	return func(yield func(*model.Page) bool) {
		if dir != "" {
			if _, src, err := store.OpenRun(dir); err == nil {
				defer func() { _ = src.Close() }()
				for i := range src.Len() {
					p, err := src.Page(i)
					if err != nil || !yield(p) {
						return
					}
				}
				return
			}
		}
		for i := range run.Pages {
			if !yield(&run.Pages[i]) {
				return
			}
		}
	}
}

// notAudited counts the pages of an --eaa run whose a11y or keyboard
// audit didn't finish. Their evidence is missing, so the run exits 3.
// An interrupted run is partial anyway and exits 130. A page that
// answered with an HTTP error has no content of its own to test: the
// health module reports it and the draft still lists it as not
// tested. It doesn't make the evidence of the other pages
// incomplete.
func notAudited(run *model.Run) int {
	if run == nil || !run.Options.EAA || run.Interrupted {
		return 0
	}
	n := 0
	for _, p := range run.Pages {
		if p.Status >= 400 && p.Error == "" {
			continue
		}
		for _, m := range eaaNeeds {
			if a, ok := p.Audits[m]; ok && !a.Finished() {
				n++
				break
			}
		}
	}
	return n
}

// writePrivate writes a file only its owner can read (0600), in a folder
// only they can open (0700), the way the store keeps a run's files. It
// renames a finished temporary file into place, so a symlink at path is
// replaced, never followed.
func writePrivate(path string, write func(io.Writer) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { //nolint:gosec // a file -o names or one in a run folder
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".write-*") //nolint:gosec // 0600, next to the file -o names
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() //nolint:gosec // the temporary file made above
	if err := write(tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil { //nolint:gosec // the temporary file made above
		return err
	}
	return os.Rename(tmp.Name(), path) //nolint:gosec // as above
}

// eaaFolder refuses a run folder whose eaa/ is a symbolic link, for
// example one planted in a restored CI cache: the evidence pack and the
// draft would land wherever it points. MkdirAll follows such a link and
// writePrivate only replaces a linked file, not a linked folder.
func eaaFolder(dir string) error {
	path := filepath.Join(dir, eaaDir)
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 { //nolint:gosec // a run folder of the store
		return fmt.Errorf("%s is a symbolic link; not writing the EAA files through it", path)
	}
	return nil
}

// errNotAudited words the exit 3 of an --eaa run with pages its tests
// couldn't finish.
func errNotAudited(n int) error {
	return errors.New("the accessibility evidence is incomplete: the a11y or keyboard tests didn't finish on " +
		plural(n, "page") + " (see \"not checked\" above), so the statement marks them not tested")
}
