// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package review

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"

	"github.com/RhythmShahriar/rhythm/kit/cli"
	"github.com/RhythmShahriar/rhythm/kit/ui"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/report"
)

// ErrNoTTY is returned by Terminal when it isn't run in an interactive
// terminal: in CI or a pipe nobody can press a key.
var ErrNoTTY = errors.New("review needs an interactive terminal; in scripts use the HTML report or accept changes by id")

// Tally counts what a review decided.
type Tally struct {
	Accepted, Ignored, Skipped int
	Total                      int // changes offered
}

// String is "2 accepted, 1 ignored, 4 skipped (of 7)".
func (t Tally) String() string {
	return fmt.Sprintf("%d accepted, %d ignored, %d skipped (of %d)", t.Accepted, t.Ignored, t.Skipped, t.Total)
}

// Terminal steps through changes one at a time: a accepts, i ignores
// always, s skips and q quits. It calls decide for every accept and
// ignore, prints the tally when it ends and refuses to start when in or
// out isn't a terminal.
func Terminal(ctx context.Context, in io.Reader, out io.Writer, changes []model.Change, decide Decide) error {
	f, ok := in.(*os.File)
	if !ok || !term.IsTerminal(f.Fd()) || !cli.IsTerminal(out) {
		return ErrNoTTY
	}
	if len(changes) == 0 {
		fmt.Fprintln(out, "Nothing to review: no changes since the snapshot.")
		return nil
	}
	m := newTerminalModel(changes, decide)
	final, err := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out)).Run()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	fmt.Fprintln(out, "Reviewed: "+final.(*terminalModel).tally.String())
	return nil
}

// terminalModel is the Bubble Tea model behind Terminal.
type terminalModel struct {
	changes []model.Change
	decide  Decide
	at      int    // the change on screen
	status  string // what happened to the previous change, or why it failed
	failed  bool   // status is an error
	width   int
	tally   Tally
	done    bool
}

func newTerminalModel(changes []model.Change, decide Decide) *terminalModel {
	return &terminalModel{changes: changes, decide: decide, width: 80, tally: Tally{Total: len(changes)}}
}

func (m *terminalModel) Init() tea.Cmd { return nil }

func (m *terminalModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tea.KeyMsg:
		switch msg.String() {
		case "a":
			return m.choose(Accept)
		case "i":
			return m.choose(Ignore)
		case "s":
			return m.choose(Skip)
		case "q", "esc", "ctrl+c":
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

// choose records the action for the change on screen and moves on. When
// decide fails the change stays on screen with the error.
func (m *terminalModel) choose(a Action) (tea.Model, tea.Cmd) {
	c := m.changes[m.at]
	if a != Skip {
		if err := m.decide(c, a); err != nil {
			m.status, m.failed = "Couldn't "+string(a)+" it: "+clean(err.Error()), true
			return m, nil
		}
	}
	switch a {
	case Accept:
		m.tally.Accepted++
		m.status = "Accepted: "
	case Ignore:
		m.tally.Ignored++
		m.status = "Ignored from now on: "
	default:
		m.tally.Skipped++
		m.status = "Skipped: "
	}
	m.status += clean(c.Title)
	m.failed = false
	m.at++
	if m.at == len(m.changes) {
		m.done = true
		return m, tea.Quit
	}
	return m, nil
}

var (
	dim     = ui.Dim
	bold    = lipgloss.NewStyle().Bold(true)
	errText = lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Danger))
	keyText = lipgloss.NewStyle().Foreground(ui.Signal).Bold(true)
)

var labels = map[string]string{
	model.Broke: "BROKE", model.New: "NEW", model.Changed: "CHANGED", model.Slower: "SLOWER", model.Fixed: "FIXED",
}

func (m *terminalModel) View() string {
	if m.done {
		return ""
	}
	c := m.changes[m.at]
	w := max(40, m.width-4)
	var b strings.Builder
	head := fmt.Sprintf("Change %d of %d", m.at+1, len(m.changes))
	fmt.Fprintf(&b, "\n  %s%s%s\n\n", bold.Render(head), strings.Repeat(" ", max(2, w-len(head)-8)), dim.Render(ID(c)))
	badge := ui.Badge(labels[c.Class], report.ClassColor(c.Class), true, 9)
	fmt.Fprintf(&b, "  %s %s\n", badge, bold.Render(ui.Truncate(clean(c.Title), w-11)))
	pad := "            "
	where := pagesText(c.Pages)
	if c.Before != "" || c.After != "" {
		where += " · " + orDash(c.Before) + " → " + orDash(c.After)
	}
	fmt.Fprintf(&b, "%s%s\n", pad, dim.Render(ui.Truncate(clean(where), w-11)))
	if d := firstLine(c.Detail); d != "" {
		fmt.Fprintf(&b, "%s%s\n", pad, ui.Truncate(clean(d), w-11))
	}
	if c.Source != "" {
		fmt.Fprintf(&b, "%s%s\n", pad, dim.Render(ui.Truncate("source: "+clean(c.Source), w-11)))
	}
	if f := firstLine(c.Fix); f != "" {
		fmt.Fprintf(&b, "%s%s\n", pad, ui.Truncate("→ "+clean(f), w-11))
	}
	fmt.Fprintf(&b, "%s%s\n\n", pad, dim.Render(c.Check))
	keys := []string{keyText.Render("a") + " accept", keyText.Render("i") + " ignore always",
		keyText.Render("s") + " skip", keyText.Render("q") + " quit"}
	fmt.Fprintf(&b, "  %s\n", strings.Join(keys, "   "))
	if m.status != "" {
		st := dim
		if m.failed {
			st = errText
		}
		fmt.Fprintf(&b, "\n  %s\n", st.Render(ui.Truncate(m.status, w)))
	}
	return b.String()
}

// pagesText is "/shop/", "/a/, /b/ +3 more" or "site-wide".
func pagesText(pages []string) string {
	switch {
	case len(pages) == 0:
		return "site-wide"
	case len(pages) <= 2:
		return strings.Join(pages, ", ")
	}
	return fmt.Sprintf("%s +%d more", strings.Join(pages[:2], ", "), len(pages)-2)
}

func orDash(s string) string {
	if s == "" {
		return "–"
	}
	return s
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

// clean replaces control characters with spaces. Titles and details come
// from the site, and an escape sequence in a page title must not be able
// to move the cursor or recolor the terminal.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}
