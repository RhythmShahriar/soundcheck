// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cache

import (
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"
)

var date = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func hdr(kv ...string) http.Header {
	h := http.Header{}
	h.Set("Date", date.Format(http.TimeFormat))
	for i := 0; i+1 < len(kv); i += 2 {
		h.Add(kv[i], kv[i+1])
	}
	return h
}

func TestRead(t *testing.T) {
	rocketAt := strconv.FormatInt(date.Add(-time.Hour).Unix(), 10)
	cases := []struct {
		name   string
		h      http.Header
		body   string
		status Status
		layer  string
		layers []string
		age    int
	}{
		{"cloudflare hit", hdr("cf-cache-status", "HIT", "age", "120", "cf-ray", "8a-FRA"), "", Hit, "Cloudflare", []string{"Cloudflare"}, 120},
		{"cloudflare dynamic", hdr("cf-cache-status", "DYNAMIC", "cf-ray", "8a-FRA"), "", Dynamic, "Cloudflare", nil, -1},
		{"cloudflare expired", hdr("cf-cache-status", "EXPIRED"), "", Expired, "Cloudflare", nil, -1},
		{"cloudflare bypass", hdr("cf-cache-status", "BYPASS"), "", Bypass, "Cloudflare", nil, -1},
		{"cloudflare stale", hdr("cf-cache-status", "STALE", "age", "9000"), "", Stale, "Cloudflare", nil, 9000},
		{"cloudflare updating", hdr("cf-cache-status", "UPDATING"), "", Stale, "Cloudflare", nil, -1},
		{"cloudflare revalidated", hdr("cf-cache-status", "REVALIDATED"), "", Hit, "Cloudflare", nil, -1},
		{"cloudflare ray only", hdr("cf-ray", "8a-FRA", "server", "cloudflare"), "", Unknown, "Cloudflare", []string{"Cloudflare"}, -1},
		{"cloudfront hit", hdr("x-cache", "Hit from cloudfront", "x-amz-cf-id", "abc"), "", Hit, "CloudFront", nil, -1},
		{"cloudfront refresh hit", hdr("x-cache", "RefreshHit from cloudfront"), "", Hit, "CloudFront", nil, -1},
		{"cloudfront miss", hdr("x-cache", "Miss from cloudfront"), "", Miss, "CloudFront", nil, -1},
		{"fastly shield hit", hdr("x-cache", "MISS, HIT", "x-served-by", "cache-fra-1-FRA, cache-lhr-2-LHR"), "", Hit, "Fastly", []string{"Fastly"}, -1},
		{"fastly miss", hdr("x-cache", "MISS, MISS", "x-served-by", "cache-fra-1-FRA"), "", Miss, "Fastly", nil, -1},
		{"varnish x-cache", hdr("x-cache", "HIT", "x-varnish", "32770 32768"), "", Hit, "Varnish", []string{"Varnish"}, -1},
		{"varnish pass", hdr("x-cache", "PASS", "via", "1.1 varnish (Varnish/7.4)"), "", Bypass, "Varnish", nil, -1},
		{"varnish two ids", hdr("x-varnish", "32770 32768"), "", Hit, "Varnish", nil, -1},
		{"varnish one id", hdr("x-varnish", "32770"), "", Miss, "Varnish", nil, -1},
		{"varnish via only", hdr("via", "1.1 varnish (Varnish/7.4)"), "", Unknown, "Varnish", nil, -1},
		{"squid", hdr("x-cache", "TCP_MEM_HIT"), "", Hit, "proxy", nil, -1},
		{"litespeed hit", hdr("x-litespeed-cache", "hit"), "", Hit, "LiteSpeed", nil, -1},
		{"litespeed private hit", hdr("x-litespeed-cache", "hit,private"), "", Hit, "LiteSpeed", nil, -1},
		{"litespeed miss", hdr("x-litespeed-cache", "miss", "server", "LiteSpeed"), "", Miss, "LiteSpeed", []string{"LiteSpeed"}, -1},
		{"litespeed no-cache", hdr("x-litespeed-cache-control", "no-cache"), "", Bypass, "LiteSpeed", nil, -1},
		{"nginx proxy", hdr("x-proxy-cache", "HIT"), "", Hit, "nginx", nil, -1},
		{"nginx cache", hdr("x-nginx-cache", "MISS"), "", Miss, "nginx", nil, -1},
		{"fastcgi bypass", hdr("x-fastcgi-cache", "BYPASS"), "", Bypass, "nginx", nil, -1},
		{"cache-status expired", hdr("x-cache-status", "EXPIRED"), "", Expired, "nginx", nil, -1},
		{"cache-status stale", hdr("x-cache-status", "STALE"), "", Stale, "nginx", nil, -1},
		{"kinsta", hdr("x-kinsta-cache", "HIT"), "", Hit, "Kinsta", nil, -1},
		{"sucuri", hdr("x-sucuri-cache", "MISS"), "", Miss, "Sucuri", nil, -1},
		{"wp engine", hdr("x-wpe-cache", "HIT"), "", Hit, "WP Engine", nil, -1},
		{"wp super cache header", hdr("wp-super-cache", "Served supercache file from PHP"), "", Hit, "WP Super Cache", nil, -1},
		{"x-wp-super-cache", hdr("x-wp-super-cache", "HIT"), "", Hit, "WP Super Cache", nil, -1},
		{"age alone", hdr("age", "300"), "", Hit, "proxy", []string{"proxy"}, 300},
		{"age zero", hdr("age", "0"), "", Unknown, "", nil, 0},
		{"nothing", hdr("cache-control", "max-age=0"), "", Unknown, "", []string{}, -1},
		{"cdn dynamic, server hit", hdr("cf-cache-status", "DYNAMIC", "x-litespeed-cache", "hit"), "", Hit, "LiteSpeed", []string{"Cloudflare", "LiteSpeed"}, -1},
		{"cdn miss, varnish hit with age", hdr("cf-cache-status", "MISS", "x-varnish", "5 3", "age", "40"), "", Hit, "Varnish", []string{"Cloudflare", "Varnish"}, 40},
		{"miss beats age", hdr("x-proxy-cache", "MISS", "age", "50"), "", Miss, "nginx", nil, 50},
		{"wp rocket", hdr(), "<html>…</html>\n<!-- This website is like a Rocket, isn't it? Performance optimized by WP Rocket. Learn more: https://wp-rocket.me - Debug: cached@" + rocketAt + " -->", Hit, "WP Rocket", []string{"WP Rocket"}, 3600},
		{"wp rocket just generated", hdr(), "<!-- Performance optimized by WP Rocket. - Debug: cached@" + strconv.FormatInt(date.Unix(), 10) + " -->", Unknown, "WP Rocket", nil, 0},
		{"cache enabler", hdr(), "<!-- Cache Enabler by KeyCDN @ Sat, 24 Oct 2026 10:00:00 GMT (https-index.html) -->", Hit, "Cache Enabler", nil, 7200},
		{"cloudflare hit over rocket", hdr("cf-cache-status", "HIT", "age", "60"), "<!-- Performance optimized by WP Rocket. Debug: cached@" + rocketAt + " -->", Hit, "Cloudflare", []string{"Cloudflare", "WP Rocket"}, 3600},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Read(c.h, c.body)
			if r.Status != c.status || r.Layer != c.layer || r.Age != c.age {
				t.Errorf("got %s/%q age %d, want %s/%q age %d (%+v)", r.Status, r.Layer, r.Age, c.status, c.layer, c.age, r.Signals)
			}
			if c.layers != nil && !slices.Equal(r.Layers, c.layers) && (len(c.layers) > 0 || len(r.Layers) > 0) {
				t.Errorf("layers = %v, want %v", r.Layers, c.layers)
			}
		})
	}
}

