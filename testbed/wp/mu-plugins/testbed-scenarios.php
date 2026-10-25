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

// s06: an "optimization" setting deregisters jQuery while the theme still
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

// s09: the "Spring offer" page was deleted while other pages still link to it.
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

// s23: a PHP upgrade or a memory-hungry update breaks every page at once:
// "Allowed memory size of 33554432 bytes exhausted" on the front end, so
// WordPress shows its critical error screen everywhere.
add_action(
	'template_redirect',
	function () {
		if ( testbed_broken( 's23' ) ) {
			ini_set( 'memory_limit', '32M' ); // phpcs:ignore WordPress.PHP.IniSet
			$GLOBALS['testbed_ballast'] = str_repeat( 'x', 64 * 1024 * 1024 );
		}
	},
	2
);

// s26: a staging robots.txt reaches production: "Disallow: /".
add_filter(
	'robots_txt',
	function ( $output ) {
		return testbed_broken( 's26' ) ? "User-agent: *\nDisallow: /\n" : $output;
	},
	100
);

// s27: the SEO plugin's tags. In the good state every page has a
// canonical (WordPress core), hreflang alternates, Open Graph tags and
// JSON-LD. An SEO plugin update drops all of them in the broken state.
add_action(
	'wp',
	function () {
		if ( testbed_broken( 's27' ) ) {
			remove_action( 'wp_head', 'rel_canonical' );
		}
	}
);
add_action(
	'wp_head',
	function () {
		if ( testbed_broken( 's27' ) || ! is_singular() ) {
			return;
		}
		$url   = get_permalink();
		$title = wp_get_document_title();
		printf( "<link rel=\"alternate\" hreflang=\"en-gb\" href=\"%s\">\n", esc_url( $url ) );
		printf( "<link rel=\"alternate\" hreflang=\"x-default\" href=\"%s\">\n", esc_url( $url ) );
		printf( "<meta property=\"og:type\" content=\"website\">\n<meta property=\"og:title\" content=\"%s\">\n<meta property=\"og:url\" content=\"%s\">\n", esc_attr( $title ), esc_url( $url ) );
		$ld = array(
			'@context'  => 'https://schema.org',
			'@type'     => 'WebPage',
			'name'      => $title,
			'url'       => $url,
			'publisher' => array(
				'@type' => 'Organization',
				'name'  => get_bloginfo( 'name' ),
			),
		);
		echo '<script type="application/ld+json">' . wp_json_encode( $ld ) . "</script>\n";
	}
);

// s28: the sitemap breaks: /wp-sitemap.xml and its sub-sitemaps answer 500.
add_action(
	'init',
	function () {
		$path = (string) wp_parse_url( $_SERVER['REQUEST_URI'] ?? '', PHP_URL_PATH );
		if ( testbed_broken( 's28' ) && str_starts_with( $path, '/wp-sitemap' ) ) {
			status_header( 500 );
			nocache_headers();
			header( 'Content-Type: text/plain; charset=utf-8' );
			echo "Internal Server Error\n";
			exit;
		}
	},
	0
);

// s29: a redirect plugin's new rule sends the page to the home page.
add_action(
	'template_redirect',
	function () {
		if ( testbed_broken( 's29' ) && is_page( 's29-redirect' ) ) {
			wp_safe_redirect( home_url( '/' ), 301 );
			exit;
		}
	},
	1
);

// s30: [testbed_roast_day] says this week's roasting day. The broken page
// cache (testbed-cache.php) keeps a copy from before the day moved, so
// cached visitors see the old day; that copy is a day and a half old and
// carries an expired form nonce.
add_shortcode(
	'testbed_roast_day',
	function () {
		$day = ( testbed_broken( 's30' ) && defined( 'TESTBED_CACHE_FILL' ) ) ? 'Monday' : 'Tuesday';
		return '<p class="roast-day">This week we roast on ' . $day . '.</p>';
	}
);

// s46: an Elementor add-on is deactivated, so a section built with its
// widget shows Elementor's placeholder instead of the carousel.
add_filter(
	'the_content',
	function ( $content ) {
		if ( testbed_broken( 's46' ) && is_page( 's46-widgets' ) ) {
			$content = preg_replace(
				'#<div class="tb-carousel">.*?</div>#s',
				'<div class="elementor-element elementor-widget-empty"><div class="elementor-widget-container">Widget "tb-carousel" not found.</div></div>',
				$content
			);
		}
		return $content;
	}
);

// Mail for the Echo scenario (s33): with TESTBED_SMTP=host:port, WordPress
// sends through that SMTP server (Echo's), so Soundcheck --echo can see
// each message. Without it, PHP's mail() is used and nothing arrives.
add_action(
	'phpmailer_init',
	function ( $mailer ) {
		$smtp = getenv( 'TESTBED_SMTP' );
		if ( $smtp ) {
			[ $host, $port ] = array_pad( explode( ':', $smtp, 2 ), 2, '1025' );
			$mailer->isSMTP();
			$mailer->Host        = $host;
			$mailer->Port        = (int) $port;
			$mailer->SMTPAuth    = false;
			$mailer->SMTPAutoTLS = false;
		}
	}
);
