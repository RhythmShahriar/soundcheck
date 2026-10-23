package health

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

func ctx() *check.Context {
	u, _ := url.Parse("https://shop.example/")
	return &check.Context{Base: u, Run: &model.Run{}}
}

func titles(p *model.Page) map[string]model.Issue {
	m := map[string]model.Issue{}
	for _, i := range p.Issues {
		m[i.Title] = i
	}
	return m
}

func TestPHPErrors(t *testing.T) {
	p := &model.Page{Status: 200, TextLength: 900, Text: `Welcome
Warning: Undefined array key "id" in /var/www/html/wp-content/themes/shopy/functions.php on line 212
Deprecated: Creation of dynamic property Foo::$bar is deprecated in /var/www/html/wp-content/plugins/legacy/legacy.php on line 41
Fatal error: Uncaught Error: Call to undefined function wc_get_x() in /home/site/public_html/wp-content/plugins/shop-ext/ext.php:88 Stack trace: #0 {main} thrown in /home/site/public_html/wp-content/plugins/shop-ext/ext.php on line 88`}
	Module{}.CheckPage(ctx(), p)
	got := titles(p)
	w := got[`PHP Warning: Undefined array key "id"`]
	if w.Level != model.Warning || w.Source != "themes/shopy/functions.php:212" || w.Key == "" {
		t.Errorf("warning = %+v", w)
	}
	if d := got["PHP Deprecated: Creation of dynamic property Foo::$bar is deprecated"]; d.Level != model.Notice || d.Source != "plugins/legacy/legacy.php:41" {
		t.Errorf("deprecated = %+v", d)
	}
	fatal := false
	for title, i := range got {
		if strings.HasPrefix(title, "PHP Fatal Error: Uncaught Error: Call to undefined function wc_get_x()") && i.Level == model.Error && i.Source == "plugins/shop-ext/ext.php:88" {
			fatal = true
		}
	}
	if !fatal {
		t.Errorf("no fatal error in %v", got)
	}
}

func TestErrorScreens(t *testing.T) {
	cases := map[string]*model.Page{
		"WordPress \"critical error\" screen": {Status: 500, Text: "There has been a critical error on this website. Learn more about troubleshooting WordPress."},
		"Application error page":              {Status: 200, TextLength: 50, Text: "Whoops, looks like something went wrong."},
		"Database error on the page":          {Status: 200, TextLength: 40, Text: "Error establishing a database connection"},
		"Blank page (white screen)":           {Status: 200},
		"Server error 503":                    {Status: 503, Text: "busy"},
		"Page not found (404)":                {Status: 404, Text: "nope"},
	}
	for want, p := range cases {
		Module{}.CheckPage(ctx(), p)
		if _, ok := titles(p)[want]; !ok {
			t.Errorf("%q not found in %v", want, p.Issues)
		}
	}
}

func TestRequestsConsoleAndHygiene(t *testing.T) {
	p := &model.Page{Status: 200, Title: "", TextLength: 500, FinalURL: "https://shop.example/cart/", Robots: "noindex, follow",
		Console: []string{"Uncaught ReferenceError: jQuery is not defined (https://shop.example/wp-content/plugins/x/app.js?ver=1.2:14)", "Order 12345 failed"},
		Failed: []model.Request{
			{URL: "https://shop.example/wp-admin/admin-ajax.php", Method: "POST", Type: "XHR", Status: 500},
			{URL: "https://shop.example/wp-content/uploads/hero.jpg", Type: "Image", Status: 404},
			{URL: "https://cdn.example/lib.js", Type: "Script", Error: "net::ERR_NAME_NOT_RESOLVED"},
			{URL: "https://shop.example/favicon.ico", Type: "Other", Status: 404},
		},
		Resources: []model.Resource{{URL: "http://shop.example/logo.png"}},
		Shots:     map[string]model.Shot{"mobile": {Width: 980}},
	}
	Module{}.CheckPage(ctx(), p)
	got := titles(p)
	for _, want := range []string{
		"JS error: Uncaught ReferenceError: jQuery is not defined",
		"JS error: Order N failed",
		"POST /wp-admin/admin-ajax.php returned 500",
		"Broken image: /wp-content/uploads/hero.jpg (404)",
		"Request failed (ERR_NAME_NOT_RESOLVED): https://cdn.example/lib.js",
		"No favicon",
		"Insecure (http://) resource on an https page",
		"Page has no <title>",
		"Page is set to noindex",
		"Page renders 980 px wide on a phone",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing %q; got %v", want, keys(got))
		}
	}
	if js := got["JS error: Uncaught ReferenceError: jQuery is not defined"]; js.Source != "https://shop.example/wp-content/plugins/x/app.js:14" {
		t.Errorf("js source = %q", js.Source)
	}
	if ajax := got["POST /wp-admin/admin-ajax.php returned 500"]; ajax.Level != model.Error {
		t.Errorf("a 500 from admin-ajax is an error: %+v", ajax)
	}
}

