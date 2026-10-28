// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package statement

import (
	_ "embed" // statement.html.tmpl
	"encoding/json"
	"html/template"
	"io"
	"strings"

	"github.com/RhythmShahriar/soundcheck/model"
)

// Markdown writes d as Markdown and sets d.Todo to the [TO DO] markers
// in it. Every piece of text is escaped (markdownText), so a page path
// or the owner's text can't add a link, a tag or a table cell.
func Markdown(w io.Writer, d *Draft) error {
	doc := build(d)
	d.Todo = doc.Todo
	var b strings.Builder
	for _, l := range doc.Banner {
		b.WriteString("> " + mdLine(l) + "\n")
		if l[0].Text != BannerEnd {
			b.WriteString(">\n")
		}
	}
	b.WriteString("\n# " + markdownText(doc.Title) + "\n")
	for _, bl := range doc.Blocks {
		b.WriteString("\n")
		switch bl.Kind {
		case blockH2:
			b.WriteString("## " + mdLine(bl.Lines[0]) + "\n")
		case blockH3:
			b.WriteString("### " + mdLine(bl.Lines[0]) + "\n")
		case blockPara:
			b.WriteString(leadEscape(mdLine(bl.Lines[0])) + "\n")
		case blockList:
			for _, l := range bl.Lines {
				b.WriteString("- " + leadEscape(mdLine(l)) + "\n")
			}
		case blockTable:
			cells := make([]string, len(bl.Head))
			for i, h := range bl.Head {
				cells[i] = markdownText(h)
			}
			b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
			b.WriteString(strings.Repeat("|---", len(bl.Head)) + "|\n")
			for _, r := range bl.Rows {
				for i, c := range r {
					cells[i] = mdLine(c)
				}
				b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
			}
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// mdLine writes the spans of a line as Markdown.
func mdLine(l line) string {
	var b strings.Builder
	for _, s := range l {
		switch s.Kind {
		case spanStrong:
			b.WriteString("**" + markdownText(s.Text) + "**")
		case spanTodo:
			b.WriteString("[TO DO: " + markdownText(s.Text) + "]")
		default:
			b.WriteString(markdownText(s.Text))
		}
	}
	return b.String()
}

//go:embed statement.html.tmpl
var htmlTemplate string

// page is the HTML template: html/template escapes every value by its
// context, so the only markup is the template's own.
var page = template.Must(template.New("statement").Parse(htmlTemplate))

// HTML writes d as a standalone, printable HTML page and sets d.Todo.
// The page loads nothing and runs nothing: its content security policy
// allows no scripts and no requests, only its own inline style.
func HTML(w io.Writer, d *Draft) error {
	doc := build(d)
	d.Todo = doc.Todo
	return page.Execute(w, doc)
}

// SchemaJSON is the schema of a draft written as JSON.
const SchemaJSON = "rhythm-soundcheck/statement/v1"

// jsonDraft is the JSON form of a draft (model/schema/statement.v1.json).
type jsonDraft struct {
	Schema       string        `json:"schema"`
	Tool         string        `json:"tool"`
	Version      string        `json:"version"`
	Date         string        `json:"date"`
	Layout       string        `json:"layout"`
	WCAG         string        `json:"wcag"`
	EN301549     string        `json:"en301549"`
	Standard     string        `json:"standard"`
	Status       *string       `json:"status"` // always null: the owner chooses it
	EvidenceNote string        `json:"evidence_note"`
	Todo         int           `json:"todo"`
	Website      string        `json:"website"`
	Owner        jsonOwner     `json:"owner"`
	Pages        int           `json:"pages"`
	PrivatePages int           `json:"private_pages"`
	Checked      []string      `json:"checked"`
	Axe          string        `json:"axe"`
	AxeRules     int           `json:"axe_rules"`
	OwnChecks    int           `json:"own_checks"`
	Totals       jsonTotals    `json:"totals"`
	Criteria     []jsonRow     `json:"criteria"`
	KnownIssues  []jsonKnown   `json:"known_issues"`
	Ignored      []jsonIgnored `json:"ignored"`
	NotAudited   []jsonAudit   `json:"not_audited"`
	Frames       []string      `json:"frames"`
	Keyboard     jsonKeyboard  `json:"keyboard"`
	Notes        []string      `json:"notes"`
	Limits       []string      `json:"limits"`
}

type jsonOwner struct {
	Name         string   `json:"name"`
	Email        string   `json:"email"`
	Phone        string   `json:"phone,omitempty"`
	Address      string   `json:"address,omitempty"`
	ResponseDays int      `json:"response_days,omitempty"`
	Country      string   `json:"country,omitempty"`
	Enforcement  string   `json:"enforcement,omitempty"`
	Scope        []string `json:"scope"`
	Service      string   `json:"service,omitempty"`
}

type jsonTotals struct {
	Criteria  int `json:"criteria"`
	Failed    int `json:"failed"`
	NotTested int `json:"not_tested"`
}

type jsonRow struct {
	ID        string      `json:"id"`
	Title     string      `json:"title"`
	Level     string      `json:"level"`
	Principle string      `json:"principle"`
	EN        string      `json:"en301549"`
	Coverage  string      `json:"coverage"`
	TestedBy  []string    `json:"tested_by"`
	HowTested string      `json:"how_tested"`
	Result    string      `json:"result"`
	Tests     string      `json:"tests"`
	Text      string      `json:"text"`
	Issues    int         `json:"issues"`
	Pages     int         `json:"pages"`
	Paths     []string    `json:"paths"`
	Manual    *jsonManual `json:"manual"`
}

type jsonManual struct {
	Result string `json:"result"`
	By     string `json:"by"`
	Date   string `json:"date"`
}

type jsonKnown struct {
	Criterion string `json:"criterion"`
	Title     string `json:"title"`
	Text      string `json:"text"`
	Until     string `json:"until,omitempty"`
}

type jsonIgnored struct {
	Criteria []string `json:"criteria"`
	Check    string   `json:"check"`
	Pages    []string `json:"pages"`
	Reason   string   `json:"reason"`
}

type jsonAudit struct {
	Path   string `json:"path"`
	Module string `json:"module"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type jsonKeyboard struct {
	PagesWalked   int            `json:"pages_walked"`
	WidgetsTested int            `json:"widgets_tested"`
	FlowsRun      int            `json:"flows_run"`
	FlowsSkipped  []jsonFlowSkip `json:"flows_skipped"`
}

type jsonFlowSkip struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

// JSON writes d as JSON (schema rhythm-soundcheck/statement/v1) and
// sets d.Todo to the markers the Markdown and HTML drafts hold. status
// is always null and lists are [] when empty, never null. Text is
// plain (control and bidirectional characters removed); encoding/json
// escapes <, > and & so the JSON is safe inside HTML too.
func JSON(w io.Writer, d *Draft) error {
	d.Todo = build(d).Todo
	o := d.Owner
	out := jsonDraft{
		Schema: SchemaJSON, Tool: "rhythm-soundcheck", Version: plain(d.Version), Date: plain(d.Date), Layout: d.Layout,
		WCAG: d.WCAG, EN301549: d.EN301549, Standard: d.Standard, EvidenceNote: model.EvidenceNote, Todo: d.Todo,
		Website: plain(o.Website), Pages: d.Pages, PrivatePages: d.PrivatePages, Checked: plainAll(d.Checked),
		Axe: d.Axe, AxeRules: d.AxeRules, OwnChecks: d.OwnChecks, Totals: jsonTotals{len(d.Rows), d.Failed, d.NotTested},
		Owner: jsonOwner{Name: plain(o.Owner), Email: plain(o.Contact.Email), Phone: plain(o.Contact.Phone), Address: plain(o.Contact.Address),
			ResponseDays: o.ResponseDays, Country: plain(o.Country), Enforcement: plain(strings.TrimSpace(o.Enforcement)), Scope: plainAll(o.Scope), Service: plain(o.Service)},
		Criteria: []jsonRow{}, KnownIssues: []jsonKnown{}, Ignored: []jsonIgnored{}, NotAudited: []jsonAudit{},
		Frames: plainAll(d.Frames), Notes: plainAll(d.Notes), Limits: Limits,
		Keyboard: jsonKeyboard{PagesWalked: d.Keyboard.PagesWalked, WidgetsTested: d.Keyboard.WidgetsTested, FlowsRun: d.Keyboard.FlowsRun, FlowsSkipped: []jsonFlowSkip{}},
	}
	for _, f := range d.Keyboard.FlowsSkipped {
		out.Keyboard.FlowsSkipped = append(out.Keyboard.FlowsSkipped, jsonFlowSkip{plain(f.File), plain(f.Reason)})
	}
	for _, r := range d.Rows {
		jr := jsonRow{ID: r.ID, Title: r.Title, Level: r.Level, Principle: r.Principle, EN: r.EN, Coverage: r.Coverage,
			TestedBy: append([]string{}, r.TestedBy...), HowTested: r.HowTested(), Result: r.Result, Tests: r.Tests, Text: plain(ResultText(r)),
			Issues: r.Issues, Pages: r.Pages, Paths: plainAll(r.Paths)}
		if m := r.Manual; m != nil {
			jr.Manual = &jsonManual{plain(m.Result), plain(m.By), plain(m.Date)}
		}
		out.Criteria = append(out.Criteria, jr)
	}
	for _, k := range d.KnownIssues {
		out.KnownIssues = append(out.KnownIssues, jsonKnown{plain(k.Criterion), k.Title, plain(k.Text), plain(k.Until)})
	}
	for _, ig := range d.Ignored {
		out.Ignored = append(out.Ignored, jsonIgnored{append([]string{}, ig.Criteria...), plain(ig.Check), plainAll(ig.Pages), plain(ig.Reason)})
	}
	for _, n := range d.NotAudited {
		out.NotAudited = append(out.NotAudited, jsonAudit{plain(n.Path), plain(n.Module), plain(n.Status), plain(n.Reason)})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// plainAll is plain of every string, never nil.
func plainAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = plain(s)
	}
	return out
}
