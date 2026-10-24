#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Tests comment.sh offline: the body built from fixture Markdown (marker,
# report, links, the size limit) and posting through a fake gh that
# records each call. CI runs it; so can you: scripts/comment-test.sh
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/bin"

# The fake gh: FAKE_COMMENTS is the JSON list of existing comments; each
# call is logged, a POST or PATCH body is saved, FAKE_GH_FAIL fails writes.
cat > "$work/bin/gh" <<'FAKE'
#!/usr/bin/env bash
echo "gh $*" >> "$FAKE_LOG"
method=GET input= jq= path=
while [ $# -gt 0 ]; do
  case "$1" in
  api | --paginate) ;;
  -X) method=$2; shift ;;
  --input) input=$2; shift ;;
  --jq) jq=$2; shift ;;
  *) path=$1 ;;
  esac
  shift
done
if [ "$method" != GET ]; then
  [ -z "${FAKE_GH_FAIL:-}" ] || { echo "HTTP 403: Resource not accessible by integration" >&2; exit 1; }
  cp "$input" "$FAKE_SENT"
  echo '{}'
  exit 0
fi
case "$path" in
*/comments) printf '%s' "${FAKE_COMMENTS:-[]}" | jq -r "$jq" ;;
*/pulls) printf '%s' "${FAKE_PULLS:-[]}" | jq -r "$jq" ;;
esac
FAKE
chmod +x "$work/bin/gh"
export PATH="$work/bin:$PATH" FAKE_LOG="$work/gh.log" FAKE_SENT="$work/sent.json"
export GITHUB_REPOSITORY=acme/site GITHUB_SERVER_URL=https://github.com GITHUB_RUN_ID=42

fail=0
ok() { echo "ok   $1"; }
bad() {
  echo "FAIL $1"
  sed 's/^/     /' "$work/out" | head -40
  fail=1
}
contains() { grep -qF -- "$2" "$1"; }

cat > "$work/report.md" <<'MD'
## ♪ Soundcheck

**Something broke.** 3 pages of https://staging.example.com compared with the snapshot from 2026-10-24 09:00 UTC.

| Broke | New | Changed | Slower | Fixed |
|---:|---:|---:|---:|---:|
| 1 | 0 | 0 | 0 | 0 |

### ✗ Broke (1)

| Change | Where | Before → after | Fix |
|---|---|---|---|
| Server error 500 | `/checkout/` | 200 → 500 | Look at the PHP and web server error logs for this URL. |

<sub>rhythm-soundcheck 1.2.3 · 3 pages · 2026-10-24 09:05 UTC</sub>
MD

"$here/comment.sh" body "$work/report.md" https://github.com/acme/site/actions/runs/42/artifacts/7 > "$work/out"
if [ "$(head -n 1 "$work/out")" = '<!-- rhythm-soundcheck -->' ] && contains "$work/out" '| 1 | 0 | 0 | 0 | 0 |' &&
  contains "$work/out" 'Server error 500' && contains "$work/out" '[Full report (HTML and JUnit)](https://github.com/acme/site/actions/runs/42/artifacts/7)' &&
  contains "$work/out" '[Workflow run](https://github.com/acme/site/actions/runs/42)' && ! contains "$work/out" 'cut here'; then
  ok "the body has the marker, the counts, the changes and the links"
else bad "the body has the marker, the counts, the changes and the links"; fi

# A report far over the limit: 5,000 change rows and open notes.
{
  sed -n '1,12p' "$work/report.md"
  for i in $(seq 5000); do echo "| PHP Warning: Undefined array key \"x$i\" | \`/page-$i/\` | | Fix the code at the source line. |"; done
  printf '\n<details><summary>2 notes</summary>\n\n- one\n'
} > "$work/big.md"
"$here/comment.sh" body "$work/big.md" > "$work/out"
chars=$(wc -m < "$work/out")
if [ "$chars" -le 65536 ] && [ "$(head -n 1 "$work/out")" = '<!-- rhythm-soundcheck -->' ] && contains "$work/out" 'cut here to fit a comment' &&
  contains "$work/out" '| Broke | New |' && [ "$(grep -c '^| PHP Warning' "$work/out")" -gt 100 ] &&
  [ "$(tail -n 3 "$work/out" | grep -c '^| PHP Warning')" = 0 ]; then
  ok "a long report is cut at a line, under GitHub's limit ($chars characters)"
