package interact

import (
	"context"
	"fmt"
	"time"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/check"
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
	if err != nil {
		cc.Warn("flows: " + err.Error())
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
	page, err := sess.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		cc.Warn(fmt.Sprintf("flow %q: %v", f.Name, err))
		return
	}
	defer func() { _ = page.Close() }()
	timeout := time.Duration(cc.Config.Timeout) * time.Second
	fails := flow.Run(ctx, page, f, flow.Options{Base: cc.Base, Timeout: timeout, Save: cc.Save,
		Fake: flow.Fake{Seed: cc.Run.Started.String()}})
	start := orDefault(f.Start, "/")
	for _, fl := range fails {
		title := fmt.Sprintf("Flow %q step %d failed: %s", f.Name, fl.Step, fl.Desc)
		if fl.Step == 0 {
			title = fmt.Sprintf("Flow %q couldn't open %s", f.Name, start)
		}
		cc.AddIssue(model.Issue{Check: "interact/flow", Level: model.Error, Title: title, Detail: fl.Err.Error(),
			Source: f.File, Pages: []string{start},
			Fix: "Run the flow by hand from " + start + "; if the page changed on purpose, update " + f.File + "."})
	}
}
