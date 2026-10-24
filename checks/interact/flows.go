// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package interact

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	echomodel "github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/echolink"
	"github.com/RhythmShahriar/soundcheck/flow"
	"github.com/RhythmShahriar/soundcheck/model"
)

// CheckSite runs level 4: every flow in the flows folder, each in its own
// private browser session. A failed step becomes an issue naming the flow
// and the step.
func (m *Module) CheckSite(ctx context.Context, cc *check.Context) {
	if !cc.Config.Level("flows") || cc.Config.Flows == "" {
		return
	}
	flows, err := flow.Load(cc.Config.Flows)
	// A broken flow file is an error in the results, not a footnote: the
	// journey it describes isn't being checked. The other flows still run.
	var bad []*flow.FileError
	collectFileErrors(err, &bad)
	for _, fe := range bad {
		cc.AddIssue(model.Issue{Check: "interact/flow-file", Level: model.Error,
			Title:  fmt.Sprintf("Flow file %q can't be read", filepath.Base(fe.File)),
			Detail: fe.Err.Error(), Source: fe.File,
			Fix: "Fix the file; `rhythm soundcheck flows init` writes an example of the format."})
	}
	if err != nil && len(bad) == 0 {
		cc.AddIssue(model.Issue{Check: "interact/flow-file", Level: model.Error,
			Title: "Flows folder can't be read", Detail: err.Error(), Source: cc.Config.Flows})
		return
	}
	if len(flows) == 0 && len(bad) > 0 {
		return
	}
	if len(flows) == 0 {
		cc.Warn("interactions.levels includes flows, but " + cc.Config.Flows + " has none (run `flows init` for an example)")
		return
	}
	if cc.NewSession == nil {
		cc.Warn("flows: no browser session available")
		return
	}
	for _, f := range flows {
		if ctx.Err() != nil {
			return
		}
		m.runFlow(ctx, cc, f)
	}
}

func (m *Module) runFlow(ctx context.Context, cc *check.Context, f flow.Flow) {
	sess, err := cc.NewSession()
	if err != nil {
		cc.Warn(fmt.Sprintf("flow %q: %v", f.Name, err))
		return
	}
	defer func() { _ = sess.Close() }()
	if f.Login {
		if cc.Login == nil {
			cc.Warn(fmt.Sprintf("flow %q needs a login: set SOUNDCHECK_USER and SOUNDCHECK_PASSWORD", f.Name))
			return
		}
		if err := cc.Login(ctx, sess); err != nil {
			cc.Warn(fmt.Sprintf("flow %q: logging in failed: %v", f.Name, err))
			return
		}
	}
	page, err := browser.NewTab(sess)
	if err != nil {
		cc.Warn(fmt.Sprintf("flow %q: %v", f.Name, err))
		return
	}
	defer func() { _ = page.Close() }()
	timeout := time.Duration(cc.Config.Timeout) * time.Second
	opts := flowOptions(cc, timeout)
	start := orDefault(f.Start, "/")
	act := model.EchoAction{Action: echolink.FlowAction(f.Name), Label: f.Name, Kind: "flow", Page: pathOnly(start)}
	if cc.Echo != nil {
		// The flow has its own session, so its tag (header and cookie)
		// stays on for the whole flow without affecting other tabs.
		untag, err := cc.Echo.Tag(ctx, page, cc.Base.String(), act.Action)
		if err != nil {
			cc.Warn(fmt.Sprintf("flow %q: echo: couldn't tag its requests: %v", f.Name, err))
		}
		defer untag()
		opts.Messages = func(ctx context.Context) ([]echomodel.Message, error) { return cc.Echo.Messages(ctx, act.Action) }
	}
	fails := flow.Run(ctx, page, f, opts)
	if cc.Echo != nil && ctx.Err() == nil {
		m.flowMessages(ctx, cc, act)
	}
	for _, fl := range fails {
		if fl.Refused {
			m.warnOnce(cc, "flow-production", "Flow clicks and submits skipped on production URLs: set production: true in a flow to allow them")
			cc.AddIssue(model.Issue{Check: "interact/flow-production", Level: model.Warning,
				Title:  fmt.Sprintf("Flow %q step %d refused on production: %s", f.Name, fl.Step, fl.Desc),
				Detail: fl.Err.Error(), Source: f.File, Pages: []string{start},
				Fix: "Run flows against staging, or add production: true to " + f.File + " if this flow is safe on the live site."})
			continue
		}
		title := fmt.Sprintf("Flow %q step %d failed: %s", f.Name, fl.Step, fl.Desc)
		if fl.Step == 0 {
			title = fmt.Sprintf("Flow %q couldn't open %s", f.Name, start)
		}
		cc.AddIssue(model.Issue{Check: "interact/flow", Level: model.Error, Title: title, Detail: fl.Err.Error(),
			Source: f.File, Pages: []string{start},
			Fix: "Run the flow by hand from " + start + "; if the page changed on purpose, update " + f.File + "."})
	}
}

// flowMessages records what a flow made the site send, and the problems
// Echo finds in it, as site issues on the flow's start page. A flow that
// sends nothing isn't reported: an expect: {message: …} step says when it
// should.
func (m *Module) flowMessages(ctx context.Context, cc *check.Context, act model.EchoAction) {
	msgs, err := cc.Echo.Wait(ctx, act.Action)
	if err != nil {
		m.warnOnce(cc, "echo-wait", "echo: couldn't get the messages flows sent: "+err.Error())
		return
	}
	// Stored and judged masked: reset links and one-time codes mustn't
	// reach the run's files.
	act.Messages = echolink.Redact(msgs)
	cc.Lock()
	cc.Run.Echo = append(cc.Run.Echo, act)
	cc.Unlock()
	for _, i := range echolink.Issues(act, cc.Base.Hostname()) {
		i.Pages = []string{act.Page}
		cc.AddIssue(i)
	}
}

// pathOnly turns a flow's start (a path or a URL) into a path.
func pathOnly(start string) string {
	if u, err := url.Parse(start); err == nil && u.Host != "" {
		return orDefault(u.RequestURI(), "/")
	}
	return start
}

// collectFileErrors finds every *flow.FileError in err (a joined error).
func collectFileErrors(err error, out *[]*flow.FileError) {
	if err == nil {
		return
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok { //nolint:errorlint // walking a joined error
		for _, e := range j.Unwrap() {
			collectFileErrors(e, out)
		}
		return
	}
	var fe *flow.FileError
	if errors.As(err, &fe) {
		*out = append(*out, fe)
	}
}

// flowOptions are the options a flow of this run gets, with what the
// command line allowed: --allow-env, --allow-host and --insecure-login.
func flowOptions(cc *check.Context, timeout time.Duration) flow.Options {
	t := cc.Config.Trust
	return flow.Options{Base: cc.Base, Timeout: timeout, Save: cc.Save,
		Fake: flow.Fake{Seed: cc.Run.Started.String()}, Production: cc.Config.IsProduction,
		AllowEnv: t.AllowEnv, AllowHosts: t.AllowHosts, PlainHTTP: t.InsecureLogin}
}
