#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Tests verify-pins.sh offline with a fake go that records its arguments
# and writes FAKE_PINS as pingen's output. CI runs it; so can you:
# scripts/verify-pins-test.sh
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/repo/browser"

cat > "$work/bin/go" <<'FAKE'
#!/usr/bin/env bash
printf '%s\n' "$@" > "$FAKE_ARGS"
while [ $# -gt 0 ]; do
  [ "$1" = -o ] && cp "$FAKE_PINS" "$2"
  shift
done
FAKE
chmod +x "$work/bin/go"
export PATH="$work/bin:$PATH" FAKE_ARGS="$work/args" FAKE_PINS="$work/regenerated.go"

pins() { # pins VERSION SHA
  printf 'package browser\n\nconst cftVersion = "%s"\n\nvar cftArchives = map[string]Archive{\n\t"linux64": {SHA256: "%s", Size: 1},\n}\n' "$1" "$2"
}

fail=0
expect() { # expect pass|fail NAME
  rm -f "$work/args"
  if (cd "$work/repo" && "$here/verify-pins.sh" -index http://127.0.0.1:20350) > "$work/out" 2>&1; then got=pass; else got=fail; fi
  if [ "$got" = "$1" ]; then echo "ok   $2"; else echo "FAIL $2: got $got"; sed 's/^/     /' "$work/out"; fail=1; fi
}

pins 153.0.8010.52 aaa > "$work/repo/browser/cft_pins.go"
pins 153.0.8010.52 aaa > "$work/regenerated.go"
expect pass "unchanged pins"
if grep -qx -- -version "$work/args" && grep -qx 153.0.8010.52 "$work/args" && grep -qx -- -index "$work/args"; then
  echo "ok   the committed version is re-hashed, not the current Stable"
else echo "FAIL the committed version is re-hashed, not the current Stable"; cat "$work/args"; fail=1; fi

pins 153.0.8010.52 bbb > "$work/regenerated.go"
expect fail "an archive that changed since it was pinned"

pins "" "" > "$work/repo/browser/cft_pins.go"
expect fail "no pinned version"
if grep -qF "run go generate ./browser" "$work/out" && [ ! -e "$work/args" ]; then
  echo "ok   no pins says how to make them"
else echo "FAIL no pins says how to make them"; fail=1; fi

exit "$fail"
