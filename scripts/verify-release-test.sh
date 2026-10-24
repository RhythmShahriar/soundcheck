#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Tests verify-release.sh offline with a fake cosign that records its
# arguments and passes only when checksums.txt is unchanged. CI runs it;
# so can you: scripts/verify-release-test.sh
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/bin" "$work/rel"

cat > "$work/bin/cosign" <<FAKE
#!/usr/bin/env bash
printf '%s\n' "\$@" > "$work/cosign-args"
$(command -v sha256sum) -c --status "$work/signed.sha256"
FAKE
chmod +x "$work/bin/cosign"
export PATH="$work/bin:$PATH"

cd "$work/rel"
fresh() {
  printf 'soundcheck binary\n' > rhythm-soundcheck_linux_amd64.tar.gz
  printf 'other\n' > rhythm-soundcheck_darwin_arm64.tar.gz
  sha256sum rhythm-soundcheck_*.tar.gz > checksums.txt
  echo '{}' > checksums.txt.sigstore.json
  # The fake signature covers checksums.txt as it is now.
  sha256sum "$work/rel/checksums.txt" > "$work/signed.sha256"
}
fail=0
expect() { # expect pass|fail NAME ARCHIVE
  if "$here/verify-release.sh" "$3" > "$work/out" 2>&1; then got=pass; else got=fail; fi
  if [ "$got" = "$1" ]; then echo "ok   $2"; else echo "FAIL $2: got $got"; cat "$work/out"; fail=1; fi
}

fresh
expect pass "an untouched release" rhythm-soundcheck_linux_amd64.tar.gz
for want in --bundle checksums.txt.sigstore.json --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/RhythmShahriar/\.github/\.github/workflows/release-go\.yml@' \
  --certificate-github-workflow-repository RhythmShahriar/soundcheck checksums.txt; do
  grep -qxF -- "$want" "$work/cosign-args" || { echo "FAIL cosign was not passed $want"; fail=1; }
done

fresh
echo "0000000000000000000000000000000000000000000000000000000000000000  rhythm-soundcheck_linux_amd64.tar.gz" > checksums.txt
expect fail "a rewritten checksums.txt" rhythm-soundcheck_linux_amd64.tar.gz

fresh
printf 'tampered\n' >> rhythm-soundcheck_linux_amd64.tar.gz
expect fail "a changed archive" rhythm-soundcheck_linux_amd64.tar.gz

fresh
printf 'unlisted\n' > rhythm-soundcheck_linux_arm64.tar.gz
expect fail "an archive not in checksums.txt" rhythm-soundcheck_linux_arm64.tar.gz

fresh
rm checksums.txt.sigstore.json
expect fail "a missing signature" rhythm-soundcheck_linux_amd64.tar.gz

# macOS: no sha256sum on PATH, only shasum. The fake shasum stands in
# for the real one and records that it was used.
mkdir "$work/mac"
for tool in bash awk cat; do ln -s "$(command -v "$tool")" "$work/mac/$tool"; done
ln -s "$work/bin/cosign" "$work/mac/cosign"
cat > "$work/mac/shasum" <<FAKE
#!$(command -v bash)
[ "\$1 \$2" = "-a 256" ] || exit 9
shift 2
: > "$work/shasum-used"
exec $(command -v sha256sum) "\$@"
FAKE
chmod +x "$work/mac/shasum"
mac() { # mac pass|fail NAME ARCHIVE
  rm -f "$work/shasum-used"
  if PATH="$work/mac" "$here/verify-release.sh" "$3" > "$work/out" 2>&1; then got=pass; else got=fail; fi
  if [ "$got" = "$1" ] && [ -e "$work/shasum-used" ]; then echo "ok   $2"; else echo "FAIL $2: got $got"; cat "$work/out"; fail=1; fi
}
fresh
mac pass "shasum when sha256sum is missing" rhythm-soundcheck_linux_amd64.tar.gz
fresh
printf 'tampered\n' >> rhythm-soundcheck_linux_amd64.tar.gz
mac fail "shasum refuses a changed archive" rhythm-soundcheck_linux_amd64.tar.gz

exit "$fail"
