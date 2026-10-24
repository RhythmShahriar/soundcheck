// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/RhythmShahriar/soundcheck/config"
)

// schemes records the scheme of every request a client sends.
type schemes struct {
	base http.RoundTripper
	mu   sync.Mutex
	seen []string
}

func (s *schemes) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	s.seen = append(s.seen, r.URL.Scheme)
	s.mu.Unlock()
	return s.base.RoundTrip(r)
}

func (s *schemes) list() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.seen, ",")
}

func hostPort(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

func TestResolveBadCertificateDoesntDowngrade(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	rec := &schemes{base: http.DefaultTransport.(*http.Transport).Clone()}
	var logs []string
	_, err := resolve(context.Background(), &http.Client{Transport: rec}, hostPort(t, srv.URL), "", false,
		func(s string) { logs = append(logs, s) })
	if err == nil || !strings.Contains(err.Error(), "invalid TLS certificate") {
		t.Errorf("err = %v", err)
	}
	if got := rec.list(); got != "https" {
		t.Errorf("requests: %s, want one https request", got)
	}
	if len(logs) != 0 {
		t.Errorf("logs = %v", logs)
	}
}

func TestResolveRefusedHTTPSFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	// A port with nothing on it stands in for the site's closed 443.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := l.Addr().String()
	_ = l.Close()
	plain := http.DefaultTransport.(*http.Transport).Clone()
	secure := http.DefaultTransport.(*http.Transport).Clone()
	secure.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, closed)
	}
	rec := &schemes{base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme == "https" {
			return secure.RoundTrip(r)
		}
		return plain.RoundTrip(r)
	})}
	var logs []string
	got, err := resolve(context.Background(), &http.Client{Transport: rec}, hostPort(t, srv.URL), "", false,
		func(s string) { logs = append(logs, s) })
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != srv.URL+"/" || rec.list() != "https,http" {
		t.Errorf("start %s, requests %s", got, rec.list())
	}
	if strings.Join(logs, "|") != "!https refused; using http" {
		t.Errorf("logs = %v", logs)
	}
}

// A bare host:port on a plain-http server answers the https try with
// http, which isn't a TLS handshake: resolve falls back to http.
func TestResolvePlainHTTPPortFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	rec := &schemes{base: http.DefaultTransport.(*http.Transport).Clone()}
	var logs []string
	got, err := resolve(context.Background(), &http.Client{Transport: rec}, hostPort(t, srv.URL), "", false,
		func(s string) { logs = append(logs, s) })
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != srv.URL+"/" || rec.list() != "https,http" {
		t.Errorf("start %s, requests %s", got, rec.list())
	}
	if strings.Join(logs, "|") != "!https refused; using http" {
		t.Errorf("logs = %v", logs)
	}
}

func TestNoTLS(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}, true},
		{fmt.Errorf("get: %w", tls.RecordHeaderError{}), true},
		{http.ErrSchemeMismatch, true},
		{errors.New(`Get "https://x": tls: first record does not look like a TLS handshake`), true},
		{&tls.CertificateVerificationError{Err: errors.New("x509: unknown authority")}, false},
		{errors.New("connection reset by peer"), false},
	} {
		if got := noTLS(tc.err); got != tc.want {
			t.Errorf("noTLS(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestResolveOtherHost(t *testing.T) {
	var hits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/welcome", http.StatusFound)
	}))
	defer srv.Close()

	var logs []string
	log := func(s string) { logs = append(logs, s) }
	_, err := resolve(context.Background(), &http.Client{}, srv.URL, "", false, log)
	if err == nil || !strings.Contains(err.Error(), srv.URL+" redirects to "+other.URL+"/welcome") {
		t.Errorf("err = %v", err)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the other site got %d requests", n)
	}

	logs = nil
	got, err := resolve(context.Background(), &http.Client{}, srv.URL, "", true, log)
	if err != nil || got.String() != other.URL+"/welcome" {
		t.Fatalf("follow: %v, %v", got, err)
	}
	if hits.Load() != 1 || strings.Join(logs, "|") != "!moved to "+other.URL+"/welcome" {
		t.Errorf("hits %d, logs %v", hits.Load(), logs)
	}
}

// Moving between apex and www is the same site: it's followed and logged.
func TestResolveWWW(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Host, "www.") {
			http.Redirect(w, r, "http://www."+r.Host+r.URL.RequestURI(), http.StatusMovedPermanently)
		}
	}))
	defer srv.Close()
	addr := hostPort(t, srv.URL)
	_, port, _ := net.SplitHostPort(addr)
	tr := http.DefaultTransport.(*http.Transport).Clone()
	// Every name dials the test server, so no lookup leaves the machine.
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	var logs []string
	got, err := resolve(context.Background(), &http.Client{Transport: tr}, "http://shop.test:"+port+"/menu?day=mon", "", false,
		func(s string) { logs = append(logs, s) })
	want := "http://www.shop.test:" + port + "/menu?day=mon"
	if err != nil || got.String() != want {
		t.Fatalf("got %v, %v; want %s", got, err, want)
	}
	if strings.Join(logs, "|") != "!moved to "+want {
		t.Errorf("logs = %v", logs)
	}
}

// Run stops before opening a browser when the target moves to another site.
func TestRunRefusesOtherHost(t *testing.T) {
	var hits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer other.Close()
	srv := httptest.NewServer(http.RedirectHandler(other.URL+"/", http.StatusFound))
	defer srv.Close()
	run, err := Run(context.Background(), Options{Config: config.Default(), Target: srv.URL, Dir: t.TempDir(), Version: "test"})
	if run != nil || err == nil || !strings.Contains(err.Error(), "which is another site") || hits.Load() != 0 {
		t.Errorf("run %v, err %v, other site hits %d", run, err, hits.Load())
	}
}

// An https target that redirects to http is refused: the run's headers
// would go out in cleartext. A bare hostname never asked for https and
// may move.
func TestResolveDowngrade(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil {
			http.Redirect(w, r, "http://"+r.Host+"/plain", http.StatusFound)
		}
	}))
	defer srv.Close()
	addr := hostPort(t, srv.URL)
	for _, follow := range []bool{false, true} {
		_, err := resolve(context.Background(), srv.Client(), srv.URL, "", follow, func(string) {})
		if want := srv.URL + " redirects to http://" + addr + "/plain, which drops https"; err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("follow %v: err = %v", follow, err)
		}
	}
	var logs []string
	got, err := resolve(context.Background(), srv.Client(), addr, "", false, func(s string) { logs = append(logs, s) })
	if err != nil || got.String() != "http://"+addr+"/plain" || strings.Join(logs, "|") != "!moved to http://"+addr+"/plain" {
		t.Errorf("bare host: %v, %v, logs %v", got, err, logs)
	}
}
