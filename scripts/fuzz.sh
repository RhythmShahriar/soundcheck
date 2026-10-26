#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Runs every fuzz target in the module, one after another.
#
#   FUZZTIME=30s scripts/fuzz.sh
#
# Run it from the repository root. go test runs one fuzz target per
# package and call, so the targets are listed first (go test -list) and
# each gets FUZZTIME (default 30s). New inputs that fail land in the
# package's testdata/fuzz/ folder: commit them with the fix.
set -euo pipefail

fuzztime=${FUZZTIME:-30s}
list=$(go test -list '^Fuzz' ./...)

ran=0
names=()
while IFS= read -r line; do
	case $line in
	Fuzz*) names+=("$line") ;;
	"ok"*)
		pkg=$(printf '%s\n' "$line" | awk '{print $2}')
		for name in ${names[@]+"${names[@]}"}; do
			printf '%s %s (%s)\n' "$pkg" "$name" "$fuzztime"
			go test -run '^$' -fuzz "^${name}\$" -fuzztime "$fuzztime" "$pkg"
			ran=$((ran + 1))
		done
		names=()
		;;
	esac
done <<<"$list"

printf 'fuzz targets run: %d (%s each)\n' "$ran" "$fuzztime"
