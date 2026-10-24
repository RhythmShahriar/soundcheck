// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package echolink ties a Soundcheck run to Echo (`rhythm echo up`), which
// captures every email, SMS and webhook a site sends. While Soundcheck
// submits a form or plays a flow, the requests it makes to the site carry
// X-Echo-Run: <run>/<action>; the site's Echo connector copies that into
// each capture, so afterwards Soundcheck can ask Echo exactly which
// messages the submit caused, and compare them with the snapshot's.
package echolink

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/client"
	echomodel "github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
	"github.com/RhythmShahriar/soundcheck/capture/intercept"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Link is a connection to a running Echo.
type Link struct {
	Client  *client.Client
	URL     string
	Mode    string // seal or observe, as Echo reported it
	Run     string // this run's ID (see ForRun)
	Quiet   time.Duration
	Timeout time.Duration
	mu      *sync.Mutex
}

// Connect checks that Echo answers at cfg.URL and returns a link to it.
func Connect(ctx context.Context, cfg config.Echo, token string) (*Link, error) {
	c := client.New(cfg.URL, token)
	hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	h, err := c.Health(hctx)
	if err != nil {
		if errors.Is(err, client.ErrNotRunning) || hctx.Err() != nil {
			return nil, fmt.Errorf("--echo: Echo isn't running at %s (start it with: rhythm echo up)", c.URL)
		}
		return nil, fmt.Errorf("--echo: %w", err)
	}
	return &Link{Client: c, URL: c.URL, Mode: h.Mode, Quiet: cfg.Quiet, Timeout: cfg.Timeout, mu: &sync.Mutex{}}, nil
}

// ForRun returns a copy of l for the run that started at t. Two runs in
// one command (--against) get different IDs, so their messages never mix.
func (l *Link) ForRun(t time.Time) *Link {
	c := *l
	c.Run = RunID(t)
	if c.mu == nil {
		c.mu = &sync.Mutex{}
	}
	return &c
}

// RunID is the run's start stamp, to the millisecond.
func RunID(t time.Time) string { return t.UTC().Format("20060102T150405.000Z") }

// FormAction is the action ID of a form: the page's path and the form's
// stable selector, the same in every run.
func FormAction(path, selector string) string { return safe("form:" + path + "#" + selector) }

// FlowAction is the action ID of a flow.
func FlowAction(name string) string { return safe("flow:" + name) }

// safe keeps an action ID to characters that survive a header, a cookie
// and the URL decoding connectors apply (PHP turns + into a space and
// decodes %XX), and to a length connectors accept.
func safe(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', strings.ContainsRune("-._~:/#[]=>*()!@", r):
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	return out[:min(len(out), 200)]
}

// Turn waits until no other tab has a tagged submit going, and returns
// the function that ends this one's turn. Tagged submits take turns
// because the tag's cookie is shared by every tab in the browser session.
func (l *Link) Turn() (done func()) {
	l.mu.Lock()
	return l.mu.Unlock
}

// Wait returns the messages action caused, once none arrived for Quiet
// (at most Timeout).
func (l *Link) Wait(ctx context.Context, action string) ([]echomodel.Message, error) {
	return l.Client.Wait(ctx, client.Filter{Run: l.Run, Action: action}, 0, l.Quiet, l.Timeout)
}

// Messages returns the messages action caused so far.
func (l *Link) Messages(ctx context.Context, action string) ([]echomodel.Message, error) {
	return l.Client.Messages(ctx, client.Filter{Run: l.Run, Action: action})
}

