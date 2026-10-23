package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil || c.Pages.Limit != 300 || !c.Has("visual") || c.Level("submit") {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	c, err = Load(write(t, "pages:\n  limit: 50\nvisual:\n  ignore: ['.slider']\nproduction: ['https://shop.example/']\ninteractions:\n  levels: [inspect, click, submit]\n"))
	if err != nil || c.Pages.Limit != 50 || c.Pages.PerGroup != 3 || c.Visual.Ignore[0] != ".slider" || !c.Level("submit") {
		t.Fatalf("override: %+v %v", c, err)
	}
	if !c.IsProduction("https://SHOP.example/contact/") || c.IsProduction("https://shop.example.staging.test/") {
		t.Error("IsProduction")
	}
	if _, err := Load(write(t, "")); err != nil {
		t.Errorf("empty file: %v", err)
	}
	for body, want := range map[string]string{
		"pagez:\n  limit: 5\n":           "field pagez not found",
		"viewports: [tablet]\n":          "tablet",
		"modules: [health, magic]\n":     "magic",
		"interactions:\n  levels: [x]\n": "interactions.levels",
		"tabs: 99\n":                     "tabs",
	} {
		if _, err := Load(write(t, body)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", body, err, want)
		}
	}
}