// Plugins that stamp local time only count once the site's time zone is
// known.
func TestLocalStamps(t *testing.T) {
	cases := []struct {
		name, body, layer string
	}{
		{"wp super cache", "<!-- Dynamic page generated in 0.4 seconds. -->\n<!-- Cached page generated by WP-Super-Cache on 2026-10-24 12:00:00 -->", "WP Super Cache"},
		{"litespeed", "<!-- Page optimized by LiteSpeed Cache @2026-10-24 12:00:00 -->", "LiteSpeed"},
		{"litespeed cached", "<!-- Page cached by LiteSpeed Cache 6.5 on 2026-10-24 12:00:00 -->", "LiteSpeed"},
		{"w3 total cache", "<!-- Performance optimized by W3 Total Cache. Page Caching using Disk: Enhanced\n\nServed from: shop.test @ 2026-10-24 12:00:00 by W3 Total Cache -->", "W3 Total Cache"},
		{"wp fastest cache", "<!-- WP Fastest Cache file was created in 0.31 seconds, on 24-10-26 @ 12:00 pm -->", "WP Fastest Cache"},
		{"hummingbird", "<!-- Hummingbird cache file was created in 0.2 seconds, on 24-10-26 @ 12:00 -->", "Hummingbird"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := hdr()
			r := Read(h, "<html><body>Hi</body></html>"+c.body)
			if r.Status != Unknown || !r.Local || r.Layer != c.layer || r.Age != -1 {
				t.Fatalf("before the time zone is known: %+v", r)
			}
			// The site is at UTC+3, so 12:00 local is 09:00 UTC: three hours old.
			r.Localize(h, 3*time.Hour)
			if r.Status != Hit || r.Layer != c.layer || r.Age != 3*3600 {
				t.Errorf("after: %s %q age %d", r.Status, r.Layer, r.Age)
			}
		})
	}
}

func TestControl(t *testing.T) {
	future := date.Add(time.Hour).Format(http.TimeFormat)
	cases := []struct {
		h      http.Header
		shared bool
	}{
		{hdr("cache-control", "public, max-age=600"), true},
		{hdr("cache-control", "s-maxage=3600"), true},
		{hdr("cache-control", "max-age=60"), true},
		{hdr("cache-control", "private, max-age=600"), false},
		{hdr("cache-control", "no-store"), false},
		{hdr("cache-control", "no-cache, must-revalidate, max-age=0"), false},
		{hdr("expires", future), true},
		{hdr("expires", "Wed, 11 Jan 1984 05:00:00 GMT"), false},
		{hdr(), false},
	}
	for _, c := range cases {
		if got := parseControl(c.h).Shared(); got != c.shared {
			t.Errorf("%v: shared = %v", c.h, got)
		}
	}
}

func TestToken(t *testing.T) {
	for v, want := range map[string]Status{
		"HIT": Hit, "hit": Hit, "MISS": Miss, "BYPASS": Bypass, "EXPIRED": Expired, "STALE": Stale, "UPDATING": Stale,
		"DYNAMIC": Dynamic, "NONE": Unknown, "HIT-FOR-PASS": Bypass, "MISS, MISS, HIT": Hit, "TCP_REFRESH_HIT": Hit,
		"Error from cloudfront": Unknown, "hit,litemage": Hit, "": Unknown,
	} {
		if got := token(v); got != want {
			t.Errorf("token(%q) = %s, want %s", v, got, want)
		}
	}
}
