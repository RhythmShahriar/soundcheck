<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Testbed theme setup.
 */

defined( 'ABSPATH' ) || exit;

if ( ! function_exists( 'testbed_broken' ) ) {
	function testbed_broken( string $id ): bool {
		return false;
	}
}

add_action(
	'after_setup_theme',
	function () {
		add_theme_support( 'title-tag' ); // s17 removes this in the testbed-scenarios mu-plugin
		register_nav_menus( array( 'primary' => 'Primary' ) );
	}
);

add_action(
	'wp_enqueue_scripts',
	function () {
		$v = wp_get_theme()->get( 'Version' );
		// s24 broken: theme 2.0 enqueues a renamed stylesheet that wasn't
		// deployed, so every page loads unstyled.
		$css = testbed_broken( 's24' ) ? get_theme_file_uri( 'style-2.0.css' ) : get_stylesheet_uri();
		wp_enqueue_style( 'testbed', $css, array(), $v );
		wp_enqueue_script( 'jquery' ); // footer.php runs inline jQuery code (s06)

		// s12 broken: menu.js 2.0 looks for .js-menu-toggle, but the header
		// still renders .menu-toggle, so the Browse button does nothing.
		$menu = testbed_broken( 's12' ) ? 'menu-2.0.js' : 'menu.js';
		wp_enqueue_script( 'testbed-menu', get_theme_file_uri( 'js/' . $menu ), array(), $v, array( 'in_footer' => true ) );

		// s13 broken: wishlist 2.0 calls a helper that was never shipped.
		$wishlist = testbed_broken( 's13' ) ? 'wishlist-2.0.js' : 'wishlist.js';
		wp_enqueue_script( 'testbed-wishlist', get_theme_file_uri( 'js/' . $wishlist ), array(), $v, array( 'in_footer' => true ) );
	}
);

// No favicon request: keeps every page's request list to what the page itself loads.
add_action(
	'wp_head',
	function () {
		echo "<link rel=\"icon\" href=\"data:,\">\n";
	},
	1
);

// No speculative prefetching: page loads stay exactly what the page asked for.
add_filter( 'wp_speculation_rules_configuration', '__return_null' );
