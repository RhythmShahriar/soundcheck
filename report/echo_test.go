package report

import (
	"bytes"
	"strings"
	"testing"

	echomodel "github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/soundcheck/model"
)

func echoRun() *model.Run {
	r := sampleRun()
	r.Options.Echo, r.Options.EchoMode, r.Options.EchoURL = true, "seal", "http://127.0.0.1:8025"
	r.Pages[0].Echo = []model.EchoAction{{Action: "form:/#form", Label: "Contact Form 7 #7", Kind: "form", Page: "/", Messages: []echomodel.Message{
		{Channel: echomodel.Email, To: []string{"admin@brew.test"}, Subject: "New enquiry <b>now</b>", Status: echomodel.Faked},
		{Channel: echomodel.Webhook, URL: "https://hooks.zapier.com/hooks/catch/1/", Status: echomodel.Failed},
	}}}
	r.Echo = []model.EchoAction{{Action: "flow:Poll", Label: "Poll", Kind: "flow", Page: "/shop/"}}
	return r
}

func TestEchoInReports(t *testing.T) {
	r := echoRun()
	const summary = "Echo: 1 email, 1 webhook from 2 forms and flows"
	if got := (Result{Run: r}).stats(); !strings.Contains(got, summary) {
		t.Errorf("stats: %q", got)
	}
	var md, html, term bytes.Buffer
	if err := Markdown(&md, Result{Run: r}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md.String(), summary) {
		t.Errorf("markdown lacks the counts:\n%s", md.String())
	}
	NewPretty(&term, &term, PrettyOptions{Plain: true}).Summary(Result{Run: r}, "")
	if !strings.Contains(term.String(), summary) {
		t.Errorf("terminal lacks the counts:\n%s", term.String())
	}
	if err := HTML(&html, Result{Run: r}); err != nil {
		t.Fatal(err)
	}
	out := html.String()
	for _, want := range []string{"Messages sent · " + summary, "<td>Contact Form 7 #7</td>", "admin@brew.test", "New enquiry &lt;b&gt;now&lt;/b&gt;",
		"webhook (failed)", "hooks.zapier.com/hooks/catch/1/", "<td>Poll</td>", "no message went out", `href="http://127.0.0.1:8025"`} {
		if !strings.Contains(out, want) {
			t.Errorf("HTML lacks %q", want)
		}
	}

	// Without Echo nothing about it shows.
	html.Reset()
	if err := HTML(&html, Result{Run: sampleRun()}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html.String(), "Messages sent") || strings.Contains((Result{Run: sampleRun()}).stats(), "Echo") {
		t.Error("Echo shown for a run without it")
	}
	r.Pages[0].Echo, r.Echo = nil, nil
	if got := (Result{Run: r}).stats(); !strings.Contains(got, "Echo: no form submits or flows") {
		t.Errorf("stats without actions: %q", got)
	}
}
