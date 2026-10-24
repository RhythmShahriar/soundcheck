// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package config

import (
	"strings"
	"testing"
)

func TestIsProduction(t *testing.T) {
	for _, tc := range []struct {
		entry, url string
		want       bool
	}{
		// A bare host covers both schemes.
		{"example.com", "https://example.com/contact/", true},
		{"example.com", "http://example.com/contact/", true},
		{"yoursite.com", "https://yoursite.com/contact/", true},
		// www and the apex are one site, both ways.
		{"https://example.com", "https://www.example.com/contact/", true},
		{"https://www.example.com", "https://example.com/contact/", true},
		{"www.example.com", "http://www.example.com/", true},
		// The scheme is ignored.
		{"https://example.com", "http://example.com/contact/", true},
		{"http://yoursite.com", "https://yoursite.com/contact/", true},
		// Default ports don't matter, on either side.
		{"https://example.com", "https://example.com:443/contact/", true},
		{"https://example.com", "http://example.com:80/", true},
		{"https://example.com:443", "https://example.com/", true},
		{"example.com:80", "https://example.com/", true},
		// A trailing dot, on either side.
		{"https://example.com", "https://example.com./contact/", true},
		{"https://example.com.", "https://example.com/contact/", true},
		// An IDN matches its punycode, both ways.
		{"https://bücher.de", "https://xn--bcher-kva.de/kontakt/", true},
		{"xn--bcher-kva.de", "https://bücher.de/kontakt/", true},
		// Case.
		{"https://shop.example/", "https://SHOP.example/contact/", true},
		{"EXAMPLE.COM/Shop", "https://example.com/shop/cart", true},
		// *. covers subdomains and the apex.
		{"*.example.com", "https://shop.example.com/checkout/", true},
		{"*.example.com", "https://a.b.example.com/", true},
		{"*.example.com", "https://example.com/", true},
		// A path prefix on a segment boundary.
		{"example.com/shop", "https://example.com/shop", true},
		{"example.com/shop/", "https://example.com/shop/cart", true},
		{"https://example.com/", "https://example.com/anything", true},
		// A non-default port is matched exactly.
		{"127.0.0.1:19140", "http://127.0.0.1:19140/contact/", true},
		{"127.0.0.1", "http://127.0.0.1:19140/contact/", true},

		{"https://example.com", "https://shop.example.com/checkout/", false}, // a sibling, without *.
		{"shop.example.com", "https://blog.example.com/", false},
		{"*.example.com", "https://example.com.evil.test/", false},
		{"*.example.com", "https://notexample.com/", false},
		{"example.com/shop", "https://example.com/shopping", false},
		{"example.com/shop", "https://example.com/", false},
		{"https://shop.example/", "https://shop.example.staging.test/", false},
		{"127.0.0.1:19140", "http://127.0.0.1:19141/", false},
		{"example.com", "https://example.org/", false},
		{"example.com", "not a url", false},
	} {
		c := Default()
		c.Production = []string{tc.entry}
		if err := c.Validate(); err != nil {
			t.Errorf("%q: %v", tc.entry, err)
			continue
		}
		if got := c.IsProduction(tc.url); got != tc.want {
			t.Errorf("production %q, IsProduction(%q) = %v, want %v", tc.entry, tc.url, got, tc.want)
		}
	}
}

func TestProductionValidate(t *testing.T) {
	for _, bad := range []string{"://bad", "http://", "", "ftp://example.com", "*.", "a*b.example.com", "https://*.*.example.com"} {
		c := Default()
		c.Production = []string{"example.com", bad}
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), "production: \""+bad+"\"") {
			t.Errorf("%q: err = %v, want it named", bad, err)
		}
	}
	if _, err := Load(write(t, "production: ['://bad']\n")); err == nil || !strings.Contains(err.Error(), "://bad") {
		t.Errorf("Load: %v", err)
	}
	c, err := Load(write(t, "production: [Example.com., 'https://www.shop.test:8443/Checkout/', '*.bücher.de']\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c.ProductionSites(), ", "); got != "example.com, shop.test:8443/checkout, *.xn--bcher-kva.de" {
		t.Errorf("ProductionSites = %q", got)
	}
	if len(Default().ProductionSites()) != 0 {
		t.Error("no production sites by default")
	}
}

// Configs built in code, or changed after Validate, are parsed when asked.
func TestProductionInCode(t *testing.T) {
	c := Default()
	c.Production = []string{"example.com"}
	if !c.IsProduction("https://www.example.com/") {
		t.Error("not validated")
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Production = []string{"other.test"}
	if c.IsProduction("https://example.com/") || !c.IsProduction("https://other.test/") {
		t.Error("changed after Validate")
	}
	c.Production = []string{"://bad", "good.test"}
	if !c.IsProduction("http://good.test/") {
		t.Error("a bad entry must not hide the good ones")
	}
}
