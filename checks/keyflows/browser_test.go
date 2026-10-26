// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyflows

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/keyboard/ids"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/internal/testenv"
	"github.com/RhythmShahriar/soundcheck/model"
)

// orderShop is a checkout that counts the orders placed. Its Place
// order is a button, or with mouseOnly a div only a mouse can use.
type orderShop struct {
	*httptest.Server
	mu     sync.Mutex
	orders []url.Values
}

const shopPage = `<!doctype html><html lang="en"><body>
<form method="post" action="/order/">
<label>Email <input id="email" name="email" type="email"></label>
<label>Password <input id="pass" name="pass" type="password"></label>
<label><input type="checkbox" id="terms" name="terms"> I agree</label>
%s
</form></body></html>`

func newOrderShop(t *testing.T, mouseOnly bool) *orderShop {
	t.Helper()
	s := &orderShop{}
	button := `<button id="place_order">Place order</button>`
	if mouseOnly {
		button = `<div id="place_order" onclick="this.closest('form').submit()">Place order</div>`
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `<html><body>Home</body></html>`) })
	mux.HandleFunc("/checkout/", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintf(w, shopPage, button) })
	mux.HandleFunc("/order/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			s.mu.Lock()
			s.orders = append(s.orders, r.PostForm)
			s.mu.Unlock()
		}
		fmt.Fprint(w, `<html><body><h1>Thank you</h1></body></html>`)
	})
	srv := httptest.NewUnstartedServer(mux)
	// The ports 20360-20379 are this suite's, so it takes no other's.
	for port := 20360; port <= 20379 && s.Server == nil; port++ {
		if l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			srv.Listener = l
			srv.Start()
			s.Server = srv
		}
	}
	if s.Server == nil {
		t.Fatal("ports 20360-20379 are all taken")
	}
	t.Cleanup(srv.Close)
	return s
}

func (s *orderShop) placed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.orders)
}

// canaryEnv holds a password assembled at run time, so no literal
// secret is in the source. Flows may read SOUNDCHECK_* variables.
const canaryEnv = "SOUNDCHECK_KEYFLOWS_CANARY"

var canary = "Kf" + "lw-" + strings.Repeat("3", 7) + "-pw"

const orderFlow = `name: Order by keyboard
start: /checkout/
keyboard: %s
steps:
  - fill: {selector: "#email", value: "${fake:email}"}
  - fill: {selector: "#pass", value: "${env:SOUNDCHECK_KEYFLOWS_CANARY}"}
  - check: "#terms"
  - click: "#place_order"
  - expect: {text: Thank you}
`

// keyRun runs one key flow through Run against s in a real browser and
// returns the run and the files it saved.
func keyRun(t *testing.T, s *orderShop, mode string, tune func(*check.Context)) (*model.Run, map[string][]byte) {
	t.Helper()
	if testing.Short() {
		t.Skip("browser test")
	}
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
	t.Setenv(canaryEnv, canary)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "order.yaml"), []byte(fmt.Sprintf(orderFlow, mode)), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	cfg := config.Default()
	cfg.Flows = dir
	cfg.Interactions.Levels = []string{"flows"}
	cfg.Timeout = testenv.Seconds(config.Seconds(5))
	base, _ := url.Parse(s.URL + "/")
	saved := map[string][]byte{}
	var mu sync.Mutex
	cc := &check.Context{Config: cfg, Base: base, Run: &model.Run{Pages: []model.Page{{Path: "/checkout/"}}},
		NewSession: func() (*rod.Browser, error) { return b.Session() },
		Save: func(name string, data []byte) error {
			mu.Lock()
			saved[name] = data
			mu.Unlock()
			return nil
		}}
	if tune != nil {
		tune(cc)
	}
	if err := cc.Config.Validate(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), testenv.Timeout(90*time.Second))
	defer cancel()
	Run(ctx, cc, model.KeyboardOptions{MaxTabs: 8})
	// What a run stores and reports comes from the run itself: the
	// canary password typed must be in none of it.
	out, err := json.Marshal(cc.Run)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), canary) {
		t.Errorf("the canary is in the run: %s", out)
	}
	for name, data := range saved {
		if strings.Contains(string(data), canary) {
			t.Errorf("the canary is in %s", name)
		}
	}
	return cc.Run, saved
}

