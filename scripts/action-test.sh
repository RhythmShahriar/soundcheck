#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Tests the Action's helper scripts offline: action-run.sh with a fake
# rhythm-soundcheck that records its arguments, and action-install.sh
# with a fake curl. CI runs it; so can you: scripts/action-test.sh
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/bin"

# The fake Soundcheck: arguments to $work/args (one per line), stderr
# from FAKE_STDERR, exit code from FAKE_EXIT. report -o writes a report.
cat > "$work/bin/rhythm-soundcheck" <<'FAKE'
#!/usr/bin/env bash
if [ "${1:-}" = report ]; then
  while [ $# -gt 0 ]; do
    [ "$1" = -o ] && printf '## Soundcheck\n\nfake report\n' > "$2"
    shift
  done
  exit 0
fi
printf '%s\n' "$@" > "$FAKE_ARGS"
[ -z "${FAKE_STDERR:-}" ] || printf '%s\n' "$FAKE_STDERR" >&2
exit "${FAKE_EXIT:-0}"
FAKE
# The fake curl answers the latest-release lookup.
cat > "$work/bin/curl" <<'FAKE'
#!/usr/bin/env bash
printf 'https://github.com/RhythmShahriar/soundcheck/releases/tag/v9.8.7'
FAKE
chmod +x "$work/bin/rhythm-soundcheck" "$work/bin/curl"
export PATH="$work/bin:$PATH" FAKE_ARGS="$work/args"

fail=0
ok() { echo "ok   $1"; }
bad() {
  echo "FAIL $1"
  sed 's/^/     /' "$work/log"
  fail=1
}

# act SCRIPT ARG [VAR=value...] runs a helper with a fresh runner
# environment and the given inputs. Its status is in $code.
act() {
  local script=$1 arg=$2
  shift 2
  rm -rf "$work/runner" "$work/args"
  mkdir -p "$work/runner/temp" "$work/site"
  : > "$work/runner/output"
  : > "$work/runner/summary"
  : > "$work/runner/path"
  (
    cd "$work/site" || exit 99
    env GITHUB_OUTPUT="$work/runner/output" GITHUB_STEP_SUMMARY="$work/runner/summary" \
      GITHUB_PATH="$work/runner/path" RUNNER_TEMP="$work/runner/temp" URL=http://127.0.0.1:20340/ "$@" \
      "$here/$script" "$arg"
  ) > "$work/log" 2>&1
  code=$?
}
has() { grep -qxF -- "$2" "$1"; }   # has FILE LINE
contains() { grep -qF -- "$2" "$1"; } # contains FILE TEXT

# Headers: each line is one argument, spaces and quotes untouched.
act action-run.sh run MODE=run HEADERS=$'X-Test: a "b" \'c\'  d\n\n# a comment\r\nAuthorization: Basic dXNlcjpwYXNz\r' CONFIG="my config.yaml"
if [ "$code" = 0 ] && has "$work/args" 'X-Test: a "b" '"'"'c'"'"'  d' && has "$work/args" 'Authorization: Basic dXNlcjpwYXNz' &&
  [ "$(grep -cx -- -H "$work/args")" = 2 ] && has "$work/args" "my config.yaml" && has "$work/args" --config; then
  ok "headers and config reach Soundcheck whole"
else bad "headers and config reach Soundcheck whole"; fi
if contains "$work/log" '::add-mask::Basic dXNlcjpwYXNz' && contains "$work/log" '::add-mask::a "b"'; then
  ok "header values are masked"
else bad "header values are masked"; fi
if has "$work/runner/output" exit-code=0 && contains "$work/runner/summary" "fake report" && has "$work/runner/output" "markdown=$work/runner/temp/soundcheck-summary.md"; then
  ok "a run writes the summary and its outputs"
else bad "a run writes the summary and its outputs"; fi

act action-run.sh run MODE=run HEADERS="not a header"
if [ "$code" = 2 ] && contains "$work/runner/summary" 'headers: line 1 isn'"'"'t "Name: value"' && [ ! -e "$work/args" ]; then
  ok "a header line without a name is refused"
else bad "a header line without a name is refused"; fi

act action-run.sh run MODE=run ARGS="--limit 5 -k" ECHO=true FAIL_ON=none
if has "$work/args" --limit && has "$work/args" 5 && has "$work/args" -k && has "$work/args" --echo && ! has "$work/args" --fail-on; then
  ok "args, echo and fail-on none"
else bad "args, echo and fail-on none"; fi

# Exit 2 or 3: the summary says why, with Soundcheck's last lines.
act action-run.sh run MODE=compare FAKE_EXIT=3 FAKE_STDERR=$'Checking http://127.0.0.1:20349/\n\nrhythm soundcheck: http://127.0.0.1:20349/: connection refused'
if [ "$code" = 3 ] && contains "$work/runner/summary" "Soundcheck could not check:" &&
  contains "$work/runner/summary" "connection refused" && has "$work/runner/output" exit-code=3 &&
  contains "$work/log" "::error::Soundcheck could not check: rhythm soundcheck: http://127.0.0.1:20349/: connection refused" &&
  contains "$work/runner/temp/soundcheck-summary.md" "connection refused"; then
  ok "exit 3 writes 'could not check' to the summary"
else bad "exit 3 writes 'could not check' to the summary"; fi

act action-run.sh run MODE=against
if [ "$code" = 2 ] && contains "$work/runner/summary" "mode against needs the against input"; then
  ok "mode against without against"
else bad "mode against without against"; fi

# Baselines kept between workflow runs.
act action-run.sh prepare MODE=snapshot BASELINE=main
if [ "$code" = 0 ] && has "$work/runner/output" mode=snapshot && has "$work/runner/output" snapshots=.soundcheck/snapshots/main; then
  ok "prepare names the baseline's folder for the cache"
else bad "prepare names the baseline's folder for the cache"; fi

act action-run.sh run MODE=snapshot BASELINE=main NAME=ci
if [ "$code" = 0 ] && has "$work/args" snapshot && has "$work/args" main && ! has "$work/args" ci; then
  ok "snapshot with a baseline names the snapshot after it"
else bad "snapshot with a baseline names the snapshot after it"; fi

act action-run.sh run MODE=compare BASELINE=main
if [ "$code" = 2 ] && contains "$work/runner/summary" 'no baseline "main" was found in the cache' && [ ! -e "$work/args" ]; then
  ok "compare without a restored baseline says so"
else bad "compare without a restored baseline says so"; fi

mkdir -p "$work/site/.soundcheck/snapshots/main"
act action-run.sh run MODE=compare BASELINE=main
if [ "$code" = 0 ] && has "$work/args" --snapshot && has "$work/args" main; then
  ok "compare with a restored baseline compares with it"
else bad "compare with a restored baseline compares with it"; fi
rm -rf "$work/site/.soundcheck"

act action-run.sh prepare MODE=run BASELINE=main
if [ "$code" = 2 ] && contains "$work/runner/summary" "the baseline input works with mode snapshot or compare, not run"; then
  ok "a baseline needs mode snapshot or compare"
else bad "a baseline needs mode snapshot or compare"; fi

act action-run.sh prepare MODE=snapshot BASELINE="../main"
if [ "$code" = 2 ] && contains "$work/runner/summary" "isn't a valid snapshot name"; then
  ok "a baseline name can't leave the snapshots folder"
else bad "a baseline name can't leave the snapshots folder"; fi

# Install: the version follows the action's tag, a matching install is reused.
act action-install.sh check ACTION_REF=v2.3.4
if [ "$code" = 0 ] && has "$work/runner/output" version=2.3.4 && has "$work/runner/output" skip=false && ! contains "$work/log" "::warning::"; then
  ok "the action's tag picks the version"
else bad "the action's tag picks the version"; fi

act action-install.sh check ACTION_REF=v1
if [ "$code" = 0 ] && has "$work/runner/output" version=9.8.7 && contains "$work/log" '::warning::Soundcheck'"'"'s version isn'"'"'t pinned: the action ref "v1"'; then
  ok "a major tag takes the latest release and warns"
else bad "a major tag takes the latest release and warns"; fi

act action-install.sh check VERSION=v1.0.0 ACTION_REF=v2.3.4
if has "$work/runner/output" version=1.0.0; then ok "the version input wins"; else bad "the version input wins"; fi

act action-install.sh check VERSION="1.0.0; rm -rf /"
if [ "$code" = 2 ]; then ok "a malformed version is refused"; else bad "a malformed version is refused"; fi

installed() { # installed VERSION ARGS... runs check with VERSION already installed
  local v=$1
  shift
  rm -rf "$work/runner"
  mkdir -p "$work/runner/temp/soundcheck-bin"
  printf '#!/bin/sh\necho "rhythm-soundcheck %s"\n' "$v" > "$work/runner/temp/soundcheck-bin/rhythm-soundcheck"
  chmod +x "$work/runner/temp/soundcheck-bin/rhythm-soundcheck"
  : > "$work/runner/output"
  : > "$work/runner/path"
  env GITHUB_OUTPUT="$work/runner/output" GITHUB_PATH="$work/runner/path" RUNNER_TEMP="$work/runner/temp" "$@" \
    "$here/action-install.sh" check > "$work/log" 2>&1
  code=$?
}
installed 1.2.3 VERSION=1.2.3
if [ "$code" = 0 ] && has "$work/runner/output" skip=true && contains "$work/log" "Soundcheck 1.2.3 is already installed; skipping the download." &&
  has "$work/runner/path" "$work/runner/temp/soundcheck-bin"; then
  ok "a matching install is reused"
else bad "a matching install is reused"; fi
installed 1.2.2 VERSION=1.2.3
if has "$work/runner/output" skip=false; then ok "another version is replaced"; else bad "another version is replaced"; fi

exit "$fail"
