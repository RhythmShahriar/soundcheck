// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package demo is the small site `rhythm-soundcheck demo` checks: five
// pages served from the binary, with a working state and a broken one,
// so Soundcheck can be tried without Docker or a site of your own.
//
// The broken state plants one problem of each kind a real update causes:
// a PHP warning printed in the footer of every page, a JS error on the
// home page, a button whose script no longer binds, a page that lost its
// title, a page that went missing (404) and a contact form whose handler
// answers 500. Nothing runs PHP: the warning is the text PHP would print.
package demo

import (
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// Breaks lists what the broken state changes, in the words the demo
// prints after flipping the site.
var Breaks = []string{
	"a PHP warning in the footer of every page",
	"a JS error on /",
	"a dead button on /about/",
	"no title on /blog/",
	"/pricing/ answers 404",
	"the contact form answers 500",
}

// Site is the demo site. The zero value is the working state; it is
// safe to flip while requests are being served.
type Site struct {
	broken atomic.Bool
}

// SetBroken switches the site between its working and broken states.
func (s *Site) SetBroken(broken bool) { s.broken.Store(broken) }

// Broken reports whether the site is in its broken state.
func (s *Site) Broken() bool { return s.broken.Load() }

// ServeHTTP serves the five pages, the contact form's handler and its
// thank-you page. Anything else is a 404.
func (s *Site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	broken := s.Broken()
	switch r.URL.Path {
	case "/":
		s.home(w, broken)
	case "/about/":
		s.about(w, broken)
	case "/pricing/":
		if broken {
			s.notFound(w)
			return
		}
		s.pricing(w)
	case "/blog/":
		s.blog(w, broken)
	case "/contact/":
		s.contact(w, r, broken)
	case "/contact/thanks/":
		page(w, "Thanks", broken, `<h1>Thanks</h1><p role="status">We got your message and will answer within a day.</p>`, "")
	default:
		s.notFound(w)
	}
}

func (s *Site) home(w http.ResponseWriter, broken bool) {
	script := `<script>window.demoCart = {count: function () { return 0; }}; document.getElementById('cart').textContent = window.demoCart.count();</script>`
	if broken {
		// A plugin update renamed its global, so the page's own script
		// throws on load.
		script = `<script>document.getElementById('cart').textContent = window.demoCart.count();</script>`
	}
	page(w, "Home", broken, `<h1>Brightside Bakery</h1>
<p>Fresh bread every morning, cakes to order and a small cafe by the river.</p>
<p>Items in your basket: <span id="cart">0</span></p>
<ul><li><a href="/about/">Meet the bakers</a></li><li><a href="/pricing/">Cake prices</a></li><li><a href="/contact/">Order a cake</a></li></ul>`, script)
}

func (s *Site) about(w http.ResponseWriter, broken bool) {
	// The theme's new script looks for a class the template doesn't
	// render, so the button is still there but nothing listens to it.
	sel := ".team-toggle"
	if broken {
		sel = ".js-team-toggle"
	}
	script := `<script>(function () {
  var button = document.querySelector('` + sel + `');
  var panel = document.getElementById('team');
  if (!button || !panel) return;
  button.addEventListener('click', function () {
    var open = button.getAttribute('aria-expanded') === 'true';
    button.setAttribute('aria-expanded', String(!open));
    panel.hidden = open;
  });
})();</script>`
	page(w, "About us", broken, `<h1>About us</h1>
<p>Three bakers, one oven and a lot of flour since 2011.</p>
<button type="button" class="team-toggle" aria-controls="team" aria-expanded="false">Show the team</button>
<ul id="team" hidden><li>Ada, bread</li><li>Ben, cakes</li><li>Cleo, coffee</li></ul>`, script)
}

func (s *Site) pricing(w http.ResponseWriter) {
	page(w, "Cake prices", false, `<h1>Cake prices</h1>
<table><tr><th>Cake</th><th>Serves</th><th>Price</th></tr>
<tr><td>Lemon drizzle</td><td>8</td><td>$24</td></tr>
<tr><td>Chocolate fudge</td><td>12</td><td>$36</td></tr>
<tr><td>Carrot and walnut</td><td>10</td><td>$30</td></tr></table>`, "")
}

func (s *Site) blog(w http.ResponseWriter, broken bool) {
	title := "Blog"
	if broken {
		title = "" // the theme stopped printing <title>
	}
	page(w, title, broken, `<h1>From the bakery</h1>
<article><h2>Why our sourdough takes two days</h2><p>Slow dough tastes better and keeps longer. Here is how we make it.</p></article>
<article><h2>New this autumn: pumpkin loaf</h2><p>Spiced, soft and back until the end of November.</p></article>`, "")
}

func (s *Site) contact(w http.ResponseWriter, r *http.Request, broken bool) {
	if r.Method == http.MethodPost {
		if broken {
			// The form plugin's mail handler crashes after the update.
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`<!doctype html><link rel="icon" href="data:,"><title>Server error</title><h1>Internal Server Error</h1>`))
			return
		}
		_ = r.ParseForm()
		http.Redirect(w, r, "/contact/thanks/", http.StatusSeeOther)
		return
	}
	page(w, "Order a cake", broken, `<h1>Order a cake</h1>
<form id="order" method="post" action="/contact/" aria-label="Cake order">
<p><label for="name">Name</label> <input id="name" name="name" autocomplete="name" required></p>
<p><label for="email">Email</label> <input id="email" name="email" type="email" autocomplete="email" required></p>
<p><label for="message">Which cake, and for when?</label><br><textarea id="message" name="message" required></textarea></p>
<p><button type="submit">Send the order</button></p>
</form>`, "")
}

