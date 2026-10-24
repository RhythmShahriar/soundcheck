// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package check

import (
	"net/url"
	"testing"
)

func TestSameSite(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"https://example.com/", "https://example.com/about/", true},
		{"https://example.com/", "https://WWW.Example.com/", true},
		{"https://www.example.com/", "https://example.com/", true},
		{"http://example.com/", "https://example.com/", true},
		{"http://example.com:80/", "https://www.example.com/", true},
		{"https://example.com:443/", "http://example.com/", true},
		{"http://127.0.0.1:20280/", "http://127.0.0.1:20280/a", true},
		{"http://127.0.0.1:20280/", "http://127.0.0.1:20281/", false},
		{"http://127.0.0.1:20280/", "http://127.0.0.1/", false},
		{"http://example.com:8080/", "https://example.com/", false},
		{"http://example.com:443/", "http://example.com/", false},
		{"https://example.com/", "https://shop.example.com/", false},
		{"https://example.com/", "https://example.org/", false},
	} {
		a, _ := url.Parse(tc.a)
		b, _ := url.Parse(tc.b)
		if got := SameSite(a, b); got != tc.want {
			t.Errorf("SameSite(%s, %s) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
		if got := SameSite(b, a); got != tc.want {
			t.Errorf("SameSite(%s, %s) = %v, want %v (swapped)", tc.b, tc.a, got, tc.want)
		}
	}
	if SameSite(nil, &url.URL{Host: "example.com"}) {
		t.Error("nil is no site")
	}
}
