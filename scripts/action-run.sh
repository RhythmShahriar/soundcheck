#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Runs Soundcheck for the GitHub Action (action.yml).
#
#   action-run.sh prepare   checks the inputs, writes mode=, snapshots= and
#                           artifact= outputs
#   action-run.sh run       runs Soundcheck and writes the reports
#
# Both read the Action's inputs from the environment: URL, AGAINST, MODE,
# NAME, BASELINE, FAIL_ON, ARGS, HEADERS, CONFIG, ECHO, EAA, STATEMENT and
# WCAG. They run in the working-directory input, so .soundcheck/ (config
# and snapshots) is looked up there.
#
# run writes the job summary, JUnit and the standalone HTML report into
# $RUNNER_TEMP/soundcheck-report. With STATEMENT=true it also writes the
# statement draft accessibility-statement-draft.md into the working
# directory and copies it into that folder. The a11y-open and
# criteria-failed outputs come from the eaa block of the JSON report.
# The summary's Markdown also goes to
# $RUNNER_TEMP/soundcheck-summary.md for the pull request comment. When
# Soundcheck can't check at all (exit 2 or 3) the summary still says why,
# with its last lines of output, so a staging site that is down doesn't
# leave an empty summary and a log to dig through. An --eaa run that
# exits 3 because some pages' audits didn't finish still writes every
# report and output, sets incomplete=true and then fails the step.
set -uo pipefail

: "${GITHUB_OUTPUT:?}" "${RUNNER_TEMP:?}"
summary=${GITHUB_STEP_SUMMARY:-/dev/null}
markdown="$RUNNER_TEMP/soundcheck-summary.md"
out="$RUNNER_TEMP/soundcheck-report"
errlog="$RUNNER_TEMP/soundcheck-stderr.log"

# incomplete_text starts the line Soundcheck prints when it exits 3
# because the a11y or keyboard tests didn't finish on some pages of an
# --eaa run (its other exits 3 are runs that couldn't check at all).
incomplete_text="the accessibility evidence is incomplete"
incomplete_note="**The accessibility evidence is incomplete:** the a11y or keyboard tests didn't finish on some pages (see \"not checked\" in the report), so the statement marks them not tested and the job fails."

# couldnt CODE TEXT tells the job summary, the comment and the log why
# Soundcheck couldn't check, then exits with CODE.
couldnt() {
  local code=$1 text=$2 last fence
  # The text can carry what the site sent (URLs, page messages). A fence
  # longer than any run of tildes in it can't be closed from inside, so
  # none of it renders as Markdown in the summary or the comment.
  fence=$(printf '%s\n' "$text" | grep -o '~*' | awk '{ if (length($0) > n) n = length($0) } END { n = n < 3 ? 3 : n + 1; while (n--) printf "~" }')
  {
    printf '## Soundcheck\n\nSoundcheck could not check:\n\n%stext\n' "$fence"
    printf '%s\n' "$text"
    printf '%s\n' "$fence"
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
  local input value
  for input in eaa:EAA statement:STATEMENT; do
    value=${input#*:}
    value=${!value:-}
    case "$value" in
    "" | true | false) ;;
    *) couldnt 2 "the ${input%%:*} input is \"$value\": use true or false" ;;
    esac
  done
}

# eaa_counts FILE prints the open and criteria-failed counts from the eaa
# block of a JSON report, one per line, or nothing for a run without
# --eaa. The runner may have no jq; the report is indented two spaces
# per level by Soundcheck's encoder: every string from the site is
# escaped onto one line, so only a real key can start a line at a given
# indent. "open" sits right in the eaa block; each failed criterion is
# one object in criteria.failed.
eaa_counts() {
  awk '
    /^  "eaa": \{/ { eaa = 1; next }
    eaa && /^  \}/ { eaa = 0 }
    eaa && /^    "open": [0-9]+/ { v = $0; gsub(/[^0-9]/, "", v); open = v }
    eaa && /^      "failed": \[/ { failed = 0; inside = !/\]/; next }
    inside && /^      \]/ { inside = 0 }
    inside && /^        \{/ { failed++ }
    END { if (open != "") { print open; print failed + 0 } }
  ' "$1"
}

