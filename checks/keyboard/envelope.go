// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/model"
)

// The safety envelope: what keeps a key press from doing anything a
// visitor's press would do beyond the page itself (security K1, K2, X4,
// S3). guard and arm (record.go) hold requests other than GET and HEAD
// and cancel navigations the page starts; envelope adds the rest, for as
// long as the walk has the tab:
//
//   - a navigation of the main frame the Navigation API couldn't cancel
//     is stopped through the protocol (Page.stopLoading) before its
//     request leaves;
//   - WebSocket connections are refused (a Content-Security-Policy of
//     the inspector's making on each document, as Chrome's request
//     interception never sees them) and requests carrying an Upgrade
//     header are held;
//   - the page's service worker is bypassed and, when the site has one,
//     its own requests other than GET and HEAD are held as well;
//   - downloads are denied, file choosers canceled, JavaScript dialogs
//     dismissed, permission prompts (location, notifications, camera,
//     microphone, clipboard, MIDI) denied and passkey requests refused
//     by a virtual authenticator that never verifies a user.
//
// What it stopped goes into the trace's held and events lists.

// deniedPermissions are the permissions a page asking during the walk
// is refused.
var deniedPermissions = []string{"geolocation", "notifications", "camera", "microphone", "clipboard-read", "midi"}

// wsPolicyJS adds a Content-Security-Policy to each document of the page
// that allows every connection but WebSocket ones (http: and https:
// sources don't match ws: or wss:). It listens for what it refused. A
// policy, once added, stays with the document: the meta element can go
// at once. A policy can only narrow what the page's own allows.
const wsPolicyJS = `(() => {
  const g = window.__scKbNav;
  const docs = [document];
  for (let i = 0; i < frames.length; i++) { try { if (frames[i].document) docs.push(frames[i].document); } catch (e) {} }
  for (const d of docs) {
    if (d.__scKbWS) continue;
    d.__scKbWS = true;
    const m = d.createElement('meta');
    m.httpEquiv = 'Content-Security-Policy';
    m.content = "connect-src http: https: data: blob:";
    (d.head || d.documentElement).appendChild(m);
    m.remove();
    d.addEventListener('securitypolicyviolation', e => {
      if (g && /^wss?:/i.test(e.blockedURI || '')) {
        let h = '';
        try { h = new URL(e.blockedURI).host; } catch (x) {}
        g.ws.push(h);
      }
    }, true);
  }
  return '';
})()`

// webAuthnJS replaces the page's navigator.credentials.get and create,
// in the page's own world where its scripts call them, with versions
// that refuse at once (NotAllowedError, what a visitor's cancel gives)
// and tell the walk. A reference the page took before the walk still
// reaches the virtual authenticator, which never verifies a user.
const webAuthnJS = `(() => {
  const c = navigator.credentials;
  if (!c || c.__scKb) return '';
  const refuse = what => function () {
    window.dispatchEvent(new CustomEvent('soundcheck-keyboard-passkey', {detail: what}));
    return Promise.reject(new DOMException('The keyboard walk refuses passkey requests.', 'NotAllowedError'));
  };
  try {
    Object.defineProperty(c, 'get', {value: refuse('get'), configurable: true});
    Object.defineProperty(c, 'create', {value: refuse('create'), configurable: true});
    Object.defineProperty(c, '__scKb', {value: true});
  } catch (e) {}
  return '';
})()`

// webAuthnLogJS, run in the walk's world, counts the refusals webAuthnJS
// announces.
const webAuthnLogJS = `(() => {
  const g = window.__scKbNav;
  if (g && !g.passkeys) {
    g.passkeys = true;
    window.addEventListener('soundcheck-keyboard-passkey', e => g.pk.push(String(e.detail)));
  }
  return '';
})()`

