package report

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/RhythmShahriar/rhythm/kit/ui"
	"github.com/RhythmShahriar/soundcheck/model"
)

const (
	width      = 76 // content width of the report
	badgeWidth = 10
	indent     = badgeWidth + 2
)

type PrettyOptions struct {
	Plain    bool // no color, box drawing or decorative glyphs; for screen readers and logs
	Quiet    bool // results only
	Progress bool // redraw a progress line; only for interactive terminals
	Verbose  bool // a line per page and the browser's log
}

// Pretty renders a run for a terminal. Header, Step, Discovered, Page
// and Log follow the run as it happens; Summary prints the result.
//
// In plain mode every page is a line as it finishes, for logs and screen
// readers. Otherwise a live progress line shows how far the run is and
// Summary prints the grouped report at the end.
type Pretty struct {
	w, errw io.Writer
	o       PrettyOptions
	start   time.Time
	drawn   bool
	errors  int // pages with errors so far, for the progress line
}

func NewPretty(w, errw io.Writer, o PrettyOptions) *Pretty {
	return &Pretty{w: w, errw: errw, o: o, start: time.Now()}
}

var (
	muted = lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Muted))
	bold  = lipgloss.NewStyle().Bold(true)
)

// ClassColor is the theme color of a change class.
func ClassColor(class string) lipgloss.Color {
	switch class {
	case model.Broke:
		return lipgloss.Color(ui.Broke)
	case model.New:
		return lipgloss.Color(ui.New)
	case model.Changed:
		return lipgloss.Color(ui.Changed)
	case model.Slower:
		return lipgloss.Color(ui.Slower)
	case model.Fixed:
		return lipgloss.Color(ui.Fixed)
	}
	return lipgloss.Color(ui.Muted)
}

// LevelColor is the theme color of an issue level.
func LevelColor(l model.Level) lipgloss.Color {
	switch l {
	case model.Error:
		return lipgloss.Color(ui.Danger)
	case model.Warning:
		return lipgloss.Color(ui.High)
	}
	return lipgloss.Color(ui.Muted)
}

func (p *Pretty) clear() {
	if p.drawn {
		fmt.Fprint(p.w, "\r\x1b[2K")
		p.drawn = false
	}
}

// Header starts the report: tool, version and the site.
func (p *Pretty) Header(version, target string) {
	if p.o.Quiet {
		return
	}
	if p.o.Plain {
		fmt.Fprintf(p.w, "rhythm soundcheck %s - %s\n\n", version, target)
		return
	}
	left := ui.Brand.Render("♪") + " " + bold.Render("rhythm soundcheck") + " " + muted.Render(version)
	gap := max(2, width-lipgloss.Width(left)-lipgloss.Width(target))
	fmt.Fprintf(p.w, "\n  %s%s%s\n  %s\n\n", left, strings.Repeat(" ", gap), muted.Render(target),
		muted.Render(strings.Repeat("─", width)))
}

// Step prints one labeled line of what the run is doing.
func (p *Pretty) Step(label, value string) {
	if p.o.Quiet {
		return
	}
	p.clear()
	if p.o.Plain {
		fmt.Fprintf(p.w, "%s: %s\n", label, value)
		return
	}
	fmt.Fprintf(p.w, "  %s %s %s\n", ui.Brand.Render("▸"), muted.Bold(true).Width(13).Render(label), value)
}

// Discovered reports how many pages will be checked.
func (p *Pretty) Discovered(pages int, skipped map[string]int) {
	// Links found in rendered pages are added as the run goes, so this is
	// where it starts; the summary has the final count.
	line := plural(pages, "page", "pages") + " to start"
	if n := total(skipped); n > 0 {
		var why []string
		for _, k := range sortedKeys(skipped) {
			why = append(why, fmt.Sprintf("%s %d", k, skipped[k]))
		}
		line += fmt.Sprintf(" · %d left out (%s)", n, strings.Join(why, ", "))
	}
	p.Step("Pages", line)
}

var spinner = []string{"◐", "◓", "◑", "◒"}

