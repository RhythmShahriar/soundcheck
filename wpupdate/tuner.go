// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wpupdate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// vulnPrefix starts the id of Tuner's finding for a vulnerable plugin:
// vuln-plugin-<slug>, with the slug in lower case.
const vulnPrefix = "vuln-plugin-"

// severityRank orders Tuner's severities, worst first.
var severityRank = map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3, "info": 4}

// tunerReport is the part of rhythm-tuner's JSON report this package
// reads. Tuner writes one object for one site and an array for several.
type tunerReport struct {
	Schema   string `json:"schema"`
	Findings []struct {
		ID       string `json:"id"`
		Severity string `json:"severity"`
	} `json:"findings"`
}

// readTuner returns the plugins a rhythm-tuner JSON report lists as
// vulnerable, by lower-case slug, with the worst severity of each.
func readTuner(path string) (map[string]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a report the user names
	if err != nil {
		return nil, err
	}
	var reports []tunerReport
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '[' {
		err = json.Unmarshal(trimmed, &reports)
	} else {
		var one tunerReport
		err = json.Unmarshal(trimmed, &one)
		reports = []tunerReport{one}
	}
	if err != nil {
		return nil, fmt.Errorf("%s isn't a rhythm-tuner JSON report: %w", path, err)
	}
	vuln := map[string]string{}
	for _, r := range reports {
		if !strings.HasPrefix(r.Schema, "rhythm-tuner/") {
			return nil, fmt.Errorf("%s isn't a rhythm-tuner JSON report (run rhythm-tuner with --json)", path)
		}
		for _, f := range r.Findings {
			slug, ok := strings.CutPrefix(f.ID, vulnPrefix)
			if !ok || slug == "" {
				continue
			}
			sev := strings.ToLower(f.Severity)
			if old, seen := vuln[slug]; seen && rank(old) <= rank(sev) {
				continue
			}
			vuln[slug] = sev
		}
	}
	return vuln, nil
}

// rank is a severity's place, worst first; unrated counts as medium,
// as Tuner ranks it.
func rank(sev string) int {
	if r, ok := severityRank[sev]; ok {
		return r
	}
	return severityRank["medium"]
}