# artifact_name prints the report artifact's name. The first use of the
# Action in a job uploads soundcheck-report, later ones soundcheck-report-2,
# -3 and so on: artifact names are unique in a workflow run, so a second
# upload under the same name would fail.
artifact_name() {
  local count="$RUNNER_TEMP/soundcheck-artifacts" n=0
  [ -f "$count" ] && n=$(cat "$count")
  [[ $n =~ ^[0-9]+$ ]] || n=0
  n=$((n + 1))
  echo "$n" > "$count"
  if [ "$n" = 1 ]; then echo soundcheck-report; else echo "soundcheck-report-$n"; fi
}

prepare() {
  resolve
  {
    echo "mode=$mode"
    echo "artifact=$(artifact_name)"
    [ -z "${BASELINE:-}" ] || echo "snapshots=.soundcheck/snapshots/$BASELINE"
  } >> "$GITHUB_OUTPUT"
}

run() {
  resolve
  if [ -n "${BASELINE:-}" ] && [ "$mode" = compare ] && [ ! -d ".soundcheck/snapshots/$BASELINE" ]; then
    couldnt 2 "no baseline \"$BASELINE\" was found in the cache. Run the Action with mode: snapshot and baseline: $BASELINE on your main branch first; pull requests can restore caches saved on their base branch."
  fi

  # ARGS is split on whitespace only: read does no globbing, so
  # --include /blog/* reaches Soundcheck as written.
  local extra=() line n=0 value
  read -r -d '' -a extra <<< "${ARGS:-}" || true
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
  [ "${EAA:-}" != true ] || extra+=(--eaa)
  [ -z "${WCAG:-}" ] || extra+=(--wcag "$WCAG")

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
  local code=$? incomplete=""
  # Exit 3 of an --eaa run whose pages were checked but whose a11y or
  # keyboard audit didn't finish on some of them: the evidence of every
  # other page is there, so the reports, the outputs and the baseline
  # are written as for any finished run before the step fails.
  if [ "$code" = 3 ] && grep -qF "$incomplete_text" "$errlog"; then
    incomplete=$(grep -F "$incomplete_text" "$errlog" | tail -n 1)
  elif [ "$code" -ge 2 ]; then
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

  # The counts for the a11y-open and criteria-failed outputs. The JSON
  # holds issue details, so it stays out of the artifact.
  local json="$RUNNER_TEMP/soundcheck-report.json" open="" criteria=""
  rm -f "$json"
  rhythm-soundcheck report "${which[@]}" --json -o "$json" -q || true
  # Line by line rather than mapfile: macOS runners may have bash 3.2.
  [ ! -f "$json" ] || { read -r open && read -r criteria; } < <(eaa_counts "$json")

  local draft=""
  [ "${STATEMENT:-}" != true ] || draft=$(statement)
  {
    echo "exit-code=$code"
    echo "report=$out/soundcheck-report.html"
    echo "markdown=$markdown"
    echo "statement=$draft"
    echo "a11y-open=$open"
    echo "criteria-failed=$criteria"
    echo "incomplete=${incomplete:+true}"
  } >> "$GITHUB_OUTPUT"
  if [ -n "$incomplete" ]; then
    printf '\n%s\n' "$incomplete_note" >> "$summary"
    printf '\n%s\n' "$incomplete_note" >> "$markdown"
    # Workflow commands need % escaped.
    echo "::error::${incomplete//%/%25}"
    exit 3
  fi
}

# statement writes the statement draft of the run that just finished into
# the working directory and the report folder and prints its path. The
# workspace is a fresh checkout, so --force replaces a draft left there.
# A draft that can't be written (no statement: block in the config, no
# --eaa results) is a warning, not a failed job: the checks still ran.
statement() {
  local draft="$PWD/accessibility-statement-draft.md" args=(statement) last
  [ "$mode" != snapshot ] || args+=("$name")
  args+=(-o "$draft" --force -q)
  [ -z "${CONFIG:-}" ] || args+=(--config "$CONFIG")
  if rhythm-soundcheck "${args[@]}" 2> "$errlog" >&2; then
    cp "$draft" "$out/" 2>/dev/null || true
    printf '%s\n' "$draft"
    return
  fi
  cat "$errlog" >&2
  last=$(sed '/^[[:space:]]*$/d' "$errlog" | tail -n 1)
  echo "::warning::Soundcheck couldn't write the statement draft: ${last//%/%25}" >&2
}

case "${1:-}" in
prepare) prepare ;;
run) run ;;
*)
  echo "usage: action-run.sh prepare|run" >&2
  exit 2
  ;;
esac