// Page reports a visited page.
func (p *Pretty) Page(pg *model.Page, done, total int) {
	bad := pg.Error != "" || slices.ContainsFunc(pg.Issues, func(i model.Issue) bool { return i.Level == model.Error })
	if bad {
		p.errors++
	}
	if p.o.Quiet {
		return
	}
	if p.o.Plain || p.o.Verbose {
		p.clear()
		status := fmt.Sprint(pg.Status)
		if pg.Error != "" {
			status = "failed: " + pg.Error
		}
		mark := "ok"
		if bad {
			mark = "error"
		} else if len(pg.Issues) > 0 {
			mark = plural(len(pg.Issues), "issue", "issues")
		}
		line := fmt.Sprintf("%s %s %s (%s)", pg.Path, status, time.Duration(pg.Duration*float64(time.Millisecond)).Round(10*time.Millisecond), mark)
		if p.o.Plain {
			fmt.Fprintf(p.w, "[%d/%d] %s\n", done, total, line)
		} else {
			fmt.Fprintln(p.errw, muted.Render("  → "+line))
		}
	}
	if !p.o.Progress || p.o.Plain || done >= total {
		p.clear()
		return
	}
	const bar = 24
	fill := bar * done / max(total, 1)
	tally := ""
	if p.errors > 0 {
		tally = "  " + lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Danger)).Bold(true).Render(fmt.Sprintf("✗ %d", p.errors))
	}
	fmt.Fprintf(p.w, "\r\x1b[2K  %s %s %s  %s%s",
		ui.Brand.Render(spinner[done%len(spinner)]),
		muted.Bold(true).Width(13).Render("Checking"),
		ui.Brand.Render(strings.Repeat("━", fill))+muted.Render(strings.Repeat("━", bar-fill)),
		muted.Render(fmt.Sprintf("%d/%d · %s", done, total, time.Since(p.start).Round(100*time.Millisecond))),
		tally)
	p.drawn = true
}

// Log prints a message from the run (browser downloads, warnings) when
// verbose, or always for lines that need the user's attention.
func (p *Pretty) Log(msg string) {
	if !p.o.Verbose && !strings.HasPrefix(msg, "!") {
		return
	}
	p.clear()
	msg = strings.TrimPrefix(msg, "!")
	if p.o.Plain {
		fmt.Fprintln(p.errw, strings.TrimSpace(msg))
		return
	}
	fmt.Fprintln(p.errw, muted.Render("  · "+strings.TrimSpace(msg)))
}

// Summary prints the result.
func (p *Pretty) Summary(r Result, hint string) {
	p.clear()
	if r.Compared() {
		p.comparison(r, hint)
	} else {
		p.run(r, hint)
	}
}

func (p *Pretty) comparison(r Result, hint string) {
	c := r.Comparison
	groups := byClass(c.Changes)
	if p.o.Plain {
		if len(c.Changes) > 0 {
			fmt.Fprintln(p.w)
		}
		for _, class := range model.Classes {
			for _, ch := range groups[class] {
				fmt.Fprintf(p.w, "[%s] %s - %s\n", classes[class].Label, ch.Title, pagesLabel(ch.Pages, 3))
				if ba := beforeAfter(ch); ba != "" {
					fmt.Fprintf(p.w, "  %s\n", ba)
				}
				if ch.Source != "" {
					fmt.Fprintf(p.w, "  source: %s\n", ch.Source)
				}
				if ch.Detail != "" {
					fmt.Fprintf(p.w, "  %s\n", firstLine(ch.Detail))
				}
				if fix := firstLine(ch.Fix); fix != "" && class != model.Fixed {
					fmt.Fprintf(p.w, "  fix: %s\n", fix)
				}
			}
		}
		if p.o.Quiet {
			return
		}
		var parts []string
		for _, class := range model.Classes {
			parts = append(parts, fmt.Sprintf("%d %s", len(groups[class]), class))
		}
		fmt.Fprintf(p.w, "\n%s - %s - compared with %s, %s\n", verdict(c), strings.Join(parts, ", "),
			snapshotName(c.Before), r.stats())
		p.notes(c.Warnings)
		if hint != "" {
			fmt.Fprintln(p.w, hint)
		}
		return
	}

	if len(c.Changes) > 0 {
		fmt.Fprintln(p.w)
	}
	for _, class := range model.Classes {
		cs := groups[class]
		if len(cs) == 0 {
			continue
		}
		color := ClassColor(class)
		fmt.Fprintln(p.w, "  "+ui.Section(classes[class].Label, len(cs), color, width)+"\n")
		for _, ch := range cs {
			if class == model.Broke || class == model.New {
				fmt.Fprintln(p.w, changeCard(ch))
				fmt.Fprintln(p.w)
			} else {
				fmt.Fprintln(p.w, changeLine(ch))
			}
		}
		if class != model.Broke && class != model.New {
			fmt.Fprintln(p.w)
		}
	}
	if p.o.Quiet {
		return
	}
	fmt.Fprintln(p.w, comparePanel(c))
	fmt.Fprintf(p.w, "  %s\n", muted.Render(fmt.Sprintf("compared with %s · %s", snapshotName(c.Before), r.stats())))
	p.notes(c.Warnings)
	if hint != "" {
		fmt.Fprintf(p.w, "\n  %s %s\n", ui.Brand.Render("→"), muted.Render(hint))
	}
	fmt.Fprintln(p.w)
}

