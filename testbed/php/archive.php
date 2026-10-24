<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

require __DIR__ . '/layout.php';

// Scenario s38, Ctrl+C during a compare. The broken state makes each
// archive page take 3 s, so the benchmark's interrupt lands while pages
// are still loading. /archive.php/2019 and so on are the year pages.
if ( testbed_broken( 's38' ) ) {
	sleep( 3 );
}
$year = (int) trim( $_SERVER['PATH_INFO'] ?? '', '/' );
page_start( $year ? "Archive $year" : 'Archive' );
if ( $year ) {
	echo '<h1>Invoices from ' . $year . '</h1><p>Every invoice sent in ' . $year . ', paid or not.</p>';
} else {
	echo '<h1>Archive</h1><ul>';
	foreach ( range( 2019, 2026 ) as $y ) {
		echo '<li><a href="/archive.php/' . $y . '">' . $y . '</a></li>';
	}
	echo '</ul>';
}
page_end();
