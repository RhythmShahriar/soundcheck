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
 * A fingerprint of every scenario's state. The page cache keys on it, so
 * toggling a scenario never serves a page rendered in the other state.
 */
function testbed_state_hash(): string {
	$files = glob( testbed_state_dir() . '/s*' ) ?: array();
	sort( $files );
	return md5( implode( ',', array_map( 'basename', $files ) ) );
}
