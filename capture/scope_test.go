// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package capture

import (
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/model"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func scopeFor(t *testing.T, site string, target ...string) *Scope {
	t.Helper()
	s := &Scope{Site: mustURL(t, site), Allow: []string{"pay.example.net", "127.0.0.1:37015"}}
	for _, a := range target {
		s.Target = append(s.Target, netip.MustParseAddr(a))
	}
	return s
}

func TestScopeBlocks(t *testing.T) {
	public := scopeFor(t, "https://shop.example.com/", "203.0.113.10")
	local := scopeFor(t, "http://127.0.0.1:37000/", "127.0.0.1")
	office := scopeFor(t, "http://intranet.lan/", "10.0.0.5")
	for _, c := range []struct {
		s    *Scope
		raw  string
		want bool
	}{
		// Link-local and metadata names: never, whatever the target.
		{public, "http://169.254.169.254/latest/meta-data/", true},
		{local, "http://169.254.169.254/latest/meta-data/", true},
		{office, "http://[fe80::1]/", true},
		{public, "http://metadata.google.internal/computeMetadata/v1/", true},
		{local, "http://METADATA.google.internal./", true},
		{public, "http://metadata/computeMetadata/v1/", true},
		{public, "http://[::ffff:169.254.169.254]/", true},
		// Loopback and private: only from a public target.
		{public, "http://127.0.0.1:8080/", true},
		{public, "http://localhost:3000/", true},
		{public, "http://admin.localhost/", true},
		{public, "http://192.168.1.1/", true},
		{public, "http://10.1.2.3/", true},
		{public, "http://[fd00::1]/", true},
		{public, "http://100.100.100.200/", true},
		{public, "ws://127.0.0.1:9222/devtools", true},
		{local, "http://127.0.0.1:37001/admin", false},
		{local, "http://localhost:3000/", false},
		{office, "http://192.168.1.1/", false},
		{office, "http://127.0.0.1/", false},
		// Public addresses, names and the site itself: always.
		{public, "https://cdn.example.org/app.js", false},
		{public, "http://203.0.113.99/", false},
		{public, "https://shop.example.com/cart", false},
		{public, "https://www.shop.example.com/", false},
		{public, "data:text/plain,hi", false},
		// --allow-host lets a host through, by host and port.
		{public, "http://127.0.0.1:37015/", false},
		{public, "http://127.0.0.1:37016/", true},
		{scopeFor(t, "https://example.com/", "203.0.113.10"), "https://pay.example.net/", false},
		// A site on a private address is itself always allowed.
		{scopeFor(t, "http://127.0.0.1:37000/", "203.0.113.10"), "http://127.0.0.1:37000/x", false},
		{scopeFor(t, "http://127.0.0.1:37000/", "203.0.113.10"), "http://127.0.0.1:37001/x", true},
	} {
		if got := c.s.Blocks(mustURL(t, c.raw)); got != c.want {
			t.Errorf("site %s: Blocks(%s) = %v, want %v", c.s.Site, c.raw, got, c.want)
		}
		// Chrome pauses only the URLs of scopePatterns: each blocked URL
		// must be among them, in the lower case Chrome gives hosts.
		if c.want && !matchesAny(c.s.scopePatterns(), strings.ToLower(c.raw)) {
			t.Errorf("site %s: %s is blocked but not paused", c.s.Site, c.raw)
		}
	}
	var nilScope *Scope
	if nilScope.Blocks(mustURL(t, "http://169.254.169.254/")) {
		t.Error("a nil scope blocks")
	}
}

// matchesAny matches raw against Fetch URL patterns ("*" any run, "?"
// one character).
func matchesAny(patterns []string, raw string) bool {
	for _, p := range patterns {
		re := "^" + strings.NewReplacer(`\*`, ".*", `\?`, ".").Replace(regexp.QuoteMeta(p)) + "$"
		if regexp.MustCompile(re).MatchString(raw) {
			return true
		}
	}
	return false
}

// Ordinary requests aren't paused: pausing costs a round trip, which
// the page's timings would show.
func TestScopePatternsLeaveOthersAlone(t *testing.T) {
	s := scopeFor(t, "https://shop.example.com/", "203.0.113.10")
	for _, raw := range []string{"https://shop.example.com/", "https://cdn.example.org/app.js", "https://fonts.example.net/a.woff2"} {
		if matchesAny(s.scopePatterns(), raw) {
			t.Errorf("%s is paused", raw)
		}
	}
	local := scopeFor(t, "http://127.0.0.1:37000/", "127.0.0.1")
	if matchesAny(local.scopePatterns(), "http://127.0.0.1:37000/") {
		t.Error("a local site's own requests are paused")
	}
}

