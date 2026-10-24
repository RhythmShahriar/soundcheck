// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package fleet

import (
	_ "embed"
	"fmt"
	"html/template"
	"io"
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
		if row.Counts == "" && r.Verdict != Failed {
			row.Counts = "–"
		}
		d.Rows = append(d.Rows, row)
	}
	for _, v := range verdictOrder {
		if n[v] > 0 {
			d.Chips = append(d.Chips, pageChip{v, verdictLabels[v], n[v]})
		}
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
