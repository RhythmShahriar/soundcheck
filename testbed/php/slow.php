<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

require __DIR__ . '/layout.php';

// Scenario s41, a page that answers just under the timeout. The broken
// state waits 5 s; the benchmark runs this scenario with --timeout 6s.
if ( testbed_broken( 's41' ) ) {
	sleep( 5 );
}
page_start( 'Yearly summary' );
?>
<h1>Yearly summary</h1>
<p>Invoiced £48,300 this year; £45,100 of it is paid.</p>
<?php
page_end();
