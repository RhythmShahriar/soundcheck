package echo

import (
	"context"
	"testing"

	echomodel "github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

func email(to, subject string) echomodel.Message {
	return echomodel.Message{Channel: echomodel.Email, To: []string{to}, Subject: subject, Text: "Hello", Status: echomodel.Faked}
}

func form(page, label string, msgs ...echomodel.Message) model.EchoAction {
	return model.EchoAction{Action: "form:" + page + "#" + label, Label: label, Kind: "form", Page: page, Messages: msgs}
}

func pageWith(path string, acts ...model.EchoAction) model.Page {
	return model.Page{Path: path, Echo: acts}
}

func TestCompare(t *testing.T) {
	admin := email("admin@shop.test", "New enquiry")
	thanks := email("soundcheck-test+abc123@example.com", "Thanks")
	thanksNow := email("soundcheck-test+def456@example.com", "Thanks")
	before := &model.Run{Pages: []model.Page{
		pageWith("/contact/", form("/contact/", "Contact", admin, thanks)),
		pageWith("/a/", form("/a/", "Newsletter", email("list@shop.test", "Signup"))),
		pageWith("/b/", form("/b/", "Newsletter", email("list@shop.test", "Signup")), form("/b/", "Quote", admin)),
		pageWith("/gone/", form("/gone/", "Contact", admin)),
	}, Echo: []model.EchoAction{{Action: "flow:Checkout", Label: "Checkout", Kind: "flow", Page: "/shop/",
		Messages: []echomodel.Message{{Channel: echomodel.Webhook, URL: "https://hooks.zapier.com/hooks/catch/1/", Body: `{"order":{"id":1,"total":5}}`}}}}}
	after := &model.Run{Pages: []model.Page{
		pageWith("/contact/", form("/contact/", "Contact", thanksNow)),
		pageWith("/a/", form("/a/", "Newsletter")),
		pageWith("/b/", form("/b/", "Newsletter"), form("/b/", "Quote", email("admin@shop.test", "Quote request"))),
		pageWith("/new/", form("/new/", "Contact")),
	}, Echo: []model.EchoAction{{Action: "flow:Checkout", Label: "Checkout", Kind: "flow", Page: "/shop/",
		Messages: []echomodel.Message{{Channel: echomodel.Webhook, URL: "https://hooks.zapier.com/hooks/catch/1/", Body: `{"order":{"id":2}}`}}}}}
	got := Module{}.Compare(context.Background(), &check.CompareContext{Before: before, After: after})
	byTitle := map[string]model.Change{}
	for _, c := range got {
		byTitle[c.Title] = c
	}
	if len(byTitle) != len(got) {
		t.Errorf("duplicate titles: %+v", got)
	}
	for title, want := range map[string]struct {
		class, check, source string
		pages                []string
	}{
		"Email to admin@shop.test is no longer sent":            {model.Broke, "echo/message/missing", "Contact", []string{"/contact/"}},
		"Email to list@shop.test is no longer sent":             {model.Broke, "echo/message/missing", "Newsletter", []string{"/a/"}},
		"Newsletter: Email to list@shop.test is no longer sent": {model.Broke, "echo/message/missing", "Newsletter", []string{"/b/"}},
		"Quote: Email to admin@shop.test: subject changed":      {model.Changed, "echo/message/subject", "Quote", []string{"/b/"}},
		"Webhook to hooks.zapier.com: payload lost fields":      {model.Changed, "echo/message/fields-lost", "Checkout", []string{"/shop/"}},
	} {
		c, ok := byTitle[title]
		if !ok {
			t.Errorf("missing %q in %+v", title, got)
			continue
		}
		if c.Class != want.class || c.Check != want.check || c.Source != want.source || len(c.Pages) != len(want.pages) || c.Pages[0] != want.pages[0] {
			t.Errorf("%q: %+v", title, c)
		}
	}
	if c := byTitle["Webhook to hooks.zapier.com: payload lost fields"]; c.Detail != "order.total" {
		t.Errorf("lost fields: %q", c.Detail)
	}
	// The visitor's autoresponder matched across runs despite the new tag.
	for _, c := range got {
		if c.Source == "Contact" && c.Title != "Email to admin@shop.test is no longer sent" {
			t.Errorf("unexpected change for the contact form: %+v", c)
		}
	}
}

func TestCompareGroupsPages(t *testing.T) {
	before := &model.Run{Pages: []model.Page{
		pageWith("/a/", form("/a/", "Newsletter", email("list@shop.test", "Signup"))),
		pageWith("/b/", form("/b/", "Newsletter", email("list@shop.test", "Signup"))),
	}}
	after := &model.Run{Pages: []model.Page{pageWith("/a/", form("/a/", "Newsletter")), pageWith("/b/", form("/b/", "Newsletter"))}}
	got := Module{}.Compare(context.Background(), &check.CompareContext{Before: before, After: after})
	if len(got) != 1 || len(got[0].Pages) != 2 || got[0].Score != 2 {
		t.Errorf("changes: %+v", got)
	}
}

func TestFilterChanges(t *testing.T) {
	in := []model.Change{
		{Class: model.Broke, Check: "echo/message/missing", Title: "Email to admin@shop.test is no longer sent", Source: "Contact", Pages: []string{"/contact/"}},
		{Class: model.New, Check: "echo/no-message", Title: "Form says sent, but no message went out", Source: "Contact", Pages: []string{"/contact/", "/other/"}},
		{Class: model.New, Check: "echo/no-message", Title: "Form says sent, but no message went out", Source: "Poll", Pages: []string{"/contact/"}},
		{Class: model.New, Check: "health/php-error", Title: "PHP Warning", Pages: []string{"/contact/"}},
	}
	out := Module{}.FilterChanges(in)
	if len(out) != 4 || len(out[1].Pages) != 1 || out[1].Pages[0] != "/other/" || out[2].Source != "Poll" {
		t.Errorf("filtered: %+v", out)
	}
	in[1].Pages = []string{"/contact/"}
	if out := (Module{}).FilterChanges(in); len(out) != 3 {
		t.Errorf("the notice explained by BROKE stays: %+v", out)
	}
}