func (p *Pretty) run(r Result, hint string) {
	issues := Issues(r.Run)
	if p.o.Plain {
		if len(issues) > 0 {
			fmt.Fprintln(p.w)
		}
		for _, i := range issues {
			fmt.Fprintf(p.w, "[%s] %s - %s\n", strings.ToUpper(string(i.Level)), i.Title, pagesLabel(i.Pages, 3))
			if i.Source != "" {
				fmt.Fprintf(p.w, "  source: %s\n", i.Source)
			}
			if d := firstLine(i.Detail); d != "" && len(i.Pages) <= 1 {
				fmt.Fprintf(p.w, "  %s\n", d)
			}
			if fix := firstLine(i.Fix); fix != "" && i.Level != model.Notice {
				fmt.Fprintf(p.w, "  fix: %s\n", fix)
			}
		}
		if p.o.Quiet {
			return
		}
		counts := Counts(issues)
		fmt.Fprintf(p.w, "\n%s - %s, %s, %s - %s\n", runVerdict(r.Run, counts),
			plural(counts[model.Error], "error", "errors"), plural(counts[model.Warning], "warning", "warnings"),
			plural(counts[model.Notice], "notice", "notices"), r.stats())
		p.notes(r.Run.Warnings)
		if hint != "" {
			fmt.Fprintln(p.w, hint)
		}
		return
	}

	if len(issues) > 0 {
		fmt.Fprintln(p.w)
	}
	for _, level := range Levels {
		var group []model.Issue
		for _, i := range issues {
			if i.Level == level {
				group = append(group, i)
			}
		}
		if len(group) == 0 {
			continue
		}
		fmt.Fprintln(p.w, "  "+ui.Section(strings.ToUpper(string(level))+"S", len(group), LevelColor(level), width)+"\n")
		for _, i := range group {
			if level == model.Notice {
				fmt.Fprintln(p.w, issueLine(i))
			} else {
				fmt.Fprintln(p.w, issueCard(i))
				fmt.Fprintln(p.w)
			}
		}
		if level == model.Notice {
			fmt.Fprintln(p.w)
		}
	}
	if p.o.Quiet {
		return
	}
	fmt.Fprintln(p.w, runPanel(r.Run, Counts(issues)))
	fmt.Fprintf(p.w, "  %s\n", muted.Render(r.stats()))
	p.notes(r.Run.Warnings)
	if hint != "" {
		fmt.Fprintf(p.w, "\n  %s %s\n", ui.Brand.Render("→"), muted.Render(hint))
	}
	fmt.Fprintln(p.w)
}

// notes prints run warnings, at most a few of each kind.
func (p *Pretty) notes(lists ...[]string) {
	var all []string
	for _, l := range lists {
		all = append(all, l...)
	}
	const show = 5
	for i, n := range all {
		if i == show {
			n = fmt.Sprintf("…and %d more warnings in the report", len(all)-show)
		}
		if p.o.Plain {
			fmt.Fprintf(p.w, "note: %s\n", n)
		} else {
			fmt.Fprintf(p.w, "  %s\n", muted.Render("! "+ui.Truncate(n, width-2)))
		}
		if i == show {
			break
		}
	}
}

// stats is "124 pages · 2m10s".
func (r Result) stats() string {
	parts := []string{plural(len(r.Run.Pages), "page", "pages")}
	failed := 0
	for _, p := range r.Run.Pages {
		if p.Error != "" {
			failed++
		}
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d couldn't be loaded", failed))
	}
	parts = append(parts, (time.Duration(r.Run.Duration) * time.Millisecond).Round(time.Second).String())
	return strings.Join(parts, " · ")
}

