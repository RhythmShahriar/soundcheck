// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package keyflows runs key flows: flows with keyboard: true or
// keyboard: send, walked by keyboard only (Tab, Enter, Space and the
// arrow keys), never by mouse. The keyboard module calls Run from its
// site check at --level flows. A step that can't be reached or operated
// is a keyboard/flow issue and form errors that aren't tied to their
// fields are keyboard/error-not-linked.
//
// keyboard: true runs dry: what the flow would send is held, so a
// checkout is proved operable without placing an order. keyboard: send
// sends for real only to a loopback or private address, or with
// --staging; on a public address it runs dry with a note. The flow
// runner's production refusal, off-site refusal and environment
// allowlist apply to key flows as to any flow.
package keyflows

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	echomodel "github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/keyboard/ids"
	"github.com/RhythmShahriar/soundcheck/echolink"
	"github.com/RhythmShahriar/soundcheck/flow"
	"github.com/RhythmShahriar/soundcheck/model"
)

// The reasons a key flow is listed under KeyFlows.Skipped.
const (
	SkipLevel   = "needs --level flows"
	SkipLogin   = "needs a login: set SOUNDCHECK_USER and SOUNDCHECK_PASSWORD"
	SkipBrowser = "no browser session"
	SkipStopped = "the run was stopped"
)

// player plays one key flow with o and returns what became of it, or a
// reason it didn't run.
type player func(ctx context.Context, cc *check.Context, f flow.Flow, o flow.Options) (res flow.Result, skip string)

// play is the player Run uses; tests swap in a fake.
var play player = playInSession

// Run runs the run's key flows with the keyboard limits o and records
// what became of them in cc.Run.KeyFlows, under cc.Lock. Flows run one
// after another, each in its own private session. A run without key
// flows keeps KeyFlows nil.
func Run(ctx context.Context, cc *check.Context, o model.KeyboardOptions) {
	if cc.Config.Flows == "" {
		return
	}
	// A flow file that can't be read is interact's to report
	// (interact/flow-file), so it isn't reported twice. When interact
	// won't play flows on this run, nobody else says so: a broken key
	// flow would vanish without a word, so it gets a note here.
	all, err := flow.Load(cc.Config.Flows)
	if err != nil && (!cc.Config.Has("interact") || !cc.Config.Level("flows")) {
		noteLoadError(cc, err)
	}
	var keyed []flow.Flow
	for _, f := range all {
		if f.Keyboard != "" {
			keyed = append(keyed, f)
		}
	}
	if len(keyed) == 0 {
		return
	}
	kf := &model.KeyFlows{}
	defer func() {
		cc.Lock()
		cc.Run.KeyFlows = kf
		cc.Unlock()
	}()
	skipAll := func(fs []flow.Flow, reason string) {
		for _, f := range fs {
			kf.Skipped = append(kf.Skipped, model.SkippedFlow{File: filepath.Base(f.File), Reason: reason})
		}
	}
	if !cc.Config.Level("flows") {
		skipAll(keyed, SkipLevel)
		if len(keyed) == 1 {
			cc.Warn("keyboard: 1 key flow needs --level flows (it types test data); not run")
		} else {
			cc.Warn(fmt.Sprintf("keyboard: %d key flows need --level flows (they type test data); not run", len(keyed)))
		}
		return
	}
	if cc.NewSession == nil {
		skipAll(keyed, SkipBrowser)
		cc.Warn("keyboard: key flows: no browser session available")
		return
	}
	for i, f := range keyed {
		if ctx.Err() != nil {
			skipAll(keyed[i:], SkipStopped)
			return
		}
		res, skip := play(ctx, cc, f, options(ctx, cc, f, o))
		if skip != "" {
			kf.Skipped = append(kf.Skipped, model.SkippedFlow{File: filepath.Base(f.File), Reason: skip})
			cc.Warn(fmt.Sprintf("keyboard: %s not run: %s", filepath.Base(f.File), skip))
			continue
		}
		kf.Run++
		report(cc, f, res)
	}
}

// noteLoadError notes each flow file in err that can't be read, or the
// flows folder when err isn't about one file. A broken file may hold a
// key flow; which one can't be told, so it's a note and not a Skipped
// entry.
func noteLoadError(cc *check.Context, err error) {
	var files []*flow.FileError
	var walk func(error)
	walk = func(e error) {
		if j, ok := e.(interface{ Unwrap() []error }); ok { //nolint:errorlint // walking a joined error
			for _, e := range j.Unwrap() {
				walk(e)
			}
			return
		}
		var fe *flow.FileError
		if errors.As(e, &fe) {
			files = append(files, fe)
		}
	}
	walk(err)
	if len(files) == 0 {
		cc.Warn(fmt.Sprintf("keyboard: flows folder %s can't be read (%v); key flows not run", cc.Config.Flows, err))
		return
	}
	for _, fe := range files {
		cc.Warn(fmt.Sprintf("keyboard: flow file %s can't be read (%v); a key flow in it is not run", filepath.Base(fe.File), fe.Err))
	}
}