func keyIssues(r *model.Run) []model.Issue {
	out := slices.Clone(r.Issues)
	for _, p := range r.Pages {
		out = append(out, p.Issues...)
	}
	return out
}

// keyboard: true places no order and says what it would have sent;
// keyboard: send on this machine places one.
func TestKeyFlowOrders(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		tune       func(*check.Context)
		orders     int
		warnings   []string
	}{
		{"dry", "true", nil, 0,
			[]string{"keyboard: order.yaml stopped at step 4 (would have sent POST /order/); step 5 not run"}},
		{"send on loopback", "send", nil, 1, nil},
		{"send on a public address", "send", func(cc *check.Context) { cc.TargetPublic = true }, 0, []string{
			"keyboard: send runs only on a loopback or private address, or with --staging; order.yaml ran dry",
			"keyboard: order.yaml stopped at step 4 (would have sent POST /order/); step 5 not run"}},
		{"send with --staging", "send", func(cc *check.Context) { cc.TargetPublic = true; cc.Config.Trust.Staging = true }, 1, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newOrderShop(t, false)
			run, saved := keyRun(t, s, tc.mode, tc.tune)
			if n := s.placed(); n != tc.orders {
				t.Errorf("%d orders placed, want %d", n, tc.orders)
			}
			if !slices.Equal(run.Warnings, tc.warnings) {
				t.Errorf("warnings %q, want %q", run.Warnings, tc.warnings)
			}
			if is := keyIssues(run); len(is) != 0 {
				t.Errorf("issues: %+v", is)
			}
			if kf := run.KeyFlows; kf == nil || kf.Run != 1 {
				t.Errorf("KeyFlows = %+v", kf)
			}
			trace := string(saved["keyflows/order-by-keyboard.txt"])
			if want := fmt.Sprintf(`step 2 fill "#pass": <typed %d characters>`, len(canary)); !strings.Contains(trace, want) {
				t.Errorf("trace:\n%s", trace)
			}
		})
	}
}

// On a URL listed under production a key flow is refused as any flow
// is. It places no order.
func TestKeyFlowRefusedOnProduction(t *testing.T) {
	s := newOrderShop(t, false)
	run, _ := keyRun(t, s, "send", func(cc *check.Context) { cc.Config.Production = []string{"127.0.0.1"} })
	if n := s.placed(); n != 0 {
		t.Errorf("%d orders placed", n)
	}
	is := keyIssues(run)
	if len(is) != 1 || is[0].Check != "interact/flow-production" ||
		is[0].Title != `Flow "Order by keyboard" step 4 refused on production: click #place_order` ||
		is[0].Detail != "refused on a production URL (set production: true in the flow to allow it)" {
		t.Errorf("issues: %+v", is)
	}
}

// A Place order only a mouse can use is a keyboard/flow issue on the
// checkout page, with the step, the Tab stops and the focus path.
func TestKeyFlowMouseOnly(t *testing.T) {
	s := newOrderShop(t, true)
	run, _ := keyRun(t, s, "send", nil)
	if n := s.placed(); n != 0 {
		t.Errorf("%d orders placed", n)
	}
	is := run.Page("/checkout/").Issues
	if len(is) != 1 || len(run.Issues) != 0 {
		t.Fatalf("page issues %+v, site issues %+v", is, run.Issues)
	}
	i := is[0]
	prefix := `Order by keyboard: step 4 click "#place_order": not reached after 8 Tab stops; focus stayed on `
	if i.Check != ids.Flow || i.Source != "order.yaml: click #place_order" || !strings.HasPrefix(i.Detail, prefix) ||
		len(i.FocusPath) != 6 || i.Impact != "critical" {
		t.Errorf("issue: %+v", i)
	}
	if !strings.HasSuffix(i.Detail, i.FocusPath[len(i.FocusPath)-1]) {
		t.Errorf("focus stayed on a stop that isn't the path's last: %q %q", i.Detail, i.FocusPath)
	}
}
