<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

require __DIR__ . '/layout.php';

// Scenario s42, an intermittent server. In the broken state every fifth
// request to these pages answers 500, counted across the whole site in a
// temp file, so it isn't the same page every time.
if ( testbed_broken( 's42' ) && testbed_count( 'flaky' ) % 5 === 0 ) {
	http_response_code( 500 );
	echo "<!doctype html>\n<title>500 Internal Server Error</title><h1>Internal Server Error</h1>\n";
	exit;
}
$n = (int) trim( $_SERVER['PATH_INFO'] ?? '', '/' );
page_start( $n ? "Payment $n" : 'Payments' );
if ( $n ) {
	echo '<h1>Payment ' . $n . '</h1><p>Received by bank transfer.</p>';
} else {
	echo '<h1>Payments</h1><ul>';
	foreach ( range( 1, 5 ) as $i ) {
		echo '<li><a href="/flaky.php/' . $i . '">Payment ' . $i . '</a></li>';
	}
	echo '</ul>';
}
page_end();
