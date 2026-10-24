<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name: Testbed Widgets
 * Description: Front-end widgets that load data in the browser: a store locator (s05), a stock checker over admin-ajax (s07) and reviews over the REST API (s08).
 * Version: 1.0.0
 */

defined( 'ABSPATH' ) || exit;

add_action(
	'wp_enqueue_scripts',
	function () {
		// s05 broken: locator 2.0 throws while starting up.
		$locator = testbed_broken( 's05' ) ? 'locator-2.0.js' : 'locator.js';
		wp_enqueue_script( 'testbed-locator', plugins_url( 'js/' . $locator, __FILE__ ), array(), '1.0.0', array( 'in_footer' => true ) );
		wp_enqueue_script( 'testbed-data-widgets', plugins_url( 'js/widgets.js', __FILE__ ), array(), '1.0.0', array( 'in_footer' => true ) );
		wp_localize_script(
			'testbed-data-widgets',
			'tbWidgets',
			array(
				'ajax' => admin_url( 'admin-ajax.php' ),
				'rest' => rest_url( 'testbed/v1/reviews' ),
			)
		);
	}
);

// Stock checker (s07): broken, the endpoint fails with a 500.
function testbed_stock() {
	if ( testbed_broken( 's07' ) ) {
		wp_send_json_error( array( 'message' => 'Stock service unavailable' ), 500 );
	}
	wp_send_json_success( array( 'product' => sanitize_key( $_GET['product'] ?? '' ), 'stock' => 42 ) ); // phpcs:ignore WordPress.Security.NonceVerification
}
add_action( 'wp_ajax_testbed_stock', 'testbed_stock' );
add_action( 'wp_ajax_nopriv_testbed_stock', 'testbed_stock' );

// Reviews (s08): broken, a plugin update renamed the route, so the widget's
// request to testbed/v1/reviews gets the REST API's 404.
add_action(
	'rest_api_init',
	function () {
		$route = testbed_broken( 's08' ) ? '/customer-reviews' : '/reviews';
		register_rest_route(
			'testbed/v1',
			$route,
			array(
				'methods'             => 'GET',
				'permission_callback' => '__return_true',
				'callback'            => function () {
					return array(
						array( 'author' => 'Priya', 'text' => 'The Ethiopian roast is wonderful.' ),
						array( 'author' => 'Tom', 'text' => 'Fast delivery, great beans.' ),
					);
				},
			)
		);
	}
);
