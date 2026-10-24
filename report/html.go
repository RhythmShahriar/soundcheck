// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"cmp"
	_ "embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"image"
	"image/jpeg"
	_ "image/png" // screenshots are PNG
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/image/draw"

	echomodel "github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/soundcheck/echolink"
	"github.com/RhythmShahriar/soundcheck/history"
	"github.com/RhythmShahriar/soundcheck/model"
)

//go:embed report.html.tmpl
var htmlSource string

var htmlTemplate = template.Must(template.New("report").Parse(htmlSource))

// Embedded screenshots are scaled down and stored as JPEG, so a report
// with dozens of full-page captures stays small enough to attach or mail.
const (
	shotWidth     = 960
	shotMaxHeight = 9000
	shotQuality   = 74
	maxShotSets   = 60 // visual changes with embedded images; later ones link to the files
)

type htmlData struct {
	Title, Version, Target, Created, Stats string
	Compared                               bool
	Partial                                string // why the result is incomplete, when it is
	Verdict, Tone                          string
	Against                                string
	Chips                                  []htmlChip
	Groups                                 []htmlGroup
	Pages                                  []htmlPage
	Notes                                  []string
	Echo                                   *htmlEcho
	Trend                                  template.HTML // a sparkline of the site's LCP over its latest runs
	TrendLabel                             string
}

// htmlEcho lists what each form submit and flow sent, when Echo ran.
type htmlEcho struct {
	Summary, URL string
	Rows         []htmlMessage
}

type htmlMessage struct {
	Action, Page, Channel, To, Subject string
	None                               bool // the action sent nothing
}

type htmlChip struct {
	Key, Label, Glyph string
	N                 int
}

type htmlGroup struct {
	Key, Label, Glyph, Blurb string
	Items                    []htmlItem
}

type htmlItem struct {
	// ID is the change's id (data-id on its article), which the review
	// bar pairs it by; empty for a run's issues.
	ID                              string
	Key, Label, Title, Check, Where string
	Pages                           []string
	BeforeAfter, Source, Detail     string
	Fix                             string
	Score                           string
	Shots                           *htmlShots
	Files                           []string // image files too many to embed
}

type htmlShots struct {
	Before, After, Diff template.URL
	Phone               bool // a mobile capture, shown at phone width
}

type htmlPage struct {
	Path, URL, Status, Issues, LCP, CLS, Weight, Time string
	Bad                                               bool
}

// HTML writes a standalone report: one file with styles, scripts and
// screenshots inside, that opens in any browser without a server.
func HTML(w io.Writer, r Result) error {
	run := r.Run
	d := htmlData{
		Version: run.Version, Target: run.Target, Created: run.Started.Local().Format("2 Jan 2006, 15:04"),
		Compared: r.Compared(), Stats: r.stats(),
	}
	if Partial(r) {
		d.Partial = capitalize(partialNote) + "."
	}
	if len(r.Trend) > 1 {
		d.Trend = history.SVG(r.Trend)
		first, last := r.Trend[0], r.Trend[len(r.Trend)-1]
		d.TrendLabel = fmt.Sprintf("LCP %s to %s over the last %d runs", seconds(first), seconds(last), len(r.Trend))
	}
	if r.Compared() {
		c := r.Comparison
		d.Title = "Soundcheck · " + verdict(c)
		d.Verdict, d.Against = verdict(c), snapshotAt(c.Before)
		d.Tone = "ok"
		switch {
		case c.Count(model.Broke) > 0:
			d.Tone = model.Broke
		case c.Count(model.New) > 0:
			d.Tone = model.New
		case c.Count(model.Changed)+c.Count(model.Slower) > 0:
			d.Tone = model.Changed
		}
		groups := byClass(c.Changes)
		embedded := 0
		for _, class := range shownClasses(c) {
			info := classes[class]
			d.Chips = append(d.Chips, htmlChip{class, info.Label, info.Glyph, len(groups[class])})
			if len(groups[class]) == 0 {
				continue
			}
			g := htmlGroup{Key: class, Label: info.Label, Glyph: info.Glyph, Blurb: capitalize(info.Blurb)}
			for _, ch := range groups[class] {
				it := htmlItem{ID: ch.ID, Key: class, Label: info.Label, Title: ch.Title, Check: ch.Check, Where: where(ch.Pages),
					Pages: ch.Pages, BeforeAfter: beforeAfter(ch), Source: ch.Source, Detail: ch.Detail}
				if class != model.Fixed {
					it.Fix = ch.Fix
				}
				// Score is a percentage for visual and speed changes; for
				// issue changes it only ranks by page count.
				if ch.Score > 0 && (class == model.Changed || class == model.Slower) {
					it.Score = fmt.Sprintf("%.1f%%", ch.Score)
				}
				if len(ch.Files) > 0 {
					if embedded < maxShotSets {
						it.Shots = r.shots(ch.Files)
						embedded++
					}
					if it.Shots == nil {
						for _, k := range []string{"before", "after", "diff"} {
							if f := ch.Files[k]; f != "" {
								it.Files = append(it.Files, r.fileLabel(f))
							}
						}
					}
				}
				g.Items = append(g.Items, it)
			}
			d.Groups = append(d.Groups, g)
		}
		d.Notes = c.Warnings
	} else {
		issues := Issues(run)
		counts := Counts(issues)
		d.Verdict = capitalize(runVerdict(run, counts))
		d.Title = "Soundcheck · " + d.Verdict
		d.Tone = "ok"
		switch {
		case counts[model.Error] > 0:
			d.Tone = model.Broke
		case counts[model.Warning] > 0:
			d.Tone = model.New
		}
		for _, level := range Levels {
			label := strings.ToUpper(string(level)) + "S"
			d.Chips = append(d.Chips, htmlChip{string(level), label, levelGlyphs[level], counts[level]})
			g := htmlGroup{Key: string(level), Label: label, Glyph: levelGlyphs[level]}
			for _, i := range issues {
				if i.Level == level {
					g.Items = append(g.Items, htmlItem{Key: string(level), Label: strings.ToUpper(string(level)), Title: i.Title,
						Check: i.Check, Where: where(i.Pages), Pages: i.Pages, Source: i.Source, Detail: i.Detail, Fix: i.Fix})
				}
			}
			if len(g.Items) > 0 {
				d.Groups = append(d.Groups, g)
			}
		}
		d.Notes = run.Warnings
	}
	for _, p := range run.Pages {
		if p.Canceled() {
			d.Pages = append(d.Pages, htmlPage{Path: p.Path, URL: p.URL, Status: "not checked", Issues: "–",
				Time: "–", LCP: "–", CLS: "–", Weight: "–"})
			continue
		}
		hp := htmlPage{Path: p.Path, URL: p.URL, Status: fmt.Sprint(p.Status), Issues: fmt.Sprint(len(p.Issues)),
			Time: ms(p.Metrics.Load), LCP: ms(p.Metrics.LCP), CLS: fmt.Sprintf("%.2f", p.Metrics.CLS), Weight: bytesLabel(p.Metrics.Bytes)}
		if p.Error != "" {
			hp.Status, hp.Bad = "failed", true
		}
		for _, i := range p.Issues {
			if i.Level == model.Error {
				hp.Bad = true
			}
		}
		d.Pages = append(d.Pages, hp)
	}
	d.Echo = echoTable(run)
	// html/template escapes site text but keeps bidi overrides, which
	// make a name like "gnp.exe" read backwards. None of the report's own
	// text has one, so they are removed from the whole page.
	var buf bytes.Buffer
	if err := htmlTemplate.Execute(&buf, d); err != nil {
		return err
	}
	_, err := io.WriteString(w, noBidi(buf.String()))
	return err
}

