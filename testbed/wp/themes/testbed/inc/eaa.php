<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * The theme's accessibility scenarios (s51 to s80). Each block names its
 * scenario. The good state passes --eaa (step zero in testbed/README.md);
 * each broken state takes away one thing a keyboard or screen reader
 * user needs, the way a theme or plugin update does. The header's own
 * switches (s55, s56, s80) are in header.php.
 */

defined( 'ABSPATH' ) || exit;

add_action(
	'wp_enqueue_scripts',
	function () {
		wp_enqueue_script( 'testbed-dialog', get_theme_file_uri( 'js/dialog.js' ), array(), wp_get_theme()->get( 'Version' ), array( 'in_footer' => true ) );
	}
);

// s54: the theme's 1.1 "clean look" removes every focus outline.
// s58: its menu rewrite opens submenus on hover only (body class, see
// style.css), so a keyboard user never sees them.
add_action(
	'wp_head',
	function () {
		if ( testbed_broken( 's54' ) ) {
			echo "<style id=\"tb-no-outline\">*:focus{outline:none !important}</style>\n";
		}
	},
	20
);
add_filter(
	'body_class',
	function ( $classes ) {
		if ( testbed_broken( 's58' ) ) {
			$classes[] = 'tb-hover-menus';
		}
		return $classes;
	}
);

// The primary menu's "Guides" item with its submenu (s58). It opens on
// hover and, in the good state, when focus is inside it (:focus-within),
// which is the keyboard path a link trigger needs.
add_filter(
	'wp_nav_menu_items',
	function ( $items, $args ) {
		if ( 'primary' !== ( $args->theme_location ?? '' ) ) {
			return $items;
		}
		return $items . '<li class="menu-item has-sub"><a href="' . esc_url( home_url( '/blog/' ) ) . '">Guides</a><ul class="sub-menu">'
			. '<li><a href="' . esc_url( home_url( '/how-to-brew-with-a-v60/' ) ) . '">Brewing with a V60</a></li>'
			. '<li><a href="' . esc_url( home_url( '/choosing-a-grinder/' ) ) . '">Choosing a grinder</a></li>'
			. '</ul></li>';
	},
	10,
	2
);

// s62: an SEO plugin's "clean head" option drops the lang attribute.
add_filter(
	'language_attributes',
	function ( $output ) {
		return testbed_broken( 's62' ) ? '' : $output;
	}
);

// s63: a media plugin update loses the gallery images' alt text.
add_filter(
	'the_content',
	function ( $content ) {
		if ( testbed_broken( 's63' ) && is_page( 's63-gallery' ) ) {
			$content = preg_replace( '/\salt="[^"]*"/', '', $content );
		}
		return $content;
	}
);

// [testbed_newsletter] (s51): a newsletter sign-up dialog. Good: closing
// it puts focus back on the button that opened it. Broken: its 2.0 close
// button removes itself as it closes the dialog, so focus is left on
// nothing (the document body).
add_shortcode(
	'testbed_newsletter',
	function () {
		$options = wp_json_encode( array( 'removeClose' => testbed_broken( 's51' ) ) );
		return '<p><button type="button" class="tb-button" id="news-open" aria-haspopup="dialog" aria-expanded="false" aria-controls="news">Get the newsletter</button></p>'
			. '<div class="tb-modal" id="news" role="dialog" aria-modal="true" aria-labelledby="news-title" hidden>'
			. '<h2 id="news-title">Our newsletter</h2><p>One email a month about new roasts.</p>'
			. '<label for="news-email">Email address</label><input id="news-email" name="email" type="email" autocomplete="email">'
			. '<p><button type="button" class="tb-button" id="news-close">No thanks</button></p></div>'
			. "<script>document.addEventListener('DOMContentLoaded', function () { testbedDialog('news-open', 'news', 'news-close', $options); });</script>";
	}
);

// [testbed_modal] (s52): a "Find your grind" dialog marked aria-modal.
// Good: Tab and Shift+Tab stay inside it. Broken: a rewrite dropped the
// containment, so Tab walks out into the page behind the modal.
add_shortcode(
	'testbed_modal',
	function () {
		$options = wp_json_encode( array( 'contain' => ! testbed_broken( 's52' ) ) );
		return '<p><button type="button" class="tb-button" id="grind-open" aria-haspopup="dialog" aria-expanded="false" aria-controls="grind">Find your grind</button></p>'
			. '<div class="tb-modal" id="grind" role="dialog" aria-modal="true" aria-labelledby="grind-title" hidden>'
			. '<h2 id="grind-title">Find your grind</h2>'
			. '<label for="grind-brewer">Your brewer</label><select id="grind-brewer" name="brewer"><option>V60</option><option>Cafetière</option><option>Espresso</option></select>'
			. '<p><button type="button" class="tb-button" id="grind-show">Show</button> <button type="button" class="tb-button" id="grind-close">Close</button></p></div>'
			. '<p><a href="' . esc_url( home_url( '/shop/' ) ) . '">Shop the beans</a></p>'
			. "<script>document.addEventListener('DOMContentLoaded', function () { var g = testbedDialog('grind-open', 'grind', 'grind-close', $options); document.getElementById('grind-show').addEventListener('click', g.hide); });</script>";
	}
);

// s73: an accessibility overlay plugin is installed. The testbed serves
// a stub of its script from the overlay's real plugin path
// (wp/plugins/pojo-accessibility, mounted in docker-compose.yml), so the
// page asks for nothing outside the testbed.
add_action(
	'wp_footer',
	function () {
		if ( testbed_broken( 's73' ) ) {
			echo '<script src="' . esc_url( content_url( '/plugins/pojo-accessibility/assets/js/app.js' ) ) . "\" defer></script>\n"; // phpcs:ignore WordPress.WP.EnqueuedResources
		}
	}
);
