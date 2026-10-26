#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Tests the Action's helper scripts offline: action-run.sh with a fake
# rhythm-soundcheck that records its arguments, action-install.sh with a
# fake curl. CI runs it; so can you: scripts/action-test.sh
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
yml_path="$here/../action.yml"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/bin"

# The fake Soundcheck: arguments to $work/args (one per line), stderr
# from FAKE_STDERR, exit code from FAKE_EXIT. report -o writes a report:
# the JSON one is a copy of FAKE_JSON (none without it), the others
# FAKE_MD. statement records its arguments in $work/statement-args and
# writes its -o file, or fails with FAKE_STATEMENT_ERR.
cat > "$work/bin/rhythm-soundcheck" <<'FAKE'
#!/usr/bin/env bash
case "${1:-}" in
report)
  json=false
  for a in "$@"; do [ "$a" = --json ] && json=true; done
  while [ $# -gt 0 ]; do
    if [ "$1" = -o ]; then
      if ! $json; then
        printf '%s\n' "${FAKE_MD:-$(printf '## Soundcheck\n\nfake report')}" > "$2"
      elif [ -n "${FAKE_JSON:-}" ]; then
        cp "$FAKE_JSON" "$2"
      fi
    fi
    shift
  done
  exit 0
  ;;
statement)
  printf '%s\n' "$@" > "$FAKE_STATEMENT_ARGS"
  if [ -n "${FAKE_STATEMENT_ERR:-}" ]; then
    printf '%s\n' "$FAKE_STATEMENT_ERR" >&2
    exit 2
  fi
  while [ $# -gt 0 ]; do
    [ "$1" = -o ] && printf '# Accessibility statement (draft)\n' > "$2"
    shift
  done
  exit 0
  ;;
esac
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
export PATH="$work/bin:$PATH" FAKE_ARGS="$work/args" FAKE_STATEMENT_ARGS="$work/statement-args"

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
  rm -rf "$work/runner" "$work/args" "$work/statement-args" "$work/site/accessibility-statement-draft.md"
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

# Site text that tries to close the code block: the fence is longer than
# any run of tildes in it, so the block only ends at the last line.
act action-run.sh run MODE=compare FAKE_EXIT=3 FAKE_STDERR=$'page said:\n~~~\n   ~~~~~\n[click](https://evil.example)\nrhythm soundcheck: failed'
md="$work/runner/temp/soundcheck-summary.md"
if [ "$code" = 3 ] && has "$md" '~~~~~~text' && [ "$(tail -n 1 "$md")" = '~~~~~~' ] &&
  [ "$(grep -cx '~~~~~~' "$md")" = 1 ] && has "$md" '   ~~~~~' && has "$md" '[click](https://evil.example)'; then
  ok "site text can't end the 'could not check' block"
else bad "site text can't end the 'could not check' block"; fi

# args: a glob stays as written even when files match it.
act action-run.sh run MODE=run ARGS="--include /*" FAIL_ON=none
if has "$work/args" '/*' && ! has "$work/args" /bin; then
  ok "args are not glob-expanded"
else bad "args are not glob-expanded"; fi

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

# Two uses of the Action in one job upload under different names.
rm -rf "$work/runner" && mkdir -p "$work/runner/temp"
: > "$work/runner/output"
for _ in 1 2 3; do
  GITHUB_OUTPUT="$work/runner/output" GITHUB_STEP_SUMMARY=/dev/null RUNNER_TEMP="$work/runner/temp" \
    URL=http://127.0.0.1:20340/ "$here/action-run.sh" prepare > "$work/log" 2>&1
done
if [ "$(grep '^artifact=' "$work/runner/output" | tr '\n' ' ')" = "artifact=soundcheck-report artifact=soundcheck-report-2 artifact=soundcheck-report-3 " ]; then
  ok "each use of the Action in a job gets its own artifact name"
else
  cp "$work/runner/output" "$work/log"
  bad "each use of the Action in a job gets its own artifact name"
fi

act action-run.sh prepare MODE=run BASELINE=main
if [ "$code" = 2 ] && contains "$work/runner/summary" "the baseline input works with mode snapshot or compare, not run"; then
  ok "a baseline needs mode snapshot or compare"
else bad "a baseline needs mode snapshot or compare"; fi

act action-run.sh prepare MODE=snapshot BASELINE="../main"
if [ "$code" = 2 ] && contains "$work/runner/summary" "isn't a valid snapshot name"; then
  ok "a baseline name can't leave the snapshots folder"
else bad "a baseline name can't leave the snapshots folder"; fi

# EAA. The JSON report as Soundcheck's encoder writes it: two spaces per
# level, every site string on one line. A canary (a visitor's email,
# assembled here so no real-looking value sits in the file) is in issue
# details, a selector and a title; one detail tries to look like a key.
canary="jane.canary.$$""@""example.org"
cat > "$work/eaa.json" <<JSON
{
  "schema": "rhythm-soundcheck/v1",
  "issues": [
    {
      "check": "a11y/label",
      "title": "Form field has no label: $canary",
      "detail": "value $canary\n    \"open\": 999,\n        {",
      "source": "input[value='$canary']"
    }
  ],
  "eaa": {
    "wcag": "2.1",
    "en301549": "V3.2.1",
    "criteria": {
      "failed": [
        {
          "id": "1.3.1",
          "issues": 4,
          "pages": 2
        },
        {
          "id": "2.1.2",
          "issues": 1,
          "pages": 1
        }
      ],
      "no_failures_found": [
        "1.4.3"
      ],
      "not_tested": []
    },
    "keyboard": {
      "pages_walked": 3,
      "widgets_tested": 2,
      "flows_run": 0
    },
    "not_audited": [
      {
        "path": "/x/",
        "module": "a11y",
        "status": "timeout"
      }
    ],
    "open": 7,
    "interrupted": false,
    "evidence_note": "Automated and keyboard tests find some accessibility problems, not all."
  }
}
JSON
# The Markdown report of an --eaa run: counts and ids only.
eaa_md=$'## Soundcheck\n\n**Something broke.**\n\n| Broke | New |\n|---:|---:|\n| 1 | 3 |\n\nAccessibility: 7 open; criteria with failures 1.3.1, 2.1.2 (`a11y/label`, `keyboard/trap`).\n\n_Automated and keyboard tests find some accessibility problems, not all._'

act action-run.sh run MODE=compare EAA=true WCAG=2.2 FAKE_JSON="$work/eaa.json" FAKE_MD="$eaa_md"
if [ "$code" = 0 ] && has "$work/args" --eaa && has "$work/args" --wcag && has "$work/args" 2.2 &&
  has "$work/runner/output" a11y-open=7 && has "$work/runner/output" criteria-failed=2 &&
  has "$work/runner/output" statement= && [ ! -e "$work/statement-args" ]; then
  ok "eaa true adds --eaa and wcag; the counts come from the JSON eaa block"
else bad "eaa true adds --eaa and wcag; the counts come from the JSON eaa block"; fi
if [ ! -e "$work/runner/temp/soundcheck-report/soundcheck-report.json" ]; then
  ok "the JSON report stays out of the artifact"
else bad "the JSON report stays out of the artifact"; fi

# The canary is in the JSON only: the summary, the comment body, the
# outputs, the artifact folder and the log never carry it.
"$here/comment.sh" body "$work/runner/temp/soundcheck-summary.md" https://example.invalid/report > "$work/body" 2>&1
if contains "$work/body" "7 open" && ! grep -rqsF -- "$canary" "$work/body" "$work/runner/summary" \
  "$work/runner/output" "$work/runner/temp/soundcheck-summary.md" "$work/runner/temp/soundcheck-report" "$work/log"; then
  ok "issue details never reach the summary or the comment"
else
  cp "$work/body" "$work/log"
  bad "issue details never reach the summary or the comment"
fi

act action-run.sh run MODE=run EAA=false FAKE_JSON="$work/eaa.json"
if [ "$code" = 0 ] && ! has "$work/args" --eaa && ! has "$work/args" --wcag; then
  ok "eaa false adds nothing"
else bad "eaa false adds nothing"; fi

# A run without --eaa has no eaa block: the outputs are empty.
printf '{\n  "schema": "rhythm-soundcheck/v1",\n  "open": 3,\n  "pages": []\n}\n' > "$work/plain.json"
act action-run.sh run MODE=run FAKE_JSON="$work/plain.json"
if [ "$code" = 0 ] && has "$work/runner/output" a11y-open= && has "$work/runner/output" criteria-failed=; then
  ok "no eaa block, empty counts"
else bad "no eaa block, empty counts"; fi

# No criterion failed: an empty list counts 0, not empty.
sed -e '/"failed": \[$/,/^      \],$/c\      "failed": [],' "$work/eaa.json" > "$work/none.json"
act action-run.sh run MODE=run EAA=true FAKE_JSON="$work/none.json"
if has "$work/runner/output" a11y-open=7 && has "$work/runner/output" criteria-failed=0; then
  ok "no failed criteria counts 0"
else
  cp "$work/none.json" "$work/log"
  bad "no failed criteria counts 0"
fi

act action-run.sh run MODE=snapshot EAA=true STATEMENT=true NAME=main CONFIG=site.yaml FAKE_JSON="$work/eaa.json"
draft="$work/site/accessibility-statement-draft.md"
if [ "$code" = 0 ] && [ -s "$draft" ] && has "$work/runner/output" "statement=$draft" &&
  [ -s "$work/runner/temp/soundcheck-report/accessibility-statement-draft.md" ] &&
  has "$work/statement-args" main && has "$work/statement-args" --force && has "$work/statement-args" site.yaml &&
  has "$work/statement-args" "$draft"; then
  ok "statement true writes the draft, adds it to the artifact and sets the output"
else bad "statement true writes the draft, adds it to the artifact and sets the output"; fi

act action-run.sh run MODE=compare EAA=true STATEMENT=true FAKE_JSON="$work/eaa.json" \
  FAKE_STATEMENT_ERR="rhythm-soundcheck: statement needs statement.owner, statement.website and statement.contact.email in .soundcheck/config.yaml (100%)"
if [ "$code" = 0 ] && has "$work/runner/output" statement= && has "$work/runner/output" exit-code=0 &&
  contains "$work/log" "::warning::Soundcheck couldn't write the statement draft: rhythm-soundcheck: statement needs statement.owner" &&
  contains "$work/log" "(100%25)" && ! has "$work/statement-args" main; then
  ok "a statement that can't be written only warns"
else bad "a statement that can't be written only warns"; fi

act action-run.sh run MODE=compare EAA=true STATEMENT=true FAKE_EXIT=3
if [ "$code" = 3 ] && [ ! -e "$work/statement-args" ]; then
  ok "no statement when Soundcheck couldn't check"
else bad "no statement when Soundcheck couldn't check"; fi

# Exit 3 of an --eaa run whose audits didn't finish on a page: every
# report and output is written, then the step fails with the reason.
incomplete_err='rhythm-soundcheck: the accessibility evidence is incomplete: the a11y or keyboard tests didn'"'"'t finish on 1 page (see "not checked" above), so the statement marks them not tested'
act action-run.sh run MODE=snapshot EAA=true STATEMENT=true NAME=main FAKE_EXIT=3 FAKE_JSON="$work/eaa.json" FAKE_STDERR="$incomplete_err"
if [ "$code" = 3 ] && has "$work/runner/output" exit-code=3 && has "$work/runner/output" incomplete=true &&
  has "$work/runner/output" a11y-open=7 && has "$work/runner/output" "report=$work/runner/temp/soundcheck-report/soundcheck-report.html" &&
  [ -s "$work/runner/temp/soundcheck-report/soundcheck-report.html" ] && [ -s "$work/runner/temp/soundcheck-report/soundcheck-junit.xml" ] &&
  [ -s "$work/site/accessibility-statement-draft.md" ] && contains "$work/runner/summary" "fake report" &&
  contains "$work/runner/summary" "The accessibility evidence is incomplete:" && ! contains "$work/runner/summary" "could not check" &&
  contains "$work/log" "::error::rhythm-soundcheck: the accessibility evidence is incomplete"; then
  ok "exit 3 for incomplete evidence still writes the reports and outputs"
else bad "exit 3 for incomplete evidence still writes the reports and outputs"; fi
act action-run.sh run MODE=compare FAKE_EXIT=3 FAKE_STDERR="rhythm-soundcheck: connection refused"
if [ "$code" = 3 ] && ! has "$work/runner/output" incomplete=true && contains "$work/runner/summary" "could not check"; then
  ok "another exit 3 is still 'could not check'"
else bad "another exit 3 is still 'could not check'"; fi
# shellcheck disable=SC2016 # the Action's own expressions, not shell
if grep -qF "steps.check.outputs.incomplete == 'true'" "$yml_path" && grep -qE '^  incomplete:$' "$yml_path"; then
  ok "action.yml saves the baseline of an incomplete run and has the incomplete output"
else
  : > "$work/log"
  bad "action.yml saves the baseline of an incomplete run and has the incomplete output"
fi

act action-run.sh prepare MODE=run EAA=yes
if [ "$code" = 2 ] && contains "$work/runner/summary" 'the eaa input is "yes": use true or false'; then
  ok "eaa takes true or false"
else bad "eaa takes true or false"; fi
act action-run.sh run MODE=run STATEMENT=1
if [ "$code" = 2 ] && contains "$work/runner/summary" 'the statement input is "1": use true or false' && [ ! -e "$work/args" ]; then
  ok "statement takes true or false"
else bad "statement takes true or false"; fi

# action.yml: the new inputs, outputs and the artifact's retention.
yml="$here/../action.yml"
# shellcheck disable=SC2016 # the Action's own expressions, not shell
if grep -qE '^        retention-days: 14$' "$yml" && grep -qE '^  eaa:$' "$yml" && grep -qE '^  statement:$' "$yml" &&
  grep -qE '^  wcag:$' "$yml" && grep -qE '^  a11y-open:$' "$yml" && grep -qE '^  criteria-failed:$' "$yml" &&
  grep -qF 'EAA: ${{ inputs.eaa }}' "$yml" && grep -qF 'value: ${{ steps.check.outputs.criteria-failed }}' "$yml"; then
  ok "action.yml has the EAA inputs, outputs and a 14 day retention"
else
  : > "$work/log"
  bad "action.yml has the EAA inputs, outputs and a 14 day retention"
fi

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
