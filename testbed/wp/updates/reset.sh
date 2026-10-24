#!/bin/sh
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Puts the wp-update scenarios (s47, s48) back at their start: builds a
# 1.0 and a 1.1 zip of every testbed-update-* plugin in this folder, then
# installs and activates each one at 1.0. The mu-plugin
# testbed-updates.php offers the 1.1 zips as plugin updates, so
# `wp plugin list --update=available` lists all ten again.
#
# Runs in the WP-CLI container, where docker-compose.yml mounts this
# folder at /testbed-updates: by wp/seed.sh, by `make wp-update-reset`
# and by the benchmark before and after each wp-update scenario.
set -eu
cd /var/www/html

src=$(cd "$(dirname "$0")" && pwd)
dist=wp-content/testbed-updates
mkdir -p "$dist"

for f in "$src"/testbed-update-*.php; do
	slug=$(basename "$f" .php)
	for version in 1.0 1.1; do
		# The plugin file says @VERSION@ wherever the version goes.
		# shellcheck disable=SC2016 # PHP code: PHP expands its variables
		php -r '
			[, $src, $slug, $version, $zip] = $argv;
			$z = new ZipArchive();
			if ( true !== $z->open( $zip, ZipArchive::CREATE | ZipArchive::OVERWRITE ) ) {
				fwrite( STDERR, "reset.sh: cannot write $zip\n" );
				exit( 1 );
			}
			$z->addFromString( "$slug/$slug.php", str_replace( "@VERSION@", $version, file_get_contents( $src ) ) );
			exit( $z->close() ? 0 : 1 );
		' "$f" "$slug" "$version" "$dist/$slug.$version.zip"
	done
done

wp plugin install "$dist"/testbed-update-*.1.0.zip --force --activate >/dev/null
echo "testbed: wp-update plugins reset to 1.0 ($(find "$dist" -name '*.1.1.zip' | wc -l | tr -d ' ') updates offered)"