func TestCleanPageHasNoIssues(t *testing.T) {
	p := &model.Page{Status: 200, Title: "Home", TextLength: 2000, Text: "Welcome to our shop. Warning: prices may change.",
		FinalURL: "https://shop.example/", Shots: map[string]model.Shot{"mobile": {Width: 390}}}
	Module{}.CheckPage(ctx(), p)
	if len(p.Issues) != 0 {
		t.Errorf("clean page got %v", p.Issues)
	}
}

func TestCompare(t *testing.T) {
	before := &model.Run{Pages: []model.Page{{Path: "/a", Status: 200, Title: "A", TextLength: 1000, FinalURL: "https://x/a"}}}
	after := &model.Run{Pages: []model.Page{{Path: "/a", Status: 200, Title: "A new", TextLength: 100, FinalURL: "https://x/login"}}}
	changes := Module{}.Compare(context.Background(), &check.CompareContext{Before: before, After: after})
	var got []string
	for _, c := range changes {
		got = append(got, c.Check)
	}
	if len(changes) != 3 {
		t.Errorf("changes = %v", got)
	}
}

func keys(m map[string]model.Issue) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestInlineScriptErrorsGroup(t *testing.T) {
	cc := &check.Context{}
	var keys []string
	for i, path := range []string{"/", "/about/"} {
		u := "http://x.test" + path
		p := &model.Page{URL: u, FinalURL: u, Path: path, Status: 200, TextLength: 100,
			Console: []string{fmt.Sprintf("Uncaught TypeError: Cannot read properties of undefined (reading 'init') (%s:%d)", u, 200+i*80)}}
		Module{}.CheckPage(cc, p)
		for _, is := range p.Issues {
			if is.Check == "health/js-error" {
				keys = append(keys, is.Key)
				if is.Source != "inline script" {
					t.Errorf("source = %q", is.Source)
				}
			}
		}
	}
	if len(keys) != 2 || keys[0] != keys[1] {
		t.Errorf("the same inline error on two pages must share a key: %v", keys)
	}
}

func TestWhiteScreenIgnoresFavicon(t *testing.T) {
	p := &model.Page{URL: "http://x.test/a/", Path: "/a/", Status: 200, Resources: []model.Resource{
		{URL: "http://x.test/wp-includes/images/w-logo-blue-white-bg.png", Type: "Image", Status: 200},
	}}
	Module{}.CheckPage(&check.Context{}, p)
	if len(p.Issues) == 0 || p.Issues[0].Check != "health/white-screen" {
		t.Errorf("a blank WordPress page must be a white screen: %+v", p.Issues)
	}
	p = &model.Page{URL: "http://x.test/g/", Path: "/g/", Status: 200, Resources: []model.Resource{{URL: "http://x.test/photo.jpg", Type: "Image", Status: 200}}}
	Module{}.CheckPage(&check.Context{}, p)
	for _, i := range p.Issues {
		if i.Check == "health/white-screen" {
			t.Error("a page showing an image isn't blank")
		}
	}
}
