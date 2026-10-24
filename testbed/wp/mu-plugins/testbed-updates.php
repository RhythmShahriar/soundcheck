<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name: Testbed plugin updates
 * Description: The update server for the wp-update scenarios (s47, s48). It offers each testbed-update-* plugin's newest zip in wp-content/testbed-updates/ (built by wp/updates/reset.sh) as a plugin update. It serves those zips under http://updates.testbed.invalid/ so WordPress and WP-CLI update, install and roll back the way they do from WordPress.org. Nothing leaves the container: WordPress.org is never contacted.
 */

defined( 'ABSPATH' ) || exit;

/** The made-up host the update packages are served from. */
const TESTBED_UPDATES_HOST = 'updates.testbed.invalid';

/** The folder reset.sh builds the zips in. */
function testbed_updates_dir(): string {
	return WP_CONTENT_DIR . '/testbed-updates';
}

/**
 * The versions the testbed has a zip for, by plugin slug, oldest first:
 * array( 'testbed-update-01' => array( '1.0', '1.1' ), … ).
 */
function testbed_update_packages(): array {
	$out = array();
	foreach ( glob( testbed_updates_dir() . '/testbed-update-*.zip' ) ?: array() as $zip ) {
		if ( preg_match( '/^(testbed-update-\d\d)\.(\d+\.\d+)\.zip$/', basename( $zip ), $m ) ) {
			$out[ $m[1] ][] = $m[2];
		}
	}
	foreach ( $out as &$versions ) {
		usort( $versions, 'version_compare' );
	}
	return $out;
}

/** Where WordPress downloads $slug at $version. */
function testbed_update_url( string $slug, string $version ): string {
	return 'http://' . TESTBED_UPDATES_HOST . "/plugin/$slug.$version.zip";
}

/** The installed version of a testbed-update plugin; '' when it isn't installed. */
function testbed_update_installed( string $slug ): string {
	$file = WP_PLUGIN_DIR . "/$slug/$slug.php";
	if ( ! is_file( $file ) ) {
		return '';
	}
	return (string) get_file_data( $file, array( 'Version' => 'Version' ) )['Version'];
}

/**
 * Adds the testbed's updates to the update_plugins transient: a plugin
 * older than its newest zip has an update, the others are up to date.
 * It runs both when WordPress stores the transient (after an update
 * check) and when it reads it, so a rollback shows the update again at
 * once, without waiting for the next check.
 */
function testbed_updates_offer( $transient ) {
	if ( ! is_object( $transient ) ) {
		$transient = new stdClass();
	}
	foreach ( array( 'response', 'no_update', 'checked' ) as $key ) {
		if ( ! isset( $transient->$key ) || ! is_array( $transient->$key ) ) {
			$transient->$key = array();
		}
	}
	foreach ( testbed_update_packages() as $slug => $versions ) {
		$installed = testbed_update_installed( $slug );
		if ( '' === $installed ) {
			continue;
		}
		$latest = end( $versions );
		$file   = "$slug/$slug.php";
		$item   = (object) array(
			'id'          => 'testbed/' . $slug,
			'slug'        => $slug,
			'plugin'      => $file,
			'new_version' => $latest,
			'url'         => 'http://' . TESTBED_UPDATES_HOST . "/plugin/$slug/",
			'package'     => testbed_update_url( $slug, $latest ),
		);

		$transient->checked[ $file ] = $installed;
		if ( version_compare( $installed, $latest, '<' ) ) {
			$transient->response[ $file ] = $item;
			unset( $transient->no_update[ $file ] );
		} else {
			$transient->no_update[ $file ] = $item;
			unset( $transient->response[ $file ] );
		}
	}
	return $transient;
}
add_filter( 'pre_set_site_transient_update_plugins', 'testbed_updates_offer' );
add_filter( 'site_transient_update_plugins', 'testbed_updates_offer' );

/**
 * Answers WP-CLI's plugin_information lookups for the testbed plugins.
 * `wp plugin install <slug> --version=1.0 --force`, the rollback
 * wp-update runs, starts here and then fetches <slug>.1.0.zip from the
 * same folder as download_link.
 */
add_filter(
	'plugins_api',
	function ( $result, $action, $args ) {
		$slug     = is_object( $args ) && isset( $args->slug ) ? (string) $args->slug : '';
		$packages = testbed_update_packages();
		if ( 'plugin_information' !== $action || ! isset( $packages[ $slug ] ) ) {
			return $result;
		}
		$latest   = end( $packages[ $slug ] );
		$versions = array();
		foreach ( $packages[ $slug ] as $v ) {
			$versions[ $v ] = testbed_update_url( $slug, $v );
		}
		return (object) array(
			'name'          => $slug,
			'slug'          => $slug,
			'version'       => $latest,
			'download_link' => testbed_update_url( $slug, $latest ),
			'versions'      => $versions,
		);
	},
	10,
	3
);

/**
 * Serves the zips for http(s)://updates.testbed.invalid/plugin/<slug>.<version>.zip
 * from wp-content/testbed-updates/ and keeps WordPress.org out of the
 * testbed: its update checks get an empty answer and every other request
 * to it fails at once. WP-CLI's plugin list and update check WordPress.org
 * first; without this they would reach the internet.
 */
add_filter(
	'pre_http_request',
	function ( $pre, $args, $url ) {
		$host = strtolower( (string) wp_parse_url( $url, PHP_URL_HOST ) );
		$path = (string) wp_parse_url( $url, PHP_URL_PATH );
		$file = $args['filename'] ?? null;
		$resp = function ( int $code, string $message, array $headers = array(), string $body = '' ) use ( $file ) {
			return array(
				'headers'  => $headers,
				'body'     => $body,
				'response' => array( 'code' => $code, 'message' => $message ),
				'cookies'  => array(),
				'filename' => $file,
			);
		};

		if ( 'wordpress.org' === $host || str_ends_with( $host, '.wordpress.org' ) || str_ends_with( $host, '.w.org' ) ) {
			if ( str_contains( $path, '/update-check/' ) ) {
				return $resp( 200, 'OK', array( 'content-type' => 'application/json' ), '{"plugins":[],"themes":[],"translations":[],"no_update":[]}' );
			}
			return new WP_Error( 'testbed_offline', 'The testbed never contacts WordPress.org.' );
		}
		if ( TESTBED_UPDATES_HOST !== $host ) {
			return $pre;
		}

		$name = basename( $path );
		$zip  = testbed_updates_dir() . '/' . $name;
		if ( ! preg_match( '/^testbed-update-\d\d\.\d+\.\d+\.zip$/', $name ) || ! is_file( $zip ) ) {
			return $resp( 404, 'Not Found' );
		}
		$body = '';
		if ( 'HEAD' !== ( $args['method'] ?? 'GET' ) ) {
			if ( ! empty( $args['stream'] ) && $file ) {
				copy( $zip, $file ); // download_url() streams into a temporary file
			} else {
				$body = (string) file_get_contents( $zip );
			}
		}
		return $resp( 200, 'OK', array( 'content-type' => 'application/zip', 'content-length' => (string) filesize( $zip ) ), $body );
	},
	10,
	3
);
