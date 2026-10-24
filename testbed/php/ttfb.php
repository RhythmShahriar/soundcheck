<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

require __DIR__ . '/layout.php';

// Scenario s34, a slow database query. The broken state waits 1.5 s
// before it answers; the page itself (size, requests) doesn't change.
if ( testbed_broken( 's34' ) ) {
	usleep( 1500000 );
}
page_start( 'Clients' );
?>
<h1>Clients</h1>
<table>
	<tr><th>Client</th><th>Since</th></tr>
	<tr><td>Northwind</td><td>2019</td></tr>
	<tr><td>Contoso</td><td>2021</td></tr>
	<tr><td>Fabrikam</td><td>2023</td></tr>
</table>
<?php
page_end();
