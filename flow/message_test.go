package flow

import (
	"context"
	"errors"
	"strings"
	"testing"

	echomodel "github.com/RhythmShahriar/echo/model"
)

func TestParseExpectMessage(t *testing.T) {
	f, err := Parse([]byte(`name: Contact
steps:
  - expect: {message: {channel: email, to: "${fake:email}", subject: Thanks}}
  - expect: {message: {channel: webhook, to: hooks.zapier.com, text: order_id, count: 2}}
  - expect: {message: {channel: sms}, absent: true}
  - expect: {message: {}}
`))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range f.Steps {
		got = append(got, s.String())
	}
	want := []string{`expect message email to "${fake:email}" subject "Thanks"`,
		`expect message webhook to "hooks.zapier.com" text "order_id" count 2`, "expect no message sms", "expect message"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("steps:\n got %q\nwant %q", got, want)
	}
	for in, msg := range map[string]string{
		"name: x\nsteps: [{expect: {message: {channel: fax}}}]":                         `channel "fax" isn't one of email`,
		"name: x\nsteps: [{expect: {message: {count: -1}}}]":                            "count can't be negative",
		"name: x\nsteps: [{expect: {message: {count: 2}, absent: true}}]":               "count and absent don't mix",
		"name: x\nsteps: [{expect: {message: {too: a}}}]":                               "field too not found",
		"name: x\nsteps: [{expect: {message: {to: '${fake:nope}'}}}]":                   "unknown ${fake:nope}",
		"name: x\nsteps: [{expect: {message: {channel: email, subject: '${fake:x}'}}}]": "unknown ${fake:x}",
	} {
		if _, err := Parse([]byte(in)); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%q: got %v, want %q", in, err, msg)
		}
	}
	if _, err := Parse([]byte(Example)); err != nil {
		t.Errorf("the example: %v", err)
	}
}

func TestExpectMessage(t *testing.T) {
	fake := Fake{Seed: "run"}
	email, _ := fake.Value("email")
	sent := []echomodel.Message{
		{Channel: echomodel.Email, To: []string{"admin@shop.test"}, Subject: "New enquiry from Soundcheck Test", Text: "Hello"},
		{Channel: echomodel.Email, To: []string{email}, Subject: "Thanks for getting in touch", HTML: "<p>We'll reply soon</p>"},
		{Channel: echomodel.Webhook, URL: "https://hooks.zapier.com/hooks/catch/1/abc", Body: `{"order_id": 7}`},
	}
	r := &runner{ctx: context.Background(), o: Options{Fake: fake,
		Messages: func(context.Context) ([]echomodel.Message, error) { return sent, nil }}}
	for _, tc := range []struct {
		m      Message
		absent bool
		err    string
	}{
		{Message{Channel: "email", To: "${fake:email}", Subject: "thanks"}, false, ""},
		{Message{Channel: "email", To: "ADMIN@", Subject: "New enquiry"}, false, ""},
		{Message{Channel: "email", Text: "reply soon"}, false, ""},
		{Message{Channel: "webhook", To: "zapier.com", Text: "order_id"}, false, ""},
		{Message{Channel: "email", Count: 2}, false, ""},
		{Message{Channel: "email", Count: 3}, false, "only 2 of 3 such messages were sent"},
		{Message{Channel: "sms"}, false, "no such message was sent (Echo saw: email to admin@shop.test"},
		{Message{Channel: "email", To: "billing@"}, false, "no such message"},
		{Message{Channel: "sms"}, true, ""},
		{Message{Channel: "email", To: "admin@"}, true, "the site sent 1 such message"},
		{Message{To: "${fake:bogus}"}, false, "unknown ${fake:bogus}"},
	} {
		err := r.message(tc.m, tc.absent)
		switch {
		case tc.err == "" && err != nil:
			t.Errorf("%+v absent=%v: %v", tc.m, tc.absent, err)
		case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
			t.Errorf("%+v absent=%v: got %v, want %q", tc.m, tc.absent, err, tc.err)
		}
	}

	sent = nil
	if err := r.message(Message{}, false); err == nil || !strings.Contains(err.Error(), "Echo saw nothing from this flow") {
		t.Errorf("nothing sent: %v", err)
	}
	r.o.Messages = func(context.Context) ([]echomodel.Message, error) { return nil, errors.New("echo isn't running") }
	if err := r.message(Message{}, false); err == nil || !strings.Contains(err.Error(), "asking Echo: echo isn't running") {
		t.Errorf("echo down: %v", err)
	}
	r.o.Messages = nil
	if err := r.message(Message{}, false); err == nil || !strings.Contains(err.Error(), "needs Echo") {
		t.Errorf("without echo: %v", err)
	}
}