// Tag makes tab's requests to site's origin carry X-Echo-Run for action,
// as a header and as a cookie, until the returned function is called.
//
// The header is added by pausing each request (see package intercept)
// and tagging only those to the site's own origin, not with
// Network.setExtraHTTPHeaders: an extra header goes to every host. Chrome
// then preflights cross-origin calls (reCAPTCHA, payment and CRM
// scripts), which fail when those services don't allow it. The
// interceptor is shared, so the run's own -H headers are still added.
// The cookie covers handlers that read cookies but not headers.
func (l *Link) Tag(_ context.Context, tab *rod.Page, site, action string) (func(), error) {
	u, err := url.Parse(site)
	if err != nil || u.Host == "" {
		return func() {}, fmt.Errorf("can't tag requests to %q", site)
	}
	origin := u.Scheme + "://" + u.Host
	value := client.RunValue(l.Run, action)
	if _, err := (proto.NetworkSetCookie{Name: wire.RunHeader, Value: value, URL: origin + "/", Path: "/"}).Call(tab); err != nil {
		return func() {}, err
	}
	untagCookie := func() { _ = proto.NetworkDeleteCookies{Name: wire.RunHeader, URL: origin + "/"}.Call(tab) }
	untag, err := intercept.Add(tab, func(q *intercept.Request) {
		if q.URL != nil && strings.EqualFold(q.URL.Scheme+"://"+q.URL.Host, origin) {
			q.SetHeader(wire.RunHeader, value)
		}
	})
	if err != nil {
		untagCookie()
		return func() {}, err
	}
	return func() {
		untag()
		untagCookie()
	}, nil
}

// Summary says what an action sent: "sent 2 emails, 1 webhook".
func Summary(msgs []echomodel.Message) string {
	if len(msgs) == 0 {
		return "no message went out"
	}
	n := map[echomodel.Channel]int{}
	for _, m := range msgs {
		n[m.Channel]++
	}
	var parts []string
	for _, c := range echomodel.Channels {
		if n[c] > 0 {
			parts = append(parts, ChannelCount(c, n[c]))
		}
	}
	return "sent " + strings.Join(parts, ", ")
}

// ChannelCount is "1 email", "3 emails", "2 SMS".
func ChannelCount(c echomodel.Channel, n int) string {
	nouns := map[echomodel.Channel][2]string{
		echomodel.Email: {"email", "emails"}, echomodel.SMS: {"SMS", "SMS"},
		echomodel.WhatsApp: {"WhatsApp message", "WhatsApp messages"}, echomodel.Chat: {"chat message", "chat messages"},
		echomodel.Webhook: {"webhook", "webhooks"}, echomodel.Analytics: {"analytics event", "analytics events"},
		echomodel.Payment: {"payment call", "payment calls"}, echomodel.Push: {"push notification", "push notifications"},
		echomodel.HTTP: {"other request", "other requests"},
	}
	w, ok := nouns[c]
	if !ok {
		w = [2]string{string(c), string(c)}
	}
	if n == 1 {
		return "1 " + w[0]
	}
	return fmt.Sprintf("%d %s", n, w[1])
}

// Recipient is who a message went to, or where.
func Recipient(m echomodel.Message) string {
	if len(m.To) > 0 {
		return strings.Join(m.To, ", ")
	}
	if u, err := url.Parse(m.URL); err == nil && u.Host != "" {
		return u.Host + u.Path
	}
	return m.Provider
}

// Issues are the problems Echo finds in an action's messages (unfilled
// placeholders, PHP errors, failed sends…), as Soundcheck issues named
// echo/…. Source is the action's label, so the same problem from two
// forms on one page is two issues. One message per issue is enough.
func Issues(a model.EchoAction, siteHost string) []model.Issue {
	var out []model.Issue
	seen := map[string]bool{}
	for _, m := range a.Messages {
		for _, i := range check.Message(m, check.Options{SiteHost: siteHost}) {
			is := model.Issue{Check: "echo/" + i.Check, Level: model.Level(i.Level), Title: i.Title, Detail: i.Detail,
				Source: a.Label, Fix: i.Fix}
			is.Identify()
			if !seen[is.Key] {
				seen[is.Key] = true
				out = append(out, is)
			}
		}
	}
	return out
}

// NoMessage is the notice for a submit that showed success while Echo saw
// nothing go out. It's only a notice: many forms rightly send nothing
// (a search, a filter, a poll). Losing a message the snapshot had is what
// the echo module's comparison reports as BROKE.
func NoMessage(a model.EchoAction, wait time.Duration) model.Issue {
	i := model.Issue{Check: "echo/no-message", Level: model.Notice, Title: "Form says sent, but no message went out",
		Detail: fmt.Sprintf("%s showed success and Echo saw no email, SMS or webhook from it within %s", a.Label, wait),
		Source: a.Label,
		Fix: "If the form should notify someone, check its notification settings and the site's mailer; " +
			"check the site has Echo's connector (rhythm echo doctor). Forms meant to send nothing can ignore this."}
	i.Identify()
	return i
}