// envelope arms the tab-wide parts of the envelope and returns what
// undoes them. Each part that fails is noted as an event and the rest
// go on: the walk itself never sends a write either way, as guard holds
// those.
func (w *walker) envelope(ctx, wctx context.Context) func() {
	g, tab := w.guard, w.tab
	p := tab.Context(ctx)
	b := tab.Browser()
	note := func(what string, err error) {
		if err != nil {
			g.mu.Lock()
			g.event(what + ": " + err.Error())
			g.mu.Unlock()
		}
	}
	var undo []func()

	note("can't enable the network log", proto.NetworkEnable{}.Call(p))
	if err := (proto.NetworkSetBypassServiceWorker{Bypass: true}).Call(p); err != nil {
		note("service worker not bypassed", err)
	} else {
		undo = append(undo, func() { _ = proto.NetworkSetBypassServiceWorker{Bypass: false}.Call(tab) })
	}
	note("downloads not denied", proto.BrowserSetDownloadBehavior{Behavior: proto.BrowserSetDownloadBehaviorBehaviorDeny,
		BrowserContextID: b.BrowserContextID, EventsEnabled: true}.Call(b))
	if err := (proto.PageSetInterceptFileChooserDialog{Enabled: true}).Call(p); err != nil {
		note("file chooser not intercepted", err)
	} else {
		undo = append(undo, func() { _ = proto.PageSetInterceptFileChooserDialog{Enabled: false}.Call(tab) })
	}
	if origin := pageOrigin(w.page); origin != "" {
		for _, name := range deniedPermissions {
			_ = proto.BrowserSetPermission{Permission: &proto.BrowserPermissionDescriptor{Name: name},
				Setting: proto.BrowserPermissionSettingDenied, Origin: origin, BrowserContextID: b.BrowserContextID}.Call(b)
		}
	}
	if err := (proto.WebAuthnEnable{}).Call(p); err != nil {
		note("no virtual authenticator", err)
	} else if va, err := (proto.WebAuthnAddVirtualAuthenticator{Options: &proto.WebAuthnVirtualAuthenticatorOptions{
		Protocol: proto.WebAuthnAuthenticatorProtocolCtap2, Transport: proto.WebAuthnAuthenticatorTransportInternal,
		HasUserVerification: true, IsUserVerified: false, AutomaticPresenceSimulation: false,
	}}).Call(p); err != nil {
		note("no virtual authenticator", err)
		_ = proto.WebAuthnDisable{}.Call(tab)
	} else {
		undo = append(undo, func() {
			_ = proto.WebAuthnRemoveVirtualAuthenticator{AuthenticatorID: va.AuthenticatorID}.Call(tab)
			_ = proto.WebAuthnDisable{}.Call(tab)
		})
	}

	main := tab.FrameID
	waitPage := tab.Context(wctx).EachEvent(
		func(e *proto.PageFrameRequestedNavigation) {
			if e.FrameID != main || !g.on() || sameDocument(w.page, e.URL) {
				return
			}
			// Stopped before its request leaves: request interception
			// holds the request for this long.
			_ = proto.PageStopLoading{}.Call(tab)
			g.mu.Lock()
			defer g.mu.Unlock()
			g.stopTop(e.URL)
			if e.Disposition == proto.PageClientNavigationDispositionDownload {
				g.event("download blocked: " + pathOfRaw("GET", e.URL))
				return
			}
			if e.Reason == proto.PageClientNavigationReasonFormSubmissionPost {
				// A form sent: held like the write it is.
				g.hold(pathOfRaw("POST", e.URL))
				g.effect(EffectForm)
				g.event("navigation stopped: " + pathOfRaw("POST", e.URL))
				return
			}
			g.effect(EffectNavigation)
			g.event("navigation stopped: " + pathOfRaw("GET", e.URL))
		},
		func(e *proto.PageJavascriptDialogOpening) {
			// Dismissed, never accepted: a confirm says no, a prompt
			// gives nothing, a beforeunload keeps the page.
			go func() { _ = proto.PageHandleJavaScriptDialog{Accept: false}.Call(tab) }()
			g.mu.Lock()
			g.event("dialog dismissed: " + string(e.Type))
			g.mu.Unlock()
		},
		func(*proto.PageFileChooserOpened) {
			g.mu.Lock()
			g.event("file chooser canceled")
			g.mu.Unlock()
		},
	)
	go waitPage()
	waitBrowser := b.Context(wctx).EachEvent(
		func(e *proto.BrowserDownloadWillBegin) {
			if !w.ownFrame(e.FrameID) {
				return
			}
			g.mu.Lock()
			g.event("download blocked: " + pathOfRaw("GET", e.URL))
			g.mu.Unlock()
		},
		func(e *proto.TargetTargetCreated) {
			if e.TargetInfo != nil && e.TargetInfo.Type == "service_worker" {
				w.holdWorker(wctx, e.TargetInfo)
			}
		},
	)
	go waitBrowser()
	if targets, err := (proto.TargetGetTargets{}).Call(b); err == nil {
		for _, ti := range targets.TargetInfos {
			if ti.Type == "service_worker" {
				w.holdWorker(wctx, ti)
			}
		}
	}
	return func() {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
		w.workers.Wait()
	}
}

// ownFrame reports whether a frame is the walked tab's main frame or
// one of its frames.
func (w *walker) ownFrame(id proto.PageFrameID) bool {
	if id == w.tab.FrameID {
		return true
	}
	tree, err := (proto.PageGetFrameTree{}).Call(w.tab)
	return err == nil && hasFrame(tree.FrameTree, id)
}

