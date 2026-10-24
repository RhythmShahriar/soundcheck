<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name: Testbed Pricing Tables
 * Description: The [testbed_pricing] shortcode. Scenario s01 broken: version 2.0 calls a renderer it no longer defines, a PHP fatal error on the one page that uses the shortcode.
 * Version: 1.0.0
 */

defined( 'ABSPATH' ) || exit;

function testbed_pricing_render(): string {
	$plans = array(
		'Taster'  => '£9 a month: one 250 g bag',
		'Regular' => '£16 a month: two 250 g bags',
		'Office'  => '£45 a month: 2 kg of whole beans',
	);
	$rows  = '';
	foreach ( $plans as $name => $what ) {
		$rows .= '<tr><th>' . esc_html( $name ) . '</th><td>' . esc_html( $what ) . '</td></tr>';
	}
	return '<table class="tb-pricing">' . $rows . '</table>';
}

add_shortcode(
	'testbed_pricing',
	function () {
		if ( testbed_broken( 's01' ) ) {
			return testbed_pricing_render_v2(); // undefined: Fatal error
		}
		return testbed_pricing_render();
	}
);
