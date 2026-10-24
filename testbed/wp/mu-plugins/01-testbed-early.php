<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name: Testbed early failures
 * Description: Site-wide failures that WordPress hits before it renders anything: maintenance mode left on (s21) and a lost database connection (s22). It loads right after the state plugin and before the page cache, the way the real failures stop WordPress before any plugin runs.
 */

defined( 'ABSPATH' ) || exit;

// WP-CLI (the seed) keeps working in every state.
if ( 'cli' !== PHP_SAPI ) {
	// s21: an update failed half way and left .maintenance behind, so every
	// request gets WordPress's maintenance screen (wp_maintenance()).
	if ( testbed_broken( 's21' ) ) {
		header( 'Retry-After: 600' );
		wp_die(
			esc_html__( 'Briefly unavailable for scheduled maintenance. Check back in a minute.' ),
			esc_html__( 'Maintenance' ),
			503
		);
	}

	// s22: the database server is gone, so WordPress shows its
	// "Error establishing a database connection" page (dead_db()).
	if ( testbed_broken( 's22' ) ) {
		wp_die( '<h1>Error establishing a database connection</h1>', 'Database Error', 500 );
	}
}
