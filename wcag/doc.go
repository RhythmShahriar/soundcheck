// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package wcag is the catalog of WCAG success criteria at levels A
// and AA with their EN 301 549 clauses and how Soundcheck tests each
// (criteria.yaml). It also holds the table of the vendored axe-core's
// rules (axe_rules.gen.go). It also sums up an --eaa run against the catalog
// (Summarize). It says which criteria the run's tests found failures
// of, which they found none of and which need a person: never that a
// site meets a criterion.
package wcag

//go:generate node ../scripts/gen-axe-rules.mjs
