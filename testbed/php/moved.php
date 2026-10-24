<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

require __DIR__ . '/layout.php';

// Scenario s43, a redirect to another host. The broken state sends the
// page to the same path on another host name (localhost and 127.0.0.1
// swap), as a misconfigured site URL or a CDN rule does.
if ( testbed_broken( 's43' ) && ! isset( $_GET['landed'] ) ) {
	$host  = $_SERVER['HTTP_HOST'] ?? 'localhost';
	$other = str_starts_with( $host, 'localhost' ) ? '127.0.0.1' . substr( $host, 9 ) : 'localhost' . strstr( $host . ':', ':' );
	header( 'Location: http://' . rtrim( $other, ':' ) . '/moved.php?landed=1', true, 302 );
	exit;
}
page_start( 'Settings' );
?>
<h1>Settings</h1>
<p>Your company name, logo and invoice numbering.</p>
<?php
page_end();
