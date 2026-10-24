<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

require __DIR__ . '/layout.php';

// Scenario s25, mixed content after a move to https. Served over https
// (the php-https site), the broken state still points the logo at the
// old http:// URL, as content saved before the move does.
$logo = '/pixel.svg';
if ( testbed_broken( 's25' ) ) {
	$http = getenv( 'TESTBED_HTTP_ORIGIN' ) ?: 'http://' . ( $_SERVER['HTTP_HOST'] ?? 'localhost' );
	$logo = rtrim( $http, '/' ) . '/pixel.svg';
}
page_start( 'Invoices' );
?>
<h1>Invoices</h1>
<p><img src="<?= htmlspecialchars( $logo ) ?>" width="320" height="120" alt="Ledger"></p>
<p>Every invoice you sent this year, with its due date and status.</p>
<?php
page_end();
