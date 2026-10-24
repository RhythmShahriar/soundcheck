<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name: Testbed scenarios
 * Description: The broken states that would come from a setting, a host change or a bad deploy rather than from one plugin. Each block names its scenario; theme and plugin scenarios live next to the code they break.
 */

defined( 'ABSPATH' ) || exit;

// s04: a page answers 200 with an empty body (a fatal error with
// display_errors off and no fatal handler, as on many shared hosts).
add_action(
	'template_redirect',
	function () {
		if ( testbed_broken( 's04' ) && is_page( 's04-blank' ) ) {
			while ( ob_get_level() > 0 ) {
				ob_end_clean();
			}
			status_header( 200 );
			exit;
		}
	},
	0
);

// s06: an "optimization" setting deregisters jQuery, but the theme still
// runs inline jQuery code in its footer.
add_action(
	'wp_enqueue_scripts',
	function () {
		if ( testbed_broken( 's06' ) ) {
			wp_deregister_script( 'jquery' );
		}
	},
	100
);

// s09: the "Spring offer" page was deleted, but other pages still link to it.
add_action(
	'template_redirect',
	function () {
		global $wp_query;
		if ( testbed_broken( 's09' ) && is_page( 'spring-offer' ) ) {
			$wp_query->set_404();
			status_header( 404 );
			nocache_headers();
		}
	},
	1
);

// s10: after a migration the content points at an image size that was
// never regenerated, so the image 404s.
add_filter(
	'the_content',
	function ( $content ) {
		if ( testbed_broken( 's10' ) ) {
			$content = str_replace( 'assets/team.svg', 'assets/team-1024x683.svg', $content );
		}
		return $content;
	}
);

// s14: a theme update drops the hero section and moves the logo to the
// right of the header.
add_filter(
	'body_class',
	function ( $classes ) {
		if ( testbed_broken( 's14' ) ) {
			$classes[] = 'tb-layout-v2';
		}
		return $classes;
	}
);

// s15: a page builder gives the promo strip a fixed 1200px width, so the
// page no longer fits a phone.
add_action(
	'wp_head',
	function () {
		if ( testbed_broken( 's15' ) ) {
			echo "<style id=\"tb-promo-fixed\">.promo-strip{width:1200px}</style>\n";
		}
	}
);

// s16: "Discourage search engines from indexing this site" gets switched
// on (Settings → Reading), as an SEO plugin or a staging copy would.
add_filter(
	'pre_option_blog_public',
	function ( $value ) {
		return testbed_broken( 's16' ) ? '0' : $value;
	}
);

// s17: a theme update drops add_theme_support( 'title-tag' ), so pages
// lose their <title>.
add_action(
	'after_setup_theme',
	function () {
		if ( testbed_broken( 's17' ) ) {
			remove_theme_support( 'title-tag' );
		}
	},
	20
);
