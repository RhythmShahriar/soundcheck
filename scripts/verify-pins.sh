#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Checks the committed Chrome for Testing pins before a release.
#
#   verify-pins.sh [PINGEN FLAGS...]
#
# Run it from the repository root. It reads the version browser/cft_pins.go
# pins, hashes that same version's archives again and stops if anything
# differs. It never picks a newer Stable: Google moves Stable about once a
# week. A tag pushed after that must still release with the pins it was
# tested with. `go generate ./browser` is the step that bumps them.
# Extra flags go to pingen (the tests point -index and -storage at a
# local server).
set -euo pipefail

pins=browser/cft_pins.go
v=$(sed -n 's/^const cftVersion = "\(.*\)"$/\1/p' "$pins")
if [ -z "$v" ]; then
  echo "verify-pins: $pins pins no Chrome for Testing version; run go generate ./browser and commit it before tagging" >&2
  exit 1
fi
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
go run ./browser/internal/pingen -o "$work/cft_pins.go" -version "$v" "$@"
if ! diff -u "$pins" "$work/cft_pins.go"; then
  echo "verify-pins: Chrome for Testing $v no longer matches $pins; the archives changed since they were pinned" >&2
  exit 1
fi
echo "verify-pins: Chrome for Testing $v matches $pins"
