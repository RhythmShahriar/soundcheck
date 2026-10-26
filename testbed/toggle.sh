#!/bin/sh
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Switches testbed scenarios between their good and broken state.
#
#   ./toggle.sh <id|all> good|broken    e.g. ./toggle.sh s05 broken, ./toggle.sh 5 good
#   ./toggle.sh status                  lists the broken scenarios
#
# A scenario is broken while state/<id> exists. The WordPress and PHP
# containers mount state/ read-write and check it on every request (the
# sites write only the orders.txt and sidefx.txt counters there), so a
# switch takes effect immediately, with no rebuild or restart. Set
# TESTBED_STATE to use another folder, e.g. for the plain-PHP site under
# PHP's built-in server (see README.md); the site must get the same one. The page
# cache keys on the set of broken scenarios, so it never serves a page
# rendered in the other state.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
state=${TESTBED_STATE:-$here/state}
mkdir -p "$state"

ids() { sed -n 's/^  - id: *\(s[0-9][0-9]\).*/\1/p' "$here/scenarios.yaml"; }
words() { printf '%s\n' "$1" | tr '\n' ' ' | sed 's/ $//'; }

usage() {
	echo "usage: $0 <id|all> good|broken" >&2
	echo "       $0 status" >&2
	exit 2
}

if [ "${1:-}" = "status" ]; then
	# ls fails when nothing matches; that just means none is broken.
	found=$(cd "$state" && ls s[0-9][0-9] 2>/dev/null) || found=
	if [ -z "$found" ]; then echo "all scenarios good"; else echo "broken: $(words "$found")"; fi
	exit 0
fi
[ $# -eq 2 ] || usage

case "$1" in
all) targets=$(ids) ;;
s[0-9][0-9]) targets=$1 ;;
[0-9]) targets=s0$1 ;;
[0-9][0-9] | 0[0-9]) targets=s$1 ;;
*) usage ;;
esac
for id in $targets; do
	ids | grep -qx "$id" || { echo "$0: unknown scenario $id (see scenarios.yaml)" >&2; exit 2; }
done

for id in $targets; do
	case "$2" in
	good) rm -f "$state/$id" ;;
	broken) date -u +%Y-%m-%dT%H:%M:%SZ > "$state/$id" ;;
	*) usage ;;
	esac
done
echo "$(words "$targets") → $2"
