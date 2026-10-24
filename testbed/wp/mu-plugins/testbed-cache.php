<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name: Testbed page cache
 * Description: A tiny full-page cache, standing in for WP Super Cache or a CDN. Anonymous GET requests for pages are cached for ten minutes and answered with X-Cache: HIT and an Age header. Good state: the cart and checkout bypass the cache, and logged-in visitors neither read nor fill it. Scenario s18 broken: the cart is cached. Scenario s19 broken: pages rendered for a logged-in user are stored and served to everyone. Scenario s30 broken: one page's copy is a day and a half old and out of date.
 */

defined( 'ABSPATH' ) || exit;

( function () {
	if ( 'cli' === PHP_SAPI || 'GET' !== ( $_SERVER['REQUEST_METHOD'] ?? '' ) || ! empty( $_SERVER['QUERY_STRING'] ) ) {
		return;
	}
	$path = (string) parse_url( $_SERVER['REQUEST_URI'] ?? '/', PHP_URL_PATH );
	if ( str_starts_with( $path, '/wp-' ) || str_contains( $path, '.php' ) ) {
		return; // admin, login, REST, files
	}

	$logged_in = false;
	foreach ( array_keys( $_COOKIE ) as $name ) {
		if ( str_starts_with( $name, 'wordpress_logged_in_' ) ) {
			$logged_in = true;
		}
	}
	$private = in_array( rtrim( $path, '/' ), array( '/cart', '/checkout', '/my-account' ), true );

	if ( $private && ! testbed_broken( 's18' ) ) {
		header( 'X-Cache: BYPASS' );
		header( 'Cache-Control: no-cache, must-revalidate, max-age=0, no-store, private' );
		return;
	}
	if ( $logged_in && ! testbed_broken( 's19' ) ) {
		header( 'X-Cache: BYPASS' );
		return;
	}

	$dir  = sys_get_temp_dir() . '/testbed-cache';
	$file = $dir . '/' . md5( ( $_SERVER['HTTP_HOST'] ?? '' ) . $path . '|' . testbed_state_hash() ) . '.json';
	$ttl  = 600;
	// s30 broken: this page's copy is kept for two days and was made a day
	// and a half ago, before the roasting day changed, and nothing purges
	// it. The Age header counts the extra 36 hours.
	$stale = testbed_broken( 's30' ) && '/s30-stale' === rtrim( $path, '/' );
	$older = 0;
	if ( $stale ) {
		$ttl   = 2 * DAY_IN_SECONDS;
		$older = 36 * HOUR_IN_SECONDS;
	}

	if ( ! $logged_in && is_file( $file ) ) {
		$entry = json_decode( (string) file_get_contents( $file ), true );
		$age   = time() - (int) ( $entry['time'] ?? 0 );
		if ( is_array( $entry ) && $age < $ttl ) {
			header( 'Content-Type: ' . $entry['type'] );
			header( 'X-Cache: HIT' );
			header( 'Age: ' . ( $age + $older ) );
			header( 'Cache-Control: public, max-age=0, s-maxage=' . $ttl );
			echo $entry['body']; // phpcs:ignore WordPress.Security.EscapeOutput -- cached page
			exit;
		}
	}

	if ( $stale ) {
		define( 'TESTBED_CACHE_FILL', true ); // [testbed_roast_day] renders the old day
	}
	header( 'X-Cache: MISS' );
	header( 'Cache-Control: public, max-age=0, s-maxage=' . $ttl );
	ob_start(
		function ( $html ) use ( $dir, $file ) {
			$type = 'text/html; charset=UTF-8';
			foreach ( headers_list() as $h ) {
				if ( 0 === stripos( $h, 'content-type:' ) ) {
					$type = trim( substr( $h, 13 ) );
				}
			}
			if ( 200 === http_response_code() && '' !== trim( $html ) && str_starts_with( $type, 'text/html' ) ) {
				if ( ! is_dir( $dir ) ) {
					mkdir( $dir, 0700, true );
				}
				file_put_contents( $file, wp_json_encode( array( 'time' => time(), 'type' => $type, 'body' => $html ) ), LOCK_EX );
			}
			return $html;
		}
	);
} )();