// options are the flow options of key flow f: the run's, as interact
// gives a flow (--allow-env, --allow-host, --insecure-login), with the
// keyboard walk's max_tabs. Dry is set for keyboard: true. It is set for
// keyboard: send on a public address without --staging, with a note.
// The one exception is a flow that sets production: true and starts on a
// URL listed under production: its owner said it is safe on the live
// site. Without production: true a flow that starts on a production URL
// runs dry too: the flow runner refuses its steps only on production
// pages, and production entries can name one path (example.com/shop),
// so a later step on another page would otherwise send for real.
func options(ctx context.Context, cc *check.Context, f flow.Flow, ko model.KeyboardOptions) flow.Options {
	t := cc.Config.Trust
	o := flow.Options{Base: cc.Base, Timeout: time.Duration(cc.Config.Timeout) * time.Second, Save: cc.Save,
		Fake: flow.Fake{Seed: cc.Run.Started.String()}, Production: cc.Config.IsProduction,
		AllowEnv: t.AllowEnv, AllowHosts: t.AllowHosts, PlainHTTP: t.InsecureLogin,
		MaxTabs: ko.MaxTabs, Dry: f.Keyboard == flow.KeyboardDry}
	liveOK := f.Production && cc.Config.IsProduction(startURL(cc, f)) // the owner said it's safe on the live site
	if f.Keyboard == flow.KeyboardSend && public(ctx, cc) && !t.Staging && !liveOK {
		o.Dry = true
		cc.Warn(fmt.Sprintf("keyboard: send runs only on a loopback or private address, or with --staging; %s ran dry", filepath.Base(f.File)))
	}
	return o
}

// public says whether the site may be on a public address. The runner's
// TargetPublic is a plain bool, so a run that didn't classify the target
// looks private; checking the base host here as well keeps keyboard:
// send from submitting to a public site when that happens. A host that
// can't be resolved counts as public.
func public(ctx context.Context, cc *check.Context) bool {
	if cc.TargetPublic {
		return true
	}
	if cc.Base == nil {
		return true
	}
	return !localHost(ctx, cc.Base.Hostname())
}

// localHost says whether host is localhost or resolves only to loopback,
// private or link-local addresses. It looks the name up with the local
// resolver; the site itself isn't contacted.
func localHost(ctx context.Context, host string) bool {
	if host == "" {
		return false
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return localIP(ip)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addrs) == 0 {
		return false
	}
	for _, a := range addrs {
		if !localIP(a.IP) {
			return false
		}
	}
	return true
}

func localIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

// startURL is f's start page as a URL on the site.
func startURL(cc *check.Context, f flow.Flow) string {
	u, err := url.Parse(startPath(f))
	if err != nil || cc.Base == nil {
		return startPath(f)
	}
	return cc.Base.ResolveReference(u).String()
}

func startPath(f flow.Flow) string {
	if f.Start == "" {
		return "/"
	}
	return f.Start
}

// pagePath is the path of f's start page, as the run's pages have it.
func pagePath(f flow.Flow) string {
	if u, err := url.Parse(startPath(f)); err == nil && (u.Host != "" || u.Path != "") {
		if u.Path == "" {
			return "/"
		}
		return u.Path
	}
	return startPath(f)
}

// playInSession plays f in a private session of its own, logged in when
// the flow asks for it, as interact plays a flow.
func playInSession(ctx context.Context, cc *check.Context, f flow.Flow, o flow.Options) (flow.Result, string) {
	sess, err := cc.NewSession()
	if err != nil {
		return flow.Result{}, err.Error()
	}
	defer func() { _ = sess.Close() }()
	if f.Login {
		if cc.Login == nil {
			return flow.Result{}, SkipLogin
		}
		if err := cc.Login(ctx, sess); err != nil {
			return flow.Result{}, "logging in failed: " + err.Error()
		}
	}
	page, err := browser.NewTab(sess)
	if err != nil {
		return flow.Result{}, err.Error()
	}
	defer func() { _ = page.Close() }()
	release, err := check.GuardTab(ctx, page)
	if err != nil {
		return flow.Result{}, err.Error()
	}
	defer release()
	if cc.Echo != nil {
		// Tagged as interact tags a flow, so expect: {message: …} works in
		// a key flow too.
		action := echolink.FlowAction(f.Name)
		untag, err := cc.Echo.Tag(ctx, page, cc.Base.String(), action)
		if err != nil {
			cc.Warn(fmt.Sprintf("keyboard: flow %q: echo: couldn't tag its requests: %v", f.Name, err))
		}
		defer untag()
		o.Messages = func(ctx context.Context) ([]echomodel.Message, error) { return cc.Echo.Messages(ctx, action) }
	}
	res := flow.Play(ctx, page, f, o)
	if len(res.Trace) > 0 && cc.Save != nil {
		// The trace names targets and keys; typed values show only as
		// "<typed N characters>".
		_ = cc.Save("keyflows/"+flow.Slug(f.Name)+".txt", []byte(strings.Join(res.Trace, "\n")+"\n"))
	}
	return res, ""
}