func (s *Site) notFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`<!doctype html><link rel="icon" href="data:,"><title>Not found</title><h1>Not found</h1>`))
}

// page writes one page in the site's frame. An empty title leaves out
// the <title> element.
func page(w http.ResponseWriter, title string, broken bool, body, script string) {
	head := ""
	if title != "" {
		head = "<title>" + html.EscapeString(title) + " · Brightside Bakery</title>\n"
	}
	footer := "Brightside Bakery, 12 River Lane."
	if broken {
		// What PHP prints with display_errors on when a theme reads a
		// setting that's gone.
		footer = `<br>
<b>Warning</b>:  Undefined array key "opening_hours" in <b>/var/www/html/wp-content/themes/brightside/footer.php</b> on line <b>14</b><br>
` + footer
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<link rel="icon" href="data:,">
%s<style>
body { margin: 0; font: 16px/1.6 system-ui, sans-serif; color: #2b2118; background: #fdf8f2; }
header, footer { background: #6b3e1f; color: #fff; padding: 16px 32px; }
header a { color: #fff; margin-right: 16px; }
main { max-width: 760px; margin: 0 auto; padding: 32px; }
td, th { padding: 6px 12px; text-align: left; }
</style>
</head>
<body>
<header><strong>Brightside</strong> &nbsp; <a href="/">Home</a><a href="/about/">About</a><a href="/pricing/">Prices</a><a href="/blog/">Blog</a><a href="/contact/">Order</a></header>
<main>
%s
</main>
<footer>%s</footer>
%s
</body>
</html>
`, head, body, footer, script)
}

// Server is the demo site listening on this machine.
type Server struct {
	// URL is where it answers: http://127.0.0.1:<port>.
	URL string
	srv *http.Server
	err chan error
}

// Start serves h on 127.0.0.1 on a free port. Only this machine can
// reach it.
func Start(h http.Handler) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{URL: "http://" + ln.Addr().String(), srv: &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}, err: make(chan error, 1)}
	go func() { s.err <- s.srv.Serve(ln) }()
	return s, nil
}

// Close stops the server and drops its connections. It's called once
// the comparison is done, so nothing is in flight; waiting for the
// browser's idle keep-alive connections would only hold up the exit.
func (s *Server) Close() error {
	err := s.srv.Close()
	if serveErr := <-s.err; !errors.Is(serveErr, http.ErrServerClosed) {
		err = errors.Join(err, serveErr)
	}
	return err
}

// Describe joins Breaks for one line: "a, b and c".
func Describe() string {
	if len(Breaks) < 2 {
		return strings.Join(Breaks, "")
	}
	return strings.Join(Breaks[:len(Breaks)-1], ", ") + " and " + Breaks[len(Breaks)-1]
}