func hasFrame(t *proto.PageFrameTree, id proto.PageFrameID) bool {
	if t == nil || t.Frame == nil {
		return false
	}
	if t.Frame.ID == id {
		return true
	}
	for _, c := range t.ChildFrames {
		if hasFrame(c, id) {
			return true
		}
	}
	return false
}

// holdWorker attaches to a service worker of the walked page's origin
// and holds its requests other than GET and HEAD, as guard does the
// page's: a worker can send what a key press asked of it on its own,
// past the tab's interception. The hold ends with the walk.
func (w *walker) holdWorker(wctx context.Context, ti *proto.TargetTargetInfo) {
	origin := pageOrigin(w.page)
	if origin == "" || !strings.HasPrefix(ti.URL, origin+"/") || ti.BrowserContextID != w.tab.Browser().BrowserContextID {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.workerSeen == nil {
		w.workerSeen = map[proto.TargetTargetID]bool{}
	}
	if w.workerSeen[ti.TargetID] {
		return
	}
	w.workerSeen[ti.TargetID] = true
	b := w.tab.Browser()
	at, err := proto.TargetAttachToTarget{TargetID: ti.TargetID, Flatten: true}.Call(b)
	if err != nil {
		return
	}
	// rod's session pages can't be given a context of their own: the
	// one for calls lives on, the one for events ends with the walk.
	sw := b.PageFromSession(at.SessionID)
	events := b.Context(wctx).PageFromSession(at.SessionID)
	if err := (proto.FetchEnable{Patterns: []*proto.FetchRequestPattern{{URLPattern: "*"}}}).Call(sw); err != nil {
		_ = proto.TargetDetachFromTarget{SessionID: at.SessionID}.Call(b)
		return
	}
	g := w.guard
	wait := events.EachEvent(func(e *proto.FetchRequestPaused) {
		m := e.Request.Method
		if m == http.MethodGet || m == http.MethodHead {
			_ = proto.FetchContinueRequest{RequestID: e.RequestID}.Call(sw)
			return
		}
		_ = proto.FetchFailRequest{RequestID: e.RequestID, ErrorReason: proto.NetworkErrorReasonBlockedByClient}.Call(sw)
		g.mu.Lock()
		g.hold(pathOfRaw(m, e.Request.URL))
		g.event("service worker request held: " + pathOfRaw(m, e.Request.URL))
		g.mu.Unlock()
	})
	w.workers.Add(1)
	go func() {
		defer w.workers.Done()
		wait()
		// The walk is over: let the worker go on unwatched.
		_ = proto.FetchDisable{}.Call(sw)
		_ = proto.TargetDetachFromTarget{SessionID: at.SessionID}.Call(b)
	}()
}

// pageURL is the address the page was walked at: where it ended up
// after redirects.
func pageURL(p *model.Page) string {
	if p.FinalURL != "" {
		return p.FinalURL
	}
	return p.URL
}

// pageOrigin is the walked page's scheme and host, "" when its URL
// doesn't parse.
func pageOrigin(p *model.Page) string {
	u, err := url.Parse(pageURL(p))
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// sameDocument reports whether raw is the walked page with at most a
// different fragment: a skip link's jump, which may go ahead.
func sameDocument(p *model.Page, raw string) bool {
	a, err1 := url.Parse(pageURL(p))
	b, err2 := url.Parse(raw)
	if err1 != nil || err2 != nil {
		return false
	}
	a.Fragment, b.Fragment, a.RawFragment, b.RawFragment = "", "", "", ""
	return a.String() == b.String()
}

// pathOfRaw is pathOf for a URL as text.
func pathOfRaw(method, raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return method
	}
	return pathOf(method, u)
}

// docArm is the part of the envelope each document needs again after
// a load: the WebSocket policy and the passkey refusal.
func (w *walker) docArm(ctx context.Context) {
	if _, err := w.reader.Eval(ctx, wsPolicyJS); err != nil {
		w.guard.mu.Lock()
		w.guard.event("WebSocket policy not set: " + err.Error())
		w.guard.mu.Unlock()
	}
	_, _ = w.reader.Eval(ctx, webAuthnLogJS)
	if _, err := (proto.RuntimeEvaluate{Expression: webAuthnJS}).Call(w.tab.Context(ctx)); err != nil {
		w.guard.mu.Lock()
		w.guard.event("passkeys not refused in the page: " + err.Error())
		w.guard.mu.Unlock()
	}
}