// report turns what became of key flow f into issues on its start page
// and notes.
func report(cc *check.Context, f flow.Flow, res flow.Result) {
	file := filepath.Base(f.File)
	start := startPath(f)
	if h := res.Held; h != nil {
		note := fmt.Sprintf("keyboard: %s stopped at step %d (would have sent %s %s)", file, h.Step, h.Method, h.Path)
		switch last := len(f.Steps); {
		case h.Step+1 == last:
			note += fmt.Sprintf("; step %d not run", last)
		case h.Step < last:
			note += fmt.Sprintf("; steps %d-%d not run", h.Step+1, last)
		}
		cc.Warn(note)
	}
	ver := cc.Run.Options.WCAG
	for _, fl := range res.Failures {
		var i model.Issue
		switch {
		case fl.Refused:
			// The same issue interact raises for a refused flow step.
			i = model.Issue{Check: "interact/flow-production", Level: model.Warning,
				Title:  fmt.Sprintf("Flow %q step %d refused on production: %s", f.Name, fl.Step, fl.Desc),
				Detail: fl.Err.Error(), Source: f.File, Pages: []string{start},
				Fix: "Run flows against staging, or add production: true to " + f.File + " if this flow is safe on the live site."}
		case fl.Key != nil:
			info, _ := ids.Lookup(ids.Flow)
			i = model.Issue{Check: ids.Flow, Level: info.Level, Title: info.Title,
				WCAG: info.WCAG(ver), EN301549: info.Clauses(ver), Impact: info.Impact,
				Detail: fmt.Sprintf("%s: step %d %s %q: %s", f.Name, fl.Step, fl.Key.Kind, fl.Key.Target, fl.Key.Reason),
				Source: file + ": " + fl.Key.Kind + " " + fl.Key.Target, FocusPath: fl.Key.Path, Pages: []string{start},
				Fix: "Make the control a native button, link or field, or give it tabindex=\"0\" and handle Enter and Space."}
		default:
			// The flow itself broke (a step not found, an expect that
			// failed): reported as interact reports a broken flow.
			title := fmt.Sprintf("Flow %q step %d failed: %s", f.Name, fl.Step, fl.Desc)
			if fl.Step == 0 {
				title = fmt.Sprintf("Flow %q couldn't open %s", f.Name, start)
			}
			i = model.Issue{Check: "interact/flow", Level: model.Error, Title: title, Detail: fl.Err.Error(),
				Source: f.File, Pages: []string{start},
				Fix: "Run the flow by hand from " + start + "; if the page changed on purpose, update " + f.File + "."}
		}
		addIssue(cc, pagePath(f), i)
	}
	for _, u := range res.Unlinked {
		addIssue(cc, pagePath(f), unlinked(f, u, ver))
	}
}

// unlinked is the keyboard/error-not-linked issue of form errors key
// flow f ran into.
func unlinked(f flow.Flow, u flow.Unlinked, ver string) model.Issue {
	info, _ := ids.Lookup(ids.ErrorNotLinked)
	shown := fmt.Sprintf("%d fields show errors", u.Fields)
	if u.Fields == 1 {
		shown = "1 field shows errors"
	}
	return model.Issue{Check: ids.ErrorNotLinked, Level: info.Level, Title: info.Title,
		WCAG: info.WCAG(ver), EN301549: info.Clauses(ver), Impact: info.Impact,
		Detail: fmt.Sprintf("After %s step %d, %s but focus stayed on %s and the errors aren't linked with aria-describedby or aria-errormessage.",
			f.Name, u.Step, shown, u.Focus),
		Source: u.Form, Pages: []string{startPath(f)},
		Fix: "Move focus to the first field with an error or to an error summary, or point each field at its message with aria-describedby or aria-errormessage."}
}

// addIssue adds i to the page at path, or to the run's site issues when
// the run didn't visit that page.
func addIssue(cc *check.Context, path string, i model.Issue) {
	i.Identify()
	cc.Lock()
	defer cc.Unlock()
	if p := cc.Run.Page(path); p != nil {
		i.Pages = nil // the page says where
		p.Issues = append(p.Issues, i)
		return
	}
	cc.Run.Issues = append(cc.Run.Issues, i)
}