// changeCard renders a BROKE or NEW change with its evidence and fix.
func changeCard(c model.Change) string {
	color := ClassColor(c.Class)
	titleCol := width - indent - 2
	where := countLabel(c.Pages)
	if len(c.Pages) == 1 {
		where = c.Pages[0]
	}
	head := ui.Badge(classes[c.Class].Label, color, true, badgeWidth) + "  "
	listed := len(c.Pages) == 1
	if lipgloss.Width(c.Title)+len(where)+2 <= titleCol {
		head += ui.Pad(bold.Render(c.Title), titleCol-len(where)) + muted.Render(where)
	} else {
		head += bold.Render(ui.Truncate(c.Title, titleCol))
		listed = false
	}
	body := []string{head}
	pad := strings.Repeat(" ", indent)
	add := func(s string, style lipgloss.Style) {
		body = append(body, pad+style.Render(ui.Truncate(s, width-indent-2)))
	}
	if !listed {
		add(pagesLabel(c.Pages, 3), muted)
	}
	if ba := beforeAfter(c); ba != "" {
		add(ba, muted)
	}
	if c.Source != "" {
		add(c.Source, muted)
	}
	if d := firstLine(c.Detail); d != "" {
		add(d, muted.Italic(true))
	}
	if fix := firstLine(c.Fix); fix != "" {
		body = append(body, pad+ui.Brand.Render("→")+" "+ui.Truncate(fix, width-indent-4))
	}
	rail := lipgloss.NewStyle().Border(lipgloss.Border{Left: "▌"}, false, false, false, true).
		BorderForeground(color).PaddingLeft(1).MarginLeft(1)
	return rail.Render(strings.Join(body, "\n"))
}

// changeLine renders a CHANGED, SLOWER or FIXED change on one line.
func changeLine(c model.Change) string {
	marker := lipgloss.NewStyle().Foreground(ClassColor(c.Class)).Bold(true).Render(classes[c.Class].Glyph)
	right := countLabel(c.Pages)
	if len(c.Pages) == 1 {
		right = c.Pages[0]
	}
	if ba := beforeAfter(c); ba != "" && c.Class == model.Slower {
		right = ba + "  " + right
	}
	right = ui.Truncate(right, 30)
	line := ui.Truncate(c.Title, width-8-len(right)-2)
	return "    " + marker + "  " + ui.Pad(line, width-6-lipgloss.Width(right)) + muted.Render(right)
}

func issueCard(i model.Issue) string {
	color := LevelColor(i.Level)
	titleCol := width - indent - 2
	where := countLabel(i.Pages)
	if len(i.Pages) == 1 {
		where = i.Pages[0]
	}
	head := ui.Badge(strings.ToUpper(string(i.Level)), color, true, badgeWidth) + "  "
	if lipgloss.Width(i.Title)+len(where)+2 <= titleCol {
		head += ui.Pad(bold.Render(i.Title), titleCol-len(where)) + muted.Render(where)
	} else {
		head += bold.Render(ui.Truncate(i.Title, titleCol))
	}
	body := []string{head}
	pad := strings.Repeat(" ", indent)
	add := func(s string, style lipgloss.Style) {
		body = append(body, pad+style.Render(ui.Truncate(s, width-indent-2)))
	}
	if len(i.Pages) > 1 {
		add(pagesLabel(i.Pages, 3), muted)
	}
	if i.Source != "" {
		add(i.Source, muted)
	}
	if fix := firstLine(i.Fix); fix != "" {
		body = append(body, pad+ui.Brand.Render("→")+" "+ui.Truncate(fix, width-indent-4))
	}
	rail := lipgloss.NewStyle().Border(lipgloss.Border{Left: "▌"}, false, false, false, true).
		BorderForeground(color).PaddingLeft(1).MarginLeft(1)
	return rail.Render(strings.Join(body, "\n"))
}

func issueLine(i model.Issue) string {
	marker := lipgloss.NewStyle().Foreground(LevelColor(i.Level)).Bold(true).Render(levelGlyphs[i.Level])
	right := countLabel(i.Pages)
	if len(i.Pages) == 1 {
		right = ui.Truncate(i.Pages[0], 24)
	}
	line := ui.Truncate(i.Title, width-8-len(right)-2)
	return "    " + marker + "  " + ui.Pad(line, width-6-lipgloss.Width(right)) + muted.Render(right)
}

