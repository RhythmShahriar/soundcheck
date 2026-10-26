// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package flow

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// errorForm is a contact form whose script shows errors in one of the
// ways a site does. %s is the form's attributes, %s the script.
const errorForm = `<!doctype html><html lang="en"><body>
<div id="summary" tabindex="-1"></div>
<form id="contact" %s action="/sent/" method="post">
<label for="email">Email</label> <input id="email" name="email" type="email" required>
<span id="email-err" class="hint"></span>
<label for="msg">Message</label> <textarea id="msg" name="msg"></textarea>
<button id="send">Send</button>
</form>
<p id="status" role="status"></p>
<script>
const form = document.getElementById('contact');
const fail = (text) => {
  const e = document.getElementById('email-err');
  e.textContent = text;
  e.className = 'error';
  e.style.color = 'red';
};
%s
</script>
</body></html>`

// The ways the fixture form can answer a submit.
const (
	// Red text next to the field, tied to nothing; focus stays on Send.
	redOnly = `form.addEventListener('submit', ev => { ev.preventDefault(); fail('Enter your email'); });`
	// Red text after a server-side check the field passed natively:
	// nothing is invalid, only the message shows.
	redNoField = `form.addEventListener('submit', ev => { ev.preventDefault(); fail('That email is taken'); });`
	// The same error, tied to the field with aria-describedby.
	linked = `form.addEventListener('submit', ev => {
  ev.preventDefault();
  fail('Enter your email');
  const f = document.getElementById('email');
  f.setAttribute('aria-invalid', 'true');
  f.setAttribute('aria-describedby', 'email-err');
});`
	// An error summary that takes focus.
	summary = `form.addEventListener('submit', ev => {
  ev.preventDefault();
  const s = document.getElementById('summary');
  s.innerHTML = '<p class="error">There is a problem: enter your email</p>';
  s.focus();
});`
	// The message is sent: the form is reset (its required field is
	// invalid again) and a status message says so.
	reset = `form.addEventListener('submit', ev => {
  ev.preventDefault();
  form.reset();
  document.getElementById('status').textContent = 'Thanks, your message is on its way';
});`
)

// fillEmail is a step that fills in a valid email.
const fillEmail = `  - fill: {selector: "#email", value: "${fake:email}"}` + "\n"

// errorFlow submits the contact form by keyboard after filling in fill
// (empty leaves the email empty).
const errorFlow = `name: Contact by keyboard
start: /contact/
keyboard: %s
steps:
%s  - click: "#send"
  - expect: {url: /contact/}
`

func playErrors(t *testing.T, mode, attrs, script, fill string) Result {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `<html><body>Home</body></html>`) })
	mux.HandleFunc("/contact/", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintf(w, errorForm, attrs, script) })
	mux.HandleFunc("/sent/", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `<html><body>Sent</body></html>`) })
	srv := listen(t, mux)
	f, err := Parse([]byte(fmt.Sprintf(errorFlow, mode, fill)))
	if err != nil {
		t.Fatal(err)
	}
	page := isolatedTab(t, srv.URL+"/")
	return Play(context.Background(), page, f, keyOptions(srv.URL))
}

// Errors shown in red text only, with focus left on Send, aren't tied to
// their field: an invalid field, or with none marked the message itself.
// A dry flow reads them too: nothing was held, the page checked itself.
func TestErrorNotLinked(t *testing.T) {
	needChrome(t)
	for _, tc := range []struct {
		name, mode, script, fill string
	}{
		{"invalid field, red text", "send", redOnly, ""},
		{"dry", "true", redOnly, ""},
		{"valid field, red text", "send", redNoField, fillEmail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := playErrors(t, tc.mode, "novalidate", tc.script, tc.fill)
			if len(res.Failures) != 0 {
				t.Fatalf("failures:\n%s", describe(res))
			}
			step := 1
			if tc.fill != "" {
				step = 2
			}
			want := Unlinked{Step: step, Form: "#contact", Fields: 1, Focus: "#send"}
			if len(res.Unlinked) != 1 || res.Unlinked[0] != want {
				t.Fatalf("unlinked: %+v, want %+v\ntrace:\n%s", res.Unlinked, want, strings.Join(res.Trace, "\n"))
			}
		})
	}
}

// Native validation (focus moves to the first invalid field), an error
// summary that takes focus, errors tied with aria-describedby and a form
// that sent and reset itself all pass.
func TestErrorLinked(t *testing.T) {
	needChrome(t)
	for _, tc := range []struct {
		name, attrs, script, fill string
	}{
		{"native required", "", "", ""},
		{"summary takes focus", "novalidate", summary, ""},
		{"aria-describedby", "novalidate", linked, ""},
		{"sent and reset", "novalidate", reset, fillEmail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := playErrors(t, "send", tc.attrs, tc.script, tc.fill)
			if len(res.Failures) != 0 || len(res.Unlinked) != 0 {
				t.Fatalf("failures:\n%s\nunlinked: %+v\ntrace:\n%s", describe(res), res.Unlinked, strings.Join(res.Trace, "\n"))
			}
		})
	}
}
