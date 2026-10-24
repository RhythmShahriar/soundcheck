<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name: Testbed state
 * Description: Tells the testbed's theme and plugins which scenarios are broken. A scenario is broken while wp-content/testbed-state/<id> exists; testbed/toggle.sh creates and removes those files.
 */

defined( 'ABSPATH' ) || exit;

/** Reports whether scenario $id (s01…s20) is in its broken state. */
function testbed_broken( string $id ): bool {
	return file_exists( WP_CONTENT_DIR . '/testbed-state/' . $id );
}

/**
 * A fingerprint of every scenario's state. The page cache keys on it, so
 * toggling a scenario never serves a page rendered in the other state.
 */
function testbed_state_hash(): string {
	$files = glob( WP_CONTENT_DIR . '/testbed-state/s*' ) ?: array();
	sort( $files );
	return md5( implode( ',', array_map( 'basename', $files ) ) );
}
