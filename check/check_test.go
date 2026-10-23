package check

import (
	"net/url"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
)

func TestSiteRoot(t *testing.T) {
	base, _ := url.Parse("https://shop.test/s19-account/")
	run := &model.Run{Pages: []model.Page{{Resources: []model.Resource{
		{URL: "https://cdn.other.test/wp-content/a.js"},
		{URL: "https://shop.test/blog/wp-includes/js/jquery.js"},
	}}}}
	if got := (&Context{Base: base, Run: run}).SiteRoot().String(); got != "https://shop.test/blog/" {
		t.Errorf("WordPress in /blog/: got %s", got)
	}
	if got := (&Context{Base: base, Run: &model.Run{}}).SiteRoot().String(); got != "https://shop.test/" {
		t.Errorf("no assets: got %s", got)
	}
}
