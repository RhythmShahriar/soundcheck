// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package review

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/report"
)

//go:embed toolbar.html.tmpl
var toolbarSource string

var toolbarTemplate = template.Must(template.New("toolbar").Parse(toolbarSource))

// TokenHeader carries the session token on every decision the review
// page sends. A custom header also means a page from another origin
// can't send one without a CORS preflight, which this server never
// answers.
const TokenHeader = "X-Soundcheck-Token"

// maxBody caps a decision request: an id and an action name.
const maxBody = 4 << 10

// Serve opens the result's HTML report for review: every change gets an
// Accept and an Ignore button that call decide. It listens on a free
// port of 127.0.0.1, prints the page's address to out and serves until
// ctx is done.
//
// The address carries a random token that every request must repeat,
// and requests must name the loopback address as their Host, so neither
// another page in the browser nor a DNS rebinding attack can make
// decisions.
func Serve(ctx context.Context, res report.Result, out io.Writer, decide Decide) error {
	s, err := newServer(res, decide)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("review: can't listen on 127.0.0.1: %w", err)
	}
	s.host = ln.Addr().String()
	srv := &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	fmt.Fprintf(out, "Review the changes in your browser:\n  %s\nPress Ctrl-C here when you're done.\n", s.URL())
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shut, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(shut)
	return nil
}

// server holds one review session.
type server struct {
	host   string // 127.0.0.1:port, the only Host accepted
	token  string
	page   []byte
	byID   map[string]model.Change
	decide Decide

	mu      sync.Mutex // decisions run one at a time: decide writes files
	decided map[string]Action
}

// barItem is what the page's script needs for one report entry. Entries
// line up with the report's articles in order; Fixed ones are null.
type barItem struct {
	ID    string `json:"id"`
	Class string `json:"class"`
	Title string `json:"title"`
}

type barData struct {
	Token  string     `json:"token"`
	Header string     `json:"header"`
	Items  []*barItem `json:"items"`
}

func newServer(res report.Result, decide Decide) (*server, error) {
	if !res.Compared() {
		return nil, errors.New("nothing to review: this result isn't a comparison")
	}
	tok := make([]byte, 24)
	if _, err := rand.Read(tok); err != nil {
		return nil, err
	}
	s := &server{token: hex.EncodeToString(tok), byID: map[string]model.Change{}, decide: decide, decided: map[string]Action{}}

	// The report lists changes by class in model.Classes order; the bar
	// data follows the same order so the script can pair them up.
	data := barData{Token: s.token, Header: TokenHeader}
	for _, class := range model.Classes {
		for _, c := range res.Comparison.Changes {
			if c.Class != class {
				continue
			}
			if class == model.Fixed {
				data.Items = append(data.Items, nil)
				continue
			}
			id := ID(c)
			for n := 2; s.byID[id].Class != ""; n++ { // the same problem in two classes
				id = ID(c) + "-" + strconv.Itoa(n)
			}
			s.byID[id] = c
			data.Items = append(data.Items, &barItem{ID: id, Class: c.Class, Title: c.Title})
		}
	}

	var page bytes.Buffer
	if err := report.HTML(&page, res); err != nil {
		return nil, err
	}
	var bar bytes.Buffer
	if err := toolbarTemplate.Execute(&bar, data); err != nil {
		return nil, err
	}
	html := page.Bytes()
	end := bytes.LastIndex(html, []byte("</body>"))
	if end < 0 {
		end = len(html)
	}
	s.page = append(append(append([]byte{}, html[:end]...), bar.Bytes()...), html[end:]...)
	return s, nil
}

// URL is the page's address, token included.
func (s *server) URL() string { return "http://" + s.host + "/?token=" + s.token }

// Decided returns the last action taken on each change id.
func (s *server) Decided() map[string]Action {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Action, len(s.decided))
	for k, v := range s.decided {
		out[k] = v
	}
	return out
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	// A Host other than our own address means the browser was pointed
	// here through some other name: DNS rebinding.
	if r.Host != s.host {
		http.Error(w, "wrong host", http.StatusForbidden)
		return
	}
	switch r.URL.Path {
	case "/":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !s.validToken(r.URL.Query().Get("token")) {
			http.Error(w, "this review session needs the address printed in the terminal", http.StatusForbidden)
			return
		}
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Content-Security-Policy", "default-src 'none'; img-src data:; style-src 'unsafe-inline'; "+
			"script-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		_, _ = w.Write(s.page)
	case "/decide":
		s.serveDecide(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *server) validToken(t string) bool {
	return subtle.ConstantTimeCompare([]byte(t), []byte(s.token)) == 1
}

// serveDecide handles one button press: {"id": "...", "action": "accept"}.
func (s *server) serveDecide(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.validToken(r.Header.Get(TokenHeader)) {
		http.Error(w, "wrong or missing token", http.StatusForbidden)
		return
	}
	if o := r.Header.Get("Origin"); o != "" && o != "http://"+s.host {
		http.Error(w, "wrong origin", http.StatusForbidden)
		return
	}
	var req struct{ ID, Action string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	a, err := ParseAction(req.Action)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	c, ok := s.byID[req.ID]
	if !ok {
		http.Error(w, "no change with that id", http.StatusNotFound)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if a != Skip {
		if err := s.decide(c, a); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	s.decided[req.ID] = a
	writeJSON(w, http.StatusOK, map[string]string{"id": req.ID, "action": string(a)})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
