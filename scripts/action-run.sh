#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Runs Soundcheck for the GitHub Action (action.yml).
#
#   action-run.sh prepare   checks the inputs, writes mode= and snapshots= outputs
#   action-run.sh run       runs Soundcheck and writes the reports
#
# Both read the Action's inputs from the environment: URL, AGAINST, MODE,
# NAME, BASELINE, FAIL_ON, ARGS, HEADERS, CONFIG and ECHO. They run in the
# working-directory input, so .soundcheck/ (config and snapshots) is
# looked up there.
#
# run writes the job summary, JUnit and the standalone HTML report into
# $RUNNER_TEMP/soundcheck-report, and the summary's Markdown to
# $RUNNER_TEMP/soundcheck-summary.md for the pull request comment. When
# Soundcheck can't check at all (exit 2 or 3) the summary still says why,
# with its last lines of output, so a staging site that is down doesn't
# leave an empty summary and a log to dig through.
set -uo pipefail

: "${GITHUB_OUTPUT:?}" "${RUNNER_TEMP:?}"
summary=${GITHUB_STEP_SUMMARY:-/dev/null}
markdown="$RUNNER_TEMP/soundcheck-summary.md"
out="$RUNNER_TEMP/soundcheck-report"
errlog="$RUNNER_TEMP/soundcheck-stderr.log"

# couldnt CODE TEXT tells the job summary, the comment and the log why
# Soundcheck couldn't check, then exits with CODE.
couldnt() {
  local code=$1 text=$2 last
  {
    printf '## Soundcheck\n\nSoundcheck could not check:\n\n~~~text\n'
    # A line starting with ~~~ would end the block early.
    printf '%s\n' "$text" | sed 's/^~~~/ ~~~/'
    printf '~~~\n'
  } > "$markdown"
  cat "$markdown" >> "$summary"
  last=$(printf '%s\n' "$text" | sed '/^[[:space:]]*$/d' | tail -n 1)
  # Workflow commands need % escaped.
  echo "::error::Soundcheck could not check: ${last//%/%25}"
  {
    echo "exit-code=$code"
    echo "markdown=$markdown"
  } >> "$GITHUB_OUTPUT"
  exit "$code"
}

# resolve sets mode, name and fail from the inputs.
resolve() {
  mode=${MODE:-}
  [ -n "$mode" ] || { [ -n "${AGAINST:-}" ] && mode=against || mode=run; }
  name=${NAME:-ci}
  if [ -n "${BASELINE:-}" ]; then
    [[ $BASELINE =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] ||
      couldnt 2 "baseline \"$BASELINE\" isn't a valid snapshot name: use letters, digits, dots, dashes and underscores"
    case "$mode" in
    snapshot) name=$BASELINE ;;
    compare) ;;
    *) couldnt 2 "the baseline input works with mode snapshot or compare, not $mode" ;;
    esac
  fi
  case "$mode" in
  against) [ -n "${AGAINST:-}" ] || couldnt 2 "mode against needs the against input" ;;
  snapshot | compare | run) ;;
  *) couldnt 2 "unknown mode \"$mode\": use against, snapshot, compare or run" ;;
  esac
  local level=${FAIL_ON:-}
  if [ -z "$level" ]; then
    case "$mode" in against | compare) level=broke ;; *) level=error ;; esac
  fi
  fail=()
  [ "$level" = none ] || fail=(--fail-on "$level")
}

prepare() {
  resolve
  {
    echo "mode=$mode"
    [ -z "${BASELINE:-}" ] || echo "snapshots=.soundcheck/snapshots/$BASELINE"
  } >> "$GITHUB_OUTPUT"
}

run() {
  resolve
  if [ -n "${BASELINE:-}" ] && [ "$mode" = compare ] && [ ! -d ".soundcheck/snapshots/$BASELINE" ]; then
    couldnt 2 "no baseline \"$BASELINE\" was found in the cache. Run the Action with mode: snapshot and baseline: $BASELINE on your main branch first; pull requests can restore caches saved on their base branch."
  fi

  # shellcheck disable=SC2206 # ARGS is a flag string to split
  local extra=(${ARGS:-}) line n=0 value
  # A header name is an HTTP token (RFC 9110).
  local token='^[-!#$%&'\''*+.^_`|~0-9A-Za-z]+$'
  # One header per line, each passed whole, so spaces and quotes in a
  # value reach the site unchanged.
  while IFS= read -r line || [ -n "$line" ]; do
    n=$((n + 1))
    line=${line%$'\r'}
    [[ $line =~ ^[[:space:]]*(#|$) ]] && continue
    line=$(printf '%s' "$line" | sed 's/^[[:space:]]*//')
    [[ $line == *:* && ${line%%:*} =~ $token ]] || couldnt 2 "headers: line $n isn't \"Name: value\""
    value=${line#*:}
    value=${value# }
    # Header values are often credentials (Authorization, Cookie): keep
    # them out of the log. Very short values would mask too much.
    [ "${#value}" -lt 4 ] || echo "::add-mask::$value"
    extra+=(-H "$line")
  done <<< "${HEADERS:-}"
  [ -z "${CONFIG:-}" ] || extra+=(--config "$CONFIG")
  case "${ECHO:-}" in
  "" | false) ;;
  true) extra+=(--echo) ;;
  *) extra+=("--echo=$ECHO") ;;
  esac

  local cmd
  case "$mode" in
  against) cmd=(rhythm-soundcheck "$URL" --against "$AGAINST") ;;
  snapshot) cmd=(rhythm-soundcheck snapshot "$URL" --name "$name") ;;
  compare)
    cmd=(rhythm-soundcheck compare "$URL")
    [ -z "${BASELINE:-}" ] || cmd+=(--snapshot "$BASELINE")
    ;;
  run) cmd=(rhythm-soundcheck run "$URL") ;;
  esac

  # stderr goes to the log as it happens and to a file for the summary.
  { "${cmd[@]}" "${fail[@]}" "${extra[@]}" --plain 2>&1 1>&3 3>&- | tee "$errlog" >&2; } 3>&1
  local code=$?
  if [ "$code" -ge 2 ]; then
    local lines
    lines=$(sed '/^[[:space:]]*$/d' "$errlog" | tail -n 5)
    couldnt "$code" "${lines:-rhythm-soundcheck exited with $code}"
  fi

  # Reports: the job summary, JUnit and the standalone HTML.
  local which=()
  [ "$mode" = snapshot ] && which=("$name")
  mkdir -p "$out"
  rhythm-soundcheck report "${which[@]}" --md -o "$markdown" -q || true
  cat "$markdown" >> "$summary" 2>/dev/null || true
  rhythm-soundcheck report "${which[@]}" "${fail[@]}" --junit -o "$out/soundcheck-junit.xml" -q || true
  rhythm-soundcheck report "${which[@]}" --html -o "$out/soundcheck-report.html" -q || true
  {
    echo "exit-code=$code"
    echo "report=$out/soundcheck-report.html"
    echo "markdown=$markdown"
  } >> "$GITHUB_OUTPUT"
}

case "${1:-}" in
prepare) prepare ;;
run) run ;;
*)
  echo "usage: action-run.sh prepare|run" >&2
  exit 2
  ;;
esac