else bad "a long report is cut at a line, under GitHub's limit ($chars characters)"; fi

# Cut inside <details>: the tag is closed.
{ printf '<details><summary>notes</summary>\n\n'; for i in $(seq 3000); do echo "- note $i, long enough to need some room in the comment body"; done; } > "$work/notes.md"
"$here/comment.sh" body "$work/notes.md" > "$work/out"
if [ "$(grep -c '<details>' "$work/out")" = "$(grep -c '</details>' "$work/out")" ]; then
  ok "a cut inside the notes closes <details>"
else bad "a cut inside the notes closes <details>"; fi

# post: no comment yet, so one is created on the event's pull request.
post() {
  rm -f "$work/gh.log" "$work/sent.json"
  env "$@" "$here/comment.sh" post "$work/report.md" > "$work/out" 2>&1
  code=$?
}
echo '{"pull_request": {"number": 7}}' > "$work/pr.json"
post GITHUB_EVENT_PATH="$work/pr.json" FAKE_COMMENTS='[{"id": 1, "body": "LGTM"}]'
if [ "$code" = 0 ] && contains "$work/gh.log" "-X POST repos/acme/site/issues/7/comments" && ! contains "$work/gh.log" PATCH &&
  [ "$(jq -r .body "$work/sent.json" | head -n 1)" = '<!-- rhythm-soundcheck -->' ]; then
  ok "the first run creates the comment"
else bad "the first run creates the comment"; fi

# post again: our comment exists, so it is updated, not duplicated.
post GITHUB_EVENT_PATH="$work/pr.json" FAKE_COMMENTS='[{"id": 1, "body": "LGTM"}, {"id": 99, "body": "<!-- rhythm-soundcheck -->\nold"}]'
if [ "$code" = 0 ] && contains "$work/gh.log" "-X PATCH repos/acme/site/issues/comments/99" && ! contains "$work/gh.log" POST &&
  jq -r .body "$work/sent.json" | grep -qF 'Server error 500'; then
  ok "a later run updates the same comment"
else bad "a later run updates the same comment"; fi

# deployment_status: the pull request is found from the deployed commit.
echo '{"deployment": {"sha": "abc123"}, "deployment_status": {"target_url": "https://preview.example.com"}}' > "$work/deploy.json"
post GITHUB_EVENT_PATH="$work/deploy.json" GITHUB_EVENT_NAME=deployment_status FAKE_PULLS='[{"number": 12}]'
if [ "$code" = 0 ] && contains "$work/gh.log" "repos/acme/site/commits/abc123/pulls" && contains "$work/gh.log" "-X POST repos/acme/site/issues/12/comments"; then
  ok "a deployment_status run comments on the deployed commit's pull request"
else bad "a deployment_status run comments on the deployed commit's pull request"; fi

# A push with no pull request: a notice, nothing posted.
echo '{}' > "$work/push.json"
post GITHUB_EVENT_PATH="$work/push.json" GITHUB_EVENT_NAME=push GITHUB_SHA=def456
if [ "$code" = 0 ] && contains "$work/out" "no pull request for this run (event push)" && ! contains "$work/gh.log" POST; then
  ok "no pull request, no comment"
else bad "no pull request, no comment"; fi

# A read-only token: a warning, and the step still succeeds.
post GITHUB_EVENT_PATH="$work/pr.json" FAKE_GH_FAIL=1
if [ "$code" = 0 ] && contains "$work/out" '::warning::Soundcheck: couldn'"'"'t comment on the pull request' && contains "$work/out" 'pull-requests: write'; then
  ok "a read-only token warns and goes on"
else bad "a read-only token warns and goes on"; fi

exit "$fail"
