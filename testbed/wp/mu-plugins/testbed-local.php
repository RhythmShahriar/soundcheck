<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name: Testbed stays local
 * Description: Keeps every testbed page free of requests to outside hosts, logged in or not. A signed-in user gets the admin bar and Gravatar images by default, which would send Chrome to secure.gravatar.com and make the benchmark depend on a real outside service. Filters rather than settings, so a database seeded before this plugin is covered too.
 */

defined( 'ABSPATH' ) || exit;

// No avatars anywhere: get_avatar() then prints nothing and asks no host.
add_filter(
	'pre_option_show_avatars',
	static function () {
		return '0';
	}
);

// No admin bar on the front end for anyone, so a logged-in page (s68's
// key flow, the leak test) looks like the anonymous one.
add_filter( 'show_admin_bar', '__return_false' );
