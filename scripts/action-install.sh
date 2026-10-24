#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Installs Soundcheck for the GitHub Action (action.yml).
#
#   action-install.sh check     picks the version and says whether it's installed
#   action-install.sh install   downloads, verifies and installs that version
#
# check reads VERSION (the version input) and ACTION_REF (github.action_ref)
# and writes version=X.Y.Z and skip=true|false to $GITHUB_OUTPUT. With no
# version input it installs the release that matches the action's own tag,
# so RhythmShahriar/soundcheck@v1.2.3 always runs Soundcheck 1.2.3 and a
# new release can't change a pinned workflow's results. A branch or a
# major tag such as @v1 doesn't name a release; then it takes the latest
# one and warns. Every step of a job installs into the same folder, so a
# second Soundcheck step finds the version already there and skips the
# download (and the cosign install before it).
#
# install reads VERSION (the X.Y.Z from check).
set -euo pipefail

repo=https://github.com/RhythmShahriar/soundcheck
dir="${RUNNER_TEMP:?}/soundcheck-bin"

# latest asks GitHub which release /releases/latest redirects to.
latest() {
  local url
  url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$repo/releases/latest") || {
    echo "::error::couldn't look up Soundcheck's latest release at $repo/releases/latest" >&2
    return 1
  }
  case "$url" in
  */releases/tag/v*) printf '%s\n' "${url##*/releases/tag/v}" ;;
  *)
    echo "::error::couldn't tell Soundcheck's latest release from $url" >&2
    return 1
    ;;
  esac
}

check() {
  local v=${VERSION:-} ref=${ACTION_REF:-}
  if [ -z "$v" ]; then
    if [[ $ref =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
      v=$ref
    else
      echo "::warning::Soundcheck's version isn't pinned: the action ref \"${ref:-none}\" isn't a release tag such as v1.2.3, so the latest release is installed and a new one can change your results. Use the action at a release tag or set the version input."
      v=latest
    fi
  fi
  if [ "$v" = latest ]; then v=$(latest); fi
  v=${v#v}
  [[ $v =~ ^[0-9]+\.[0-9]+\.[0-9]+([-.+][0-9A-Za-z.-]+)?$ ]] || {
    echo "::error::version \"$v\" isn't a Soundcheck version such as 1.2.3 or latest"
    exit 2
  }
  local skip=false
  if [ -x "$dir/rhythm-soundcheck" ] && [ "$("$dir/rhythm-soundcheck" --version 2>/dev/null)" = "rhythm-soundcheck $v" ]; then
    skip=true
    echo "Soundcheck $v is already installed; skipping the download."
    echo "$dir" >> "${GITHUB_PATH:?}"
  fi
  {
    echo "version=$v"
    echo "skip=$skip"
  } >> "${GITHUB_OUTPUT:?}"
}

install() {
  local v=${VERSION:?} arch os
  case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *)
    echo "::error::unsupported CPU $(uname -m)"
    exit 1
    ;;
  esac
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  local base="$repo/releases/download/v$v" archive="rhythm-soundcheck_${os}_${arch}.tar.gz"
  rm -rf "$dir"
  mkdir -p "$dir"
  cd "$dir"
  curl -fsSLO "$base/$archive"
  curl -fsSLO "$base/checksums.txt"
  curl -fsSLO "$base/checksums.txt.sigstore.json"
  # The signature, then the checksum: see verify-release.sh.
  "$(dirname "$0")/verify-release.sh" "$archive"
  tar -xzf "$archive" rhythm-soundcheck
  rm -f "$archive" checksums.txt checksums.txt.sigstore.json
  ./rhythm-soundcheck --version
}

case "${1:-}" in
check) check ;;
install)
  install
  echo "$dir" >> "${GITHUB_PATH:?}"
  ;;
*)
  echo "usage: action-install.sh check|install" >&2
  exit 2
  ;;
esac
