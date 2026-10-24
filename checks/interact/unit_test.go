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
		"Like", "btn | like-button", "Upvote", "Vote", "Follow us", "Subscribe", "Send", "Request a callback",
		"Book now", "Apply", "Publish", "Approve", "Approved", "Liked", "Followed",
	} {
		if !isDestructive(words) {
			t.Errorf("%q should be destructive", words)
		}
	}
	for _, words := range []string{"Menu | menu-toggle", "Show more", "Next slide", "Dropdown | dropdown-toggle", "Load more posts",
		"Booking policy", "likely", "Sender details", "Bookmarks", "Applications"} {
		if isDestructive(words) {
			t.Errorf("%q isn't destructive", words)
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
