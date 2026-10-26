#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Updates the vendored axe-core to a release from npm, checked end to end.
#
#   scripts/update-axe.sh 4.15.0
#
# Run it from the repository root, by hand. Dependabot doesn't know the
# vendored file and must never update it: a new axe release changes the
# rules every run applies, so it needs a person to read the changelog
# and the regenerated rule lists.
#
# It asks the npm registry for the release's tarball and its SRI
# (sha512) integrity, downloads the tarball and checks it against the
# integrity with openssl. It then installs the same release in a scratch
# package and runs npm audit signatures, which must report a verified
# registry signature and a verified provenance attestation: the tarball
# was built from dequelabs/axe-core by its release workflow. Only then
# does it copy axe.min.js and its licenses to checks/a11y/vendor/ and
# write the new version and SHA-256 into the sources, the tests and
# sboms/axe-core.cdx.json. It prints the body of the commit message.
set -euo pipefail

die() {
  echo "update-axe: $*" >&2
  exit 1
}

version=${1:-}
[[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "usage: scripts/update-axe.sh <version>, e.g. 4.15.0"
case ${GITHUB_ACTOR:-} in
dependabot*) die "the vendored axe-core is updated by hand, never by Dependabot" ;;
esac
for tool in npm openssl curl tar node; do
  command -v "$tool" >/dev/null || die "$tool is needed"
done

vendor=checks/a11y/vendor
source_go=checks/a11y/a11y.go
test_go=checks/a11y/a11y_test.go
sbom=sboms/axe-core.cdx.json
[ -f "$vendor/axe.min.js" ] || die "run it from the repository root"
old=$(sed -n 's/^const axeVersion = "\(.*\)"$/\1/p' "$source_go")
[ -n "$old" ] || die "$source_go names no axeVersion"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

integrity=$(npm view "axe-core@$version" dist.integrity)
tarball=$(npm view "axe-core@$version" dist.tarball)
[[ $integrity == sha512-* ]] || die "npm gives no sha512 integrity for axe-core@$version: $integrity"
[ "$tarball" = "https://registry.npmjs.org/axe-core/-/axe-core-$version.tgz" ] || die "unexpected tarball URL: $tarball"

curl -fsSL --proto '=https' --tlsv1.2 -o "$work/axe-core.tgz" "$tarball"
got="sha512-$(openssl dgst -sha512 -binary "$work/axe-core.tgz" | openssl base64 -A)"
[ "$got" = "$integrity" ] || die "the tarball's integrity is $got, npm says $integrity"

mkdir "$work/pkg"
(
  cd "$work/pkg"
  npm init -y >/dev/null
  npm install --ignore-scripts --no-audit --no-fund --save-exact "axe-core@$version" >/dev/null
  npm audit signatures
) >"$work/audit.txt" 2>&1 || {
  cat "$work/audit.txt" >&2
  die "npm audit signatures failed"
}
grep -q "has a verified registry signature" "$work/audit.txt" || die "no verified registry signature: $(cat "$work/audit.txt")"
grep -q "has a verified attestation" "$work/audit.txt" || die "no verified provenance attestation: $(cat "$work/audit.txt")"
locked=$(node -e 'const l = require(process.argv[1]); process.stdout.write(l.packages["node_modules/axe-core"].integrity)' "$work/pkg/package-lock.json")
[ "$locked" = "$integrity" ] || die "npm installed $locked, not the tarball checked ($integrity)"

tar -xzf "$work/axe-core.tgz" -C "$work" package/axe.min.js package/LICENSE package/LICENSE-3RD-PARTY.txt
head -n 1 "$work/package/axe.min.js" | grep -qx "/\*! axe v$version" || die "axe.min.js doesn't start with /*! axe v$version"
cp "$work/package/axe.min.js" "$work/package/LICENSE" "$work/package/LICENSE-3RD-PARTY.txt" "$vendor/"

sha256=$(openssl dgst -sha256 -r "$vendor/axe.min.js" | cut -d' ' -f1)
sri256="sha256-$(openssl dgst -sha256 -binary "$vendor/axe.min.js" | openssl base64 -A)"

# replace FILE SED-SCRIPT edits FILE in place the same way on Linux and macOS.
replace() {
  sed -e "$2" "$1" >"$work/edit"
  cat "$work/edit" >"$1"
}
old_re=${old//./\\.}
replace "$source_go" "s/^const axeVersion = \"$old_re\"$/const axeVersion = \"$version\"/"
replace "$test_go" "s/^const axeSHA256 = \"[0-9a-f]*\"$/const axeSHA256 = \"$sha256\"/; s|sha256-[A-Za-z0-9+/=]*|$sri256|; s/axe-core $old_re/axe-core $version/g"
node - "$sbom" "$version" "$sha256" <<'JS'
const fs = require('fs');
const [file, version, sha256] = process.argv.slice(2);
const bom = JSON.parse(fs.readFileSync(file, 'utf8'));
const ref = `pkg:npm/axe-core@${version}`;
const old = bom.components[0]['bom-ref'];
Object.assign(bom.components[0], {
  'bom-ref': ref,
  version,
  purl: ref,
  hashes: [{alg: 'SHA-256', content: sha256}],
  externalReferences: [
    {type: 'distribution', url: `https://registry.npmjs.org/axe-core/-/axe-core-${version}.tgz`},
    {type: 'vcs', url: 'https://github.com/dequelabs/axe-core'},
    {type: 'license', url: `https://github.com/dequelabs/axe-core/blob/v${version}/LICENSE`},
  ],
});
bom.version += 1;
for (const d of bom.dependencies) {
  if (d.ref === old) d.ref = ref;
  d.dependsOn = d.dependsOn.map(r => (r === old ? ref : r));
}
fs.writeFileSync(file, JSON.stringify(bom, null, 2) + '\n');
JS

cat <<EOF
update-axe: $vendor now holds axe-core $version (was $old). Next:
  go test ./checks/a11y -run TestRulesRecorded -update   # the rule lists per WCAG version
  update the version in README.md, read the diff of the goldens and axe's changelog
  keep compare/baseline_rules.go as it is: it's what 1.2 snapshots ran

Commit message body:

axe-core $version from $tarball
integrity: $integrity
axe.min.js sha256: $sha256
provenance: verified (npm audit signatures)
EOF
