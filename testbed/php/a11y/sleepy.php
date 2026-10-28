<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Scenario s90: a page that never loads under --eaa. The run starts at
// /a11y/sleepy.php, which links to its search page,
// /a11y/sleepy.php/search. Good: the search page has a field and a
// select without a label (two known problems the snapshot keeps).
// Broken: the server waits 12 s before it answers the search page and
// the run's timeout is 5 s, so it never loads. Soundcheck must not take
// it for a tested page with nothing found: both audits are recorded as
// not run, the compare says "not checked" for a11y and keyboard, keeps
// the snapshot's issues (never FIXED) and exits 3, and the statement
// draft lists the page under "Not tested".

require __DIR__ . '/../layout.php';
$search = '/search' === ( $_SERVER['PATH_INFO'] ?? '' );
$waited = 0;
if ( $search && testbed_broken( 's90' ) ) {
	sleep( 12 );
	$waited = 12;
}
page_start( $search ? 'Search invoices' : 'Invoices' );
if ( ! $search ) :
	?>
<h1>Invoices</h1>
<p><a href="/a11y/sleepy.php/search">Search invoices</a></p>
	<?php
else :
	?>
<h1 data-waited="<?= (int) $waited ?>" id="title">Search invoices</h1>
<form method="get" action="/a11y/sleepy.php/search" onsubmit="return false">
	<input type="text" name="q">
	<select name="status"><option>Any</option><option>Overdue</option></select>
</form>
	<?php
endif;
page_end();