// An answer from a private address to a public site (a name rebound
// after the request was let through) is left out of the record, with
// one warning per host and address.
func TestScopeResponse(t *testing.T) {
	var warnings []string
	s := scopeFor(t, "https://shop.example.com/", "203.0.113.10")
	s.Warn = func(w string) { warnings = append(warnings, w) }
	u := mustURL(t, "https://rebind.example.org/x.js")
	for range 2 { // warned about once
		if !s.Response(u, "10.0.0.7") {
			t.Error("a private answer to a public site isn't caught")
		}
	}
	if s.Response(u, "198.51.100.4") || s.Response(u, "") || s.Response(mustURL(t, "https://shop.example.com/"), "10.0.0.7") {
		t.Error("a public answer, an unknown one or the site's own is caught")
	}
	want := []string{"blocked a response from rebind.example.org at 10.0.0.7: private address while the target is public"}
	if !slices.Equal(warnings, want) {
		t.Errorf("warnings = %q", warnings)
	}
	local := scopeFor(t, "http://intranet.lan/", "10.0.0.5")
	if local.Response(u, "10.0.0.7") || !local.Response(u, "169.254.169.254") {
		t.Error("a local site's scope")
	}
}

// The recorder keeps a rebound answer out of the page's resources and
// records it, like a blocked request, as failed by the scope.
func TestRecorderDropsOutOfScopeAnswers(t *testing.T) {
	s := scopeFor(t, "https://shop.example.com/", "203.0.113.10")
	s.Warn = func(string) {}
	r := &Recorder{reqs: map[string]*req{}, scope: s}
	add := func(id, raw, remote string) {
		r.onRequest(&proto.NetworkRequestWillBeSent{RequestID: proto.NetworkRequestID(id), Type: proto.NetworkResourceTypeScript,
			Request: &proto.NetworkRequest{URL: raw, Method: "GET"}})
		r.onResponse(&proto.NetworkResponseReceived{RequestID: proto.NetworkRequestID(id), Type: proto.NetworkResourceTypeScript,
			Response: &proto.NetworkResponse{URL: raw, Status: 200, RemoteIPAddress: remote}})
		r.onFinished(&proto.NetworkLoadingFinished{RequestID: proto.NetworkRequestID(id), EncodedDataLength: 100})
	}
	add("1", "https://cdn.example.org/app.js", "198.51.100.4")
	add("2", "https://rebind.example.org/x.js", "10.0.0.7")
	r.onRequest(&proto.NetworkRequestWillBeSent{RequestID: "3", Type: proto.NetworkResourceTypeOther,
		Request: &proto.NetworkRequest{URL: "http://169.254.169.254/", Method: "GET"}})
	r.block("http://169.254.169.254/")
	r.onFailed(&proto.NetworkLoadingFailed{RequestID: "3", ErrorText: "net::ERR_BLOCKED_BY_CLIENT"})
	r.onRequest(&proto.NetworkRequestWillBeSent{RequestID: "4", Type: proto.NetworkResourceTypeXHR,
		Request: &proto.NetworkRequest{URL: "https://shop.example.com/held", Method: "POST"}})
	r.onFailed(&proto.NetworkLoadingFailed{RequestID: "4", ErrorText: "net::ERR_BLOCKED_BY_CLIENT.Inspector"})

	p := &model.Page{}
	r.fill(p, pageFacts{})
	var res []string
	for _, x := range p.Resources {
		res = append(res, x.URL)
	}
	if !slices.Equal(res, []string{"https://cdn.example.org/app.js"}) {
		t.Errorf("resources = %q", res)
	}
	failed := map[string]string{}
	for _, f := range p.Failed {
		failed[f.URL] = f.Error
	}
	want := map[string]string{
		"https://rebind.example.org/x.js": ScopeError,
		"http://169.254.169.254/":         ScopeError,
		// Blocked by another handler (interact holding a submit).
		"https://shop.example.com/held": "net::ERR_BLOCKED_BY_CLIENT.Inspector",
	}
	if len(failed) != len(want) {
		t.Errorf("failed = %v", failed)
	}
	for k, v := range want {
		if failed[k] != v {
			t.Errorf("failed[%s] = %q, want %q", k, failed[k], v)
		}
	}
}