// echoTable is one row per message, and one for each action that sent
// nothing. Bodies stay in Echo: its live view shows them in full.
func echoTable(run *model.Run) *htmlEcho {
	if !run.Options.Echo {
		return nil
	}
	e := &htmlEcho{Summary: echoSummary(run), URL: run.Options.EchoURL}
	for _, a := range run.EchoActions() {
		if len(a.Messages) == 0 {
			e.Rows = append(e.Rows, htmlMessage{Action: a.Label, Page: a.Page, None: true})
		}
		for _, m := range a.Messages {
			ch := string(m.Channel)
			if m.Status == echomodel.Failed || m.Status == echomodel.Blocked { // faked is normal when sealed
				ch += " (" + string(m.Status) + ")"
			}
			e.Rows = append(e.Rows, htmlMessage{Action: a.Label, Page: a.Page, Channel: ch,
				To: echolink.Recipient(m), Subject: cmp.Or(m.Subject, m.Event)})
		}
	}
	return e
}

// shots loads and shrinks a change's before, after and diff images. It
// returns nil when the before or after image is missing.
func (r Result) shots(files map[string]string) *htmlShots {
	load := func(key string) template.URL {
		f := files[key]
		if f == "" {
			return ""
		}
		path, err := r.Path(f)
		if err != nil {
			return ""
		}
		u, err := embedImage(path)
		if err != nil {
			return ""
		}
		return u
	}
	s := &htmlShots{Before: load("before"), After: load("after"), Diff: load("diff"),
		Phone: strings.Contains(files["after"], "-mobile")}
	if s.Before == "" || s.After == "" {
		return nil
	}
	return s
}

// fileLabel is where a screenshot is, for a report that doesn't embed
// it, or "image not available" when its path leaves the result's folder.
func (r Result) fileLabel(rel string) string {
	path, err := r.Path(rel)
	if err != nil {
		return "image not available"
	}
	return path
}

// embedImage returns a PNG file as a scaled-down JPEG data URL.
func embedImage(path string) (template.URL, error) {
	f, err := os.Open(path) //nolint:gosec // a screenshot from the run folder
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	src, _, err := image.Decode(f)
	if err != nil {
		return "", err
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > shotWidth {
		h = h * shotWidth / w
		w = shotWidth
	}
	crop := b
	if h > shotMaxHeight { // keep the top of very long pages
		crop.Max.Y = b.Min.Y + shotMaxHeight*b.Dx()/w
		h = shotMaxHeight
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, crop, draw.Src, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: shotQuality}); err != nil {
		return "", err
	}
	return template.URL("data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())), nil //nolint:gosec // our own encoded image
}

// where is one page's path, or how many pages.
func where(pages []string) string {
	if len(pages) == 1 {
		return pages[0]
	}
	return countLabel(pages)
}

func ms(v float64) string {
	if v <= 0 {
		return "–"
	}
	if v < 1000 {
		return fmt.Sprintf("%.0f ms", v)
	}
	return fmt.Sprintf("%.1f s", (time.Duration(v) * time.Millisecond).Seconds())
}

func bytesLabel(n int64) string {
	switch {
	case n <= 0:
		return "–"
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.0f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/1024/1024)
}

// seconds writes a millisecond LCP as "1.4 s".
func seconds(ms float64) string { return fmt.Sprintf("%.1f s", ms/1000) }