// verdict is the one-line answer to "did my change break anything?".
func verdict(c *model.Comparison) string {
	switch {
	case c.Count(model.Broke) > 0:
		return "Something broke"
	case c.Count(model.New) > 0:
		return "New problems"
	case c.Count(model.Changed)+c.Count(model.Slower) > 0:
		return "Nothing broke; some pages changed"
	}
	return "Nothing changed"
}

func runVerdict(r *model.Run, counts map[model.Level]int) string {
	prefix := ""
	if r.Name != "" {
		prefix = fmt.Sprintf("Snapshot %q saved; ", r.Name)
	}
	switch {
	case counts[model.Error] > 0:
		return prefix + "the site already has errors"
	case counts[model.Warning] > 0:
		return prefix + "a few warnings"
	}
	return prefix + "all clear"
}

// snapshotName is `snapshot "good" (3h ago)` for the terminal.
func snapshotName(r model.RunRef) string { return refName(r, true) }

// snapshotAt is `snapshot "good" (23 Oct 2026 18:04 UTC)`, for saved
// reports where "3h ago" would go stale.
func snapshotAt(r model.RunRef) string { return refName(r, false) }

func refName(r model.RunRef, relative bool) string {
	when := ""
	switch {
	case r.Started.IsZero():
	case relative:
		when = " (" + Ago(r.Started) + ")"
	default:
		when = " (" + r.Started.UTC().Format("2 Jan 2006 15:04 MST") + ")"
	}
	if r.Name != "" {
		return fmt.Sprintf("snapshot %q%s", r.Name, when)
	}
	return r.Target + when
}

// Ago is a short "3h ago".
func Ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// comparePanel draws the verdict and the count of each class.
func comparePanel(c *model.Comparison) string {
	color := lipgloss.Color(ui.Ok)
	switch {
	case c.Count(model.Broke) > 0:
		color = ClassColor(model.Broke)
	case c.Count(model.New) > 0:
		color = ClassColor(model.New)
	case c.Count(model.Changed)+c.Count(model.Slower) > 0:
		color = ClassColor(model.Changed)
	}
	head := bold.Render("SOUNDCHECK") + "   " + lipgloss.NewStyle().Foreground(color).Bold(true).Render(verdict(c))
	var parts []string
	for _, class := range model.Classes {
		n := c.Count(class)
		style := muted
		if n > 0 {
			style = lipgloss.NewStyle().Foreground(ClassColor(class)).Bold(true)
		}
		parts = append(parts, style.Render(fmt.Sprintf("%s %d", classes[class].Glyph, n))+" "+muted.Render(class))
	}
	return ui.Panel(head+"\n"+strings.Join(parts, "   "), color)
}

func runPanel(r *model.Run, counts map[model.Level]int) string {
	color := lipgloss.Color(ui.Ok)
	switch {
	case counts[model.Error] > 0:
		color = LevelColor(model.Error)
	case counts[model.Warning] > 0:
		color = LevelColor(model.Warning)
	}
	title := "SOUNDCHECK"
	if r.Name != "" {
		title = "SNAPSHOT " + strings.ToUpper(r.Name)
	}
	head := bold.Render(title) + "   " + lipgloss.NewStyle().Foreground(color).Bold(true).Render(capitalize(runVerdict(r, counts)))
	if r.Name != "" {
		head = bold.Render(title) + "   " + lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Ok)).Bold(true).Render("saved")
	}
	var parts []string
	for _, l := range Levels {
		n := counts[l]
		style := muted
		if n > 0 {
			style = lipgloss.NewStyle().Foreground(LevelColor(l)).Bold(true)
		}
		parts = append(parts, style.Render(fmt.Sprintf("%s %d", levelGlyphs[l], n))+" "+muted.Render(strings.TrimPrefix(plural(n, string(l), string(l)+"s"), fmt.Sprint(n)+" ")))
	}
	body := strings.Join(parts, "   ")
	if counts[model.Error]+counts[model.Warning]+counts[model.Notice] == 0 {
		body = ui.Good.Render("✓ no problems found")
	}
	return ui.Panel(head+"\n"+body, color)
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
