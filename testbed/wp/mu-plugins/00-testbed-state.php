<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name: Testbed state
 * Description: Tells the testbed's theme and plugins which scenarios are broken. A scenario is broken while <state>/<id> exists, where <state> is TESTBED_STATE or wp-content/testbed-state; testbed/toggle.sh creates and removes those files.
 */

defined( 'ABSPATH' ) || exit;

/**
 * The folder that holds the state files: TESTBED_STATE, or
 * wp-content/testbed-state where the Docker stack mounts testbed/state.
 */
function testbed_state_dir(): string {
	$dir = getenv( 'TESTBED_STATE' ) ?: WP_CONTENT_DIR . '/testbed-state';
	return rtrim( $dir, '/' );
}

/** Reports whether scenario $id (s01, s02…) is in its broken state. */
function testbed_broken( string $id ): bool {
	return file_exists( testbed_state_dir() . '/' . $id );
}

/**
 * Adds one to the counter <state>/<name>.txt and returns the new count,
 * or -1 when the file can't be written. The shop counts placed orders in
 * orders.txt; the benchmark's orders harness fails a scenario whose
 * keyboard walk or key flow changed it (s70).
 */
function testbed_bump( string $name ): int {
	$f = @fopen( testbed_state_dir() . '/' . $name . '.txt', 'c+' ); // phpcs:ignore WordPress.WP.AlternativeFunctions
	if ( ! $f ) {
		error_log( "testbed: can't write the $name counter in " . testbed_state_dir() ); // phpcs:ignore WordPress.PHP.DevelopmentFunctions
		return -1;
	}
	flock( $f, LOCK_EX );
	$n = (int) stream_get_contents( $f ) + 1;
	ftruncate( $f, 0 );
	rewind( $f );
	fwrite( $f, $n . "\n" ); // phpcs:ignore WordPress.WP.AlternativeFunctions
	flock( $f, LOCK_UN );
	fclose( $f ); // phpcs:ignore WordPress.WP.AlternativeFunctions
	return $n;
}

/**
 * The canaries the benchmark wrote into <state>/canaries (name=value per
 * line), for s68. They are made at run time and never committed: a value
 * that shows up in any of Soundcheck's files was leaked. Without the
 * file there are none.
 */
function testbed_canaries(): array {
	$out = array();
	foreach ( @file( testbed_state_dir() . '/canaries', FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES ) ?: array() as $line ) {
		[ $k, $v ] = array_pad( explode( '=', $line, 2 ), 2, '' );
		$out[ trim( $k ) ] = trim( $v );
	}
	return $out;
}

/**
 * A fingerprint of every scenario's state. The page cache keys on it, so
 * toggling a scenario never serves a page rendered in the other state.
 * The wp-update scenarios (s47, s48) change the site by updating plugins
 * rather than through a state file, so the installed version of each
 * testbed-update-* plugin counts too.
 */
function testbed_state_hash(): string {
	$files = glob( testbed_state_dir() . '/s[0-9][0-9]' ) ?: array();
	sort( $files );
	$parts = array_map( 'basename', $files );
	foreach ( glob( WP_PLUGIN_DIR . '/testbed-update-*/testbed-update-*.php' ) ?: array() as $plugin ) {
		$head = (string) file_get_contents( $plugin, false, null, 0, 1024 );
		preg_match( '/^\s*\*\s*Version:\s*(\S+)/m', $head, $m );
		$parts[] = basename( $plugin, '.php' ) . '@' . ( $m[1] ?? '' );
	}
	return md5( implode( ',', $parts ) );
}
