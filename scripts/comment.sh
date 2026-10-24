#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Posts Soundcheck's result as one pull request comment for the GitHub
# Action (comment: true). Later runs update that comment instead of
# adding another.
#
#   comment.sh body MARKDOWN [REPORT_URL]   prints the comment body
#   comment.sh post MARKDOWN [REPORT_URL]   creates or updates the comment
#
# MARKDOWN is `rhythm-soundcheck report --md` (counts and changes) or the
# "could not check" note. The comment is found again by its first line,
# the marker below and by its author, the token's own login. Anyone can
# post a comment that starts with the marker. Updating theirs would show
# Soundcheck's results under their name for them to edit. A body over
# GitHub's 65,536 character limit is cut at a line, with a note that
# points to the full report.
#
# post uses gh with GH_TOKEN. It needs GITHUB_REPOSITORY and
# GITHUB_EVENT_PATH. The pull request is the event's own, or for a
# deployment_status event (Vercel, Netlify previews) the one whose head is
# the deployed commit. Posting needs "permissions: pull-requests: write";
# when it fails (a fork's read-only token) the step warns and goes on.
set -euo pipefail

marker='<!-- rhythm-soundcheck -->'
# Under GitHub's 65,536 characters even if every byte were one character.
limit=${SOUNDCHECK_COMMENT_LIMIT:-60000}

body() {
  local md=$1 url=${2:-} footer kept
  footer=""
  [ -z "$url" ] || footer="[Full report (HTML and JUnit)]($url)"
  if [ -n "${GITHUB_SERVER_URL:-}" ] && [ -n "${GITHUB_REPOSITORY:-}" ] && [ -n "${GITHUB_RUN_ID:-}" ]; then
    footer="${footer:+$footer · }[Workflow run]($GITHUB_SERVER_URL/$GITHUB_REPOSITORY/actions/runs/$GITHUB_RUN_ID)"
  fi
  local room=$((limit - ${#marker} - ${#footer} - 200))
  printf '%s\n' "$marker"
  if [ "$(wc -c < "$md")" -le "$room" ]; then
    cat "$md"
  else
    # Whole lines only, so a table row is never cut in half.
    kept=$(LC_ALL=C awk -v room="$room" '{ n += length($0) + 1; if (n > room) exit; print }' "$md")
    printf '%s\n' "$kept"
    # A cut inside the notes leaves <details> open.
    if [ "$(grep -c '<details>' <<< "$kept")" -gt "$(grep -c '</details>' <<< "$kept")" ]; then
      printf '\n</details>\n'
    fi
    printf '\n_The list is cut here to fit a comment; the full report has every change._\n'
  fi
  [ -z "$footer" ] || printf '\n%s\n' "$footer"
}

# pr prints the pull request number for this run, if there is one. A
# deployment_status event has none of its own, so it uses the open pull
# request of the deployed commit. Other events without one (a push to
# main) get none: their commit's pull request is usually merged by then.
pr() {
  local n sha
  n=$(jq -r '.pull_request.number // .issue.number // empty' "${GITHUB_EVENT_PATH:?}")
  if [ -z "$n" ]; then
    sha=$(jq -r '.deployment.sha // empty' "$GITHUB_EVENT_PATH")
    [ -z "$sha" ] || n=$(gh api "repos/$GITHUB_REPOSITORY/commits/$sha/pulls" --jq '[.[] | select(.state == "open")][0].number // empty' 2> /dev/null || true)
  fi
  printf '%s' "$n"
}

# me prints the login the token comments as. GITHUB_TOKEN and other app
# tokens can't read /user. GITHUB_TOKEN comments as github-actions[bot]
# and SOUNDCHECK_COMMENT_AUTHOR names another app's bot.
me() {
  if [ -n "${SOUNDCHECK_COMMENT_AUTHOR:-}" ]; then
    printf '%s' "$SOUNDCHECK_COMMENT_AUTHOR"
    return
  fi
  local login
  # gh prints an error's JSON body on stdout, so check what came back.
  if login=$(gh api user --jq .login 2> /dev/null) && [[ $login =~ ^[A-Za-z0-9][A-Za-z0-9-]*$ ]]; then
    printf '%s' "$login"
  else
    printf 'github-actions[bot]'
  fi
}

post() {
  local md=$1 url=${2:-} n id payload login
  : "${GITHUB_REPOSITORY:?}"
  n=$(pr)
  if [ -z "$n" ]; then
    echo "::notice::Soundcheck: no pull request for this run (event ${GITHUB_EVENT_NAME:-unknown}), so no comment was posted."
    return 0
  fi
  payload=$(mktemp)
  trap 'rm -f "$payload"' RETURN
  body "$md" "$url" | jq -Rs '{body: .}' > "$payload"
  login=$(me)
  # The first comment of ours (by its author and marker), oldest first.
  if ! id=$(gh api --paginate "repos/$GITHUB_REPOSITORY/issues/$n/comments" \
    --jq ".[] | select(.user.login == $(jq -n --arg l "$login" '$l') and (.body | startswith(\"$marker\"))) | .id" | sed -n 1p); then
    warn "couldn't read the pull request's comments"
    return 0
  fi
  if [ -n "$id" ]; then
    gh api -X PATCH "repos/$GITHUB_REPOSITORY/issues/comments/$id" --input "$payload" > /dev/null ||
      { warn "couldn't update comment $id"; return 0; }
    echo "Updated the Soundcheck comment on pull request #$n."
  else
    gh api -X POST "repos/$GITHUB_REPOSITORY/issues/$n/comments" --input "$payload" > /dev/null ||
      { warn "couldn't comment"; return 0; }
    echo "Commented on pull request #$n."
  fi
}

warn() {
  echo "::warning::Soundcheck: $1 on the pull request. The job needs \"permissions: pull-requests: write\"; pull requests from forks only get a read-only token."
}

cmd=${1:-}
md=${2:-}
[ -n "$md" ] || { echo "usage: comment.sh body|post MARKDOWN [REPORT_URL]" >&2; exit 2; }
[ -f "$md" ] || { echo "::warning::Soundcheck: no report to comment with ($md)"; exit 0; }
case "$cmd" in
body) body "$md" "${3:-}" ;;
post) post "$md" "${3:-}" ;;
*)
  echo "usage: comment.sh body|post MARKDOWN [REPORT_URL]" >&2
  exit 2
  ;;
esac
