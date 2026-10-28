// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package fleet

import (
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"slices"
	"time"

	"github.com/RhythmShahriar/soundcheck/history"
)

//go:embed fleet.html.tmpl
var pageSource string

var pageTemplate = template.Must(template.New("fleet").Parse(pageSource))

// Page is everything on the fleet page and what fleet.json holds.
type Page struct {
	Mode    string    `json:"mode"` // snapshot or compare
	Created time.Time `json:"created"`
	Sites   []Result  `json:"sites"` // worst first
}

type pageData struct {
	Title, Created, Mode string
	Chips                []pageChip
	Rows                 []pageRow
	// EAA shows the accessibility columns: some site ran with --eaa.
	EAA bool
}

type pageChip struct {
	Key, Label string
	N          int
}

type pageRow struct {
	Rank                   int
	Site, URL, Report, Dir string
	Verdict, Label, Counts string
	Pages, Time, Error     string
	Trend                  template.HTML
	TrendLabel             string
	// EAA marks a row with accessibility columns; other rows leave the
	// cells empty, which isn't 0.
	EAA                     bool
	EAAOpen, CriteriaFailed int
	EAAArrow, EAATrend      string // "↑" and "up from 9 at the last fleet run"
	EAAKind                 string // up, down or same, for the arrow's color
	Statement               string
}

// HTML writes the fleet page: one standalone file ranking the sites by
// their worst result, each linking to its own report.
func HTML(w io.Writer, p Page) error {
	d := pageData{Created: p.Created.Local().Format("2 Jan 2006, 15:04"), Mode: "Snapshots"}
	if p.Mode == Compare {
		d.Mode = "Comparisons"
	}
	d.Title = fmt.Sprintf("Soundcheck fleet · %d sites", len(p.Sites))
	n := map[string]int{}
	for i, r := range p.Sites {
		n[r.Verdict]++
		row := pageRow{Rank: i + 1, Site: r.Site, URL: r.URL, Report: r.Report, Dir: r.Dir, Verdict: r.Verdict,
			Label: r.Label(), Pages: fmt.Sprint(r.Pages), Error: r.Error,
			Time: (time.Duration(r.Duration) * time.Millisecond).Round(time.Second).String()}
		for _, k := range r.countKeys() {
			if c := r.Counts[k]; c > 0 {
				if row.Counts != "" {
					row.Counts += ", "
				}
				row.Counts += fmt.Sprintf("%d %s", c, countName(k, c))
			}
		}
		if len(r.Trend) > 1 {
			row.Trend = history.SVG(r.Trend)
			row.TrendLabel = fmt.Sprintf("LCP %s to %s over the last %d runs", lcpLabel(r.Trend[0]), lcpLabel(r.Trend[len(r.Trend)-1]), len(r.Trend))
		}
		if r.EAAOpen != nil {
			d.EAA = true
			row.EAA, row.EAAOpen, row.Statement = true, *r.EAAOpen, r.Statement
			if r.CriteriaFailed != nil {
				row.CriteriaFailed = *r.CriteriaFailed
			}
			if r.EAABefore != nil {
				row.EAAKind = r.EAATrend
				switch r.EAATrend {
				case TrendUp:
					row.EAAArrow, row.EAATrend = "↑", fmt.Sprintf("up from %d at the last fleet run", *r.EAABefore)
				case TrendDown:
					row.EAAArrow, row.EAATrend = "↓", fmt.Sprintf("down from %d at the last fleet run", *r.EAABefore)
				case TrendSame:
					row.EAAArrow, row.EAATrend = "→", "same as at the last fleet run"
				}
			}
		}
		if row.Counts == "" && r.Verdict != Failed {
			row.Counts = "–"
		}
		d.Rows = append(d.Rows, row)
	}
	// A chip per verdict and wording: clean --eaa sites read "Nothing
	// changed", not "Clean", so they get chips of their own.
	for _, v := range verdictOrder {
		if n[v] == 0 {
			continue
		}
		var chips []pageChip
		for _, row := range d.Rows {
			if row.Verdict != v {
				continue
			}
			k := slices.IndexFunc(chips, func(c pageChip) bool { return c.Label == row.Label })
			if k < 0 {
				chips = append(chips, pageChip{v, row.Label, 0})
				k = len(chips) - 1
			}
			chips[k].N++
		}
		d.Chips = append(d.Chips, chips...)
	}
	return pageTemplate.Execute(w, d)
}

// lcpLabel is "1.4 s" or "840 ms".
func lcpLabel(v float64) string {
	if v < 1000 {
		return fmt.Sprintf("%.0f ms", v)
	}
	return fmt.Sprintf("%.1f s", v/1000)
}
