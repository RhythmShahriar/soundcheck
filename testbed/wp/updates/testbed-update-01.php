<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name: Testbed Reading Time
 * Description: Adds a reading-time class to posts. One of the ten plugins behind the wp-update scenarios: reset.sh installs it at 1.0 and builds a 1.1 zip that testbed-updates.php offers as an update.
 * Version: @VERSION@
 */

defined( 'ABSPATH' ) || exit;

// reset.sh writes the version into each zip it builds.
const TESTBED_UPDATE_01_VERSION = '@VERSION@';

add_filter(
	'body_class',
	function ( array $classes ): array {
		if ( is_singular( 'post' ) ) {
			$classes[] = 'tb-update-01';
		}
		return $classes;
	}
);
