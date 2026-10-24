// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wpupdate

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/cache"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

// LogDir is where the maintenance logs go, inside the Soundcheck folder.
const LogDir = "wp-update"

// writeLogs writes the log as text and Markdown, named after the start.
func (r *runner) writeLogs() error {
	base := filepath.Join(r.o.Dir, config.Dir, LogDir, store.Stamp(r.log.Started))
	for _, f := range []struct {
		ext   string
		write func(io.Writer) error
	}{{".txt", r.log.Text}, {".md", r.log.Markdown}} {
		var buf bytes.Buffer
		if err := f.write(&buf); err != nil {
			return err
		}
		if err := cache.WriteFile(base+f.ext, buf.Bytes()); err != nil {
			return err
		}
		r.log.Files = append(r.log.Files, base+f.ext)
	}
	return nil
}

// effect says what a bad update did, by its verdict.
func effect(verdict string) string {
	switch verdict {
	case model.Broke:
		return "broke the site"
	case model.New:
		return "brought new problems"
	}
	return "changed the site"
}

// count is "1 plugin" or "3 plugins".
func count(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func (p Plugin) versions() string { return p.Old + " → " + p.New }

// Summary is the log's opening lines: what was updated, what broke and
// what became of the vulnerable plugins.
//
//	Updated 9 plugins, 1 rolled back: contact-form 4.2.0 → 4.2.1 broke the site (Server error 500 on /contact/, plugins/contact-form/includes/submit.php:412).
//	2 vulnerable plugins fixed, site still works.
func (l *Log) Summary() []string {
	var updated, rolled, kept, failed, vulnFixed []Plugin
	var stillVuln []string
	for _, p := range l.Plugins {
		switch p.Status {
		case Updated:
			updated = append(updated, p)
		case RolledBack:
			rolled = append(rolled, p)
		case Kept:
			kept = append(kept, p)
		case Failed:
			failed = append(failed, p)
		}
		switch {
		case p.Vulnerable == "":
		case p.Status == Updated || p.Status == Kept:
			vulnFixed = append(vulnFixed, p)
		default:
			stillVuln = append(stillVuln, p.Slug)
		}
	}
	if len(updated)+len(rolled)+len(kept)+len(failed) == 0 && len(l.Plugins) == 0 {
		return []string{"No plugin updates available."}
	}
	var out []string
	line := "Updated " + count(len(updated)+len(kept), "plugin")
	if len(rolled) > 0 {
		var why []string
		for _, p := range rolled {
			why = append(why, fmt.Sprintf("%s %s %s (%s)", p.Slug, p.versions(), effect(p.Verdict), p.Evidence))
		}
		line += fmt.Sprintf(", %d rolled back: %s", len(rolled), strings.Join(why, "; "))
	}
	out = append(out, line+".")
	for _, p := range kept {
		out = append(out, fmt.Sprintf("%s %s %s (%s) and is still installed. Roll it back with: wp plugin install %s --version=%s --force",
			p.Slug, p.versions(), effect(p.Verdict), p.Evidence, p.Slug, p.Old))
	}
	if len(failed) > 0 {
		out = append(out, count(len(failed), "update")+" failed: "+slugs(failed)+".")
	}
	if n := len(vulnFixed); n > 0 {
		s := count(n, "vulnerable plugin") + " fixed"
		if l.Works {
			s += ", site still works"
		}
		out = append(out, s+".")
	}
	if len(stillVuln) > 0 {
		out = append(out, "Still vulnerable: "+strings.Join(stillVuln, ", ")+".")
	}
	switch {
	case l.Note != "":
		out = append(out, "Stopped: "+l.Note+".")
	case l.Works && len(vulnFixed) == 0 && len(updated) > 0:
		out = append(out, "The site still works.")
	}
	return out
}

func slugs(ps []Plugin) string {
	s := make([]string, len(ps))
	for i, p := range ps {
		s[i] = p.Slug
	}
	return strings.Join(s, ", ")
}

// result is a plugin's line in the log's table.
func (p Plugin) result() string {
	switch p.Status {
	case Updated:
		s := Updated
		if p.Vulnerable != "" {
			s += " (security fix)"
		}
		return s
	case RolledBack:
		return "rolled back: " + effect(p.Verdict) + ", " + p.Evidence
	case Kept:
		return "still installed: " + effect(p.Verdict) + ", " + p.Evidence
	case Failed:
		return "update failed: " + p.Error
	}
	if p.Error != "" {
		return p.Status + ": " + p.Error
	}
	return p.Status
}

// header is the line under the title: when, how and against what.
func (l *Log) header() string {
	parts := []string{l.Started.Format("2 Jan 2006 15:04"), strings.ReplaceAll(l.Mode, "-", " "), "stop on " + l.StopOn}
	if l.Snapshot != "" {
		parts = append(parts, "snapshot "+l.Snapshot)
	}
	return strings.Join(parts, " · ")
}

// footer is "12 compares in 3m20s."
func (l *Log) footer() string {
	return fmt.Sprintf("%s in %s.", count(l.Compares, "compare"), l.Finished.Sub(l.Started).Round(time.Second))
}

// Text writes the log as plain text.
func (l *Log) Text(w io.Writer) error {
	var b bytes.Buffer
	fmt.Fprintf(&b, "WordPress updates for %s\n%s\n\n", l.Target, l.header())
	for _, s := range l.Summary() {
		b.WriteString(s + "\n")
	}
	if len(l.Plugins) > 0 {
		b.WriteString("\n")
		tw := tabwriter.NewWriter(&b, 0, 0, 3, ' ', 0)
		fmt.Fprintln(tw, "Plugin\tVersion\tResult")
		for _, p := range l.Plugins {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", p.Slug, p.versions(), p.result())
		}
		_ = tw.Flush()
	}
	if len(l.Missing) > 0 {
		fmt.Fprintf(&b, "\nNo update available for %s.\n", strings.Join(l.Missing, ", "))
	}
	fmt.Fprintf(&b, "\n%s\n", l.footer())
	_, err := w.Write(b.Bytes())
	return err
}

// Markdown writes the log as Markdown, for a ticket or a client email.
func (l *Log) Markdown(w io.Writer) error {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# WordPress updates for %s\n\n%s\n\n", mdText(l.Target), mdText(l.header()))
	for _, s := range l.Summary() {
		b.WriteString(mdText(s) + "\n\n")
	}
	if len(l.Plugins) > 0 {
		b.WriteString("| Plugin | Version | Result |\n|---|---|---|\n")
		for _, p := range l.Plugins {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", mdCell(p.Slug), mdCell(p.versions()), mdCell(p.result()))
		}
		b.WriteString("\n")
	}
	if len(l.Missing) > 0 {
		fmt.Fprintf(&b, "No update available for %s.\n\n", mdText(strings.Join(l.Missing, ", ")))
	}
	b.WriteString(mdText(l.footer()) + "\n")
	_, err := w.Write(b.Bytes())
	return err
}

// mdCell makes text safe inside a Markdown table cell.
func mdCell(s string) string {
	return strings.ReplaceAll(mdText(s), "|", `\|`)
}

// mdText keeps text from plugins and pages from turning into Markdown
// or HTML: plugin names and page titles come from the site.
func mdText(s string) string {
	r := strings.NewReplacer("\n", " ", "\r", "", "<", "&lt;", ">", "&gt;", "`", "\\`", "*", `\*`, "_", `\_`, "[", `\[`, "]", `\]`)
	return r.Replace(s)
}
