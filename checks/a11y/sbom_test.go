// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package a11y

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// cdx is the part of a CycloneDX SBOM the test reads.
type cdx struct {
	BOMFormat  string `json:"bomFormat"`
	Components []struct {
		Name     string `json:"name"`
		Version  string `json:"version"`
		Purl     string `json:"purl"`
		Licenses []struct {
			License struct {
				ID string `json:"id"`
			} `json:"license"`
		} `json:"licenses"`
		Hashes []struct {
			Alg     string `json:"alg"`
			Content string `json:"content"`
		} `json:"hashes"`
	} `json:"components"`
}

// The release's SBOM for the axe-core the binary embeds names the
// vendored file: its version (from the file's own header), license and
// SHA-256. scripts/update-axe.sh keeps them in step.
func TestAxeSBOM(t *testing.T) {
	data, err := os.ReadFile("../../sboms/axe-core.cdx.json")
	if err != nil {
		t.Fatal(err)
	}
	var bom cdx
	if err := json.Unmarshal(data, &bom); err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`^/\*! axe v(\S+)\n`).FindStringSubmatch(axeSource)
	if m == nil {
		t.Fatalf("vendor/axe.min.js has no version header: %.40q", axeSource)
	}
	vendored := m[1]
	if bom.BOMFormat != "CycloneDX" || len(bom.Components) != 1 {
		t.Fatalf("SBOM = %+v", bom)
	}
	c := bom.Components[0]
	if c.Name != "axe-core" || c.Version != vendored || c.Purl != "pkg:npm/axe-core@"+vendored {
		t.Errorf("SBOM names %s %s (%s), the vendored file is axe %s", c.Name, c.Version, c.Purl, vendored)
	}
	if len(c.Licenses) != 1 || c.Licenses[0].License.ID != "MPL-2.0" {
		t.Errorf("SBOM licenses = %+v, want MPL-2.0", c.Licenses)
	}
	if len(c.Hashes) != 1 || c.Hashes[0].Alg != "SHA-256" || c.Hashes[0].Content != axeDigest {
		t.Errorf("SBOM hashes = %+v, want SHA-256 %s", c.Hashes, axeDigest)
	}
	// The references (purl, tarball, license link) all name it too.
	for _, m := range regexp.MustCompile(`axe-core@([^"]+)"|axe-core-([^/"]+)\.tgz|/blob/v([^/"]+)/`).FindAllStringSubmatch(string(data), -1) {
		if v := m[1] + m[2] + m[3]; v != vendored {
			t.Errorf("the SBOM names axe-core %s in %q, the vendored file is %s", v, m[0], vendored)
		}
	}
}

// The SBOM goes onto the release and into checksums.txt, which the
// release signs and attests.
func TestAxeSBOMReleased(t *testing.T) {
	rel, err := os.ReadFile("../../.goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(rel), "- glob: sboms/axe-core.cdx.json"); n != 2 {
		t.Errorf(".goreleaser.yaml lists sboms/axe-core.cdx.json %d times, want under checksum and release", n)
	}
	if !strings.Contains(string(rel), "sboms:\n  - artifacts: archive") {
		t.Error(".goreleaser.yaml has no SBOM per archive")
	}
}
