<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name: Testbed Contact Extras
 * Description: Marks the contact page. Version 1.1 is the planted break of the wp-update scenarios (s47, s48): its new form layout calls a renderer the release left out, a PHP fatal error on the contact page only.
 * Version: @VERSION@
 */

defined( 'ABSPATH' ) || exit;

// reset.sh writes the version into each zip it builds.
const TESTBED_UPDATE_07_VERSION = '@VERSION@';

add_filter(
	'body_class',
	function ( array $classes ): array {
		if ( is_page( 'contact' ) ) {
			$classes[] = 'tb-update-07';
		}
		return $classes;
	}
);

add_filter(
	'the_content',
	function ( string $content ): string {
		if ( ! is_page( 'contact' ) || version_compare( TESTBED_UPDATE_07_VERSION, '1.1', '<' ) ) {
			return $content;
		}
		// 1.1 moved the form layout to a renderer that isn't in the
		// release: Fatal error, Call to undefined function.
		return testbed_update_07_form_layout_v2( $content );
	}
);
