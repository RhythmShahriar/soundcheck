// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package interact

import (
	"net/url"
	"testing"
)

func TestDestructive(t *testing.T) {
	for _, words := range []string{
		"Delete account", "Remove item", "btn | unsubscribe-link", "Log out", "logout", "Sign out", "Cancel order",
		"Empty cart", "empty_cart", "Deactivate", "Move to Trash", "Reset password", "/?action=trash&post=4",
		"/wp-admin/post.php", "x | /cart/?_wpnonce=abc", "Add to cart", "Place order",
	} {
		if !isDestructive(candidate{Words: words}) {
			t.Errorf("%q should be destructive", words)
		}
	}
	for _, says := range []string{
		"Like", "Upvote", "Vote", "Follow us", "Subscribe", "Send", "Request a callback",
		"Book now", "Apply", "Publish", "Approve", "Approved", "Liked", "Followed", " | like",
	} {
		if !isDestructive(candidate{Words: says, Says: says}) {
			t.Errorf("a control that says %q should be destructive", says)
		}
	}
	for _, c := range []candidate{
		{Words: "Menu | menu-toggle", Says: "Menu"}, {Words: "Show more", Says: "Show more"}, {Words: "Next slide"},
		{Words: "Dropdown | dropdown-toggle"}, {Words: "Load more posts"}, {Words: "Booking policy", Says: "Booking policy"},
		{Words: "likely", Says: "likely"}, {Words: "Sender details", Says: "Sender details"}, {Words: "Bookmarks"},
		{Words: "Applications", Says: "Applications"},
		// Action words in an href, class or id are navigation and styling.
		{Words: "Reading list | /books/", Says: "Reading list"}, {Words: "Get a quote | /request-a-quote/", Says: "Get a quote"},
		{Words: "Jobs | /apply/", Says: "Jobs"}, {Words: "Go | btn like-button", Says: "Go"},
	} {
		if isDestructive(c) {
			t.Errorf("%+v isn't destructive", c)
		}
	}
}

func TestEligible(t *testing.T) {
	contact := formInfo{Method: "post", Profile: "cf7", Visible: true, Fields: 3}
	cases := []struct {
		name string
		f    formInfo
		prod bool
		want bool
	}{
		{"plugin form", contact, false, true},
		{"production", contact, true, false},
		{"allowlisted on production", formInfo{Method: "post", Visible: true, Fields: 2, Allowed: true}, true, true},
		{"search", formInfo{Method: "get", Search: true, Visible: true, Fields: 1}, false, false},
		{"get form", formInfo{Method: "get", Visible: true, Fields: 3}, false, false},
		{"login", formInfo{Method: "post", Password: true, Visible: true, Fields: 2}, false, false},
		{"comments", formInfo{Method: "post", Comment: true, Visible: true, Fields: 3}, false, false},
		{"hidden", formInfo{Method: "post", Fields: 3}, false, false},
		{"admin", formInfo{Method: "post", Visible: true, Fields: 3, Action: "https://x.test/wp-admin/admin-post.php"}, false, false},
		{"generic post", formInfo{Method: "post", Visible: true, Fields: 3}, false, true},
		{"posts here", formInfo{Method: "post", Visible: true, Fields: 3, Action: "https://www.x.test/join"}, false, true},
		{"posts elsewhere", formInfo{Method: "post", Visible: true, Fields: 1, Action: "https://list.mail.test/subscribe/post"}, false, false},
		{"posts to another port", formInfo{Method: "post", Visible: true, Fields: 1, Action: "https://x.test:8443/join"}, false, false},
		{"allowlisted elsewhere", formInfo{Method: "post", Visible: true, Fields: 1, Action: "https://list.mail.test/", Allowed: true}, false, true},
	}
	base, _ := url.Parse("https://x.test/contact/")
	for _, c := range cases {
		if got, why := eligible(c.f, base, c.prod); got != c.want {
			t.Errorf("%s: got %v (%s)", c.name, got, why)
		}
	}
	if _, why := eligible(cases[len(cases)-3].f, base, false); why != "posts to list.mail.test" {
		t.Errorf("reason: %q", why)
	}
}
