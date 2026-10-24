<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

require __DIR__ . '/layout.php';

// Scenario s45, a site with more than 300 pages. /catalog.php lists 320
// item pages (/catalog.php/item-1 …); in the broken state item 317 prints
// a PHP warning, so only a run that gets that far catches it.
const TESTBED_ITEMS = 320;
$n = (int) preg_replace( '/\D/', '', $_SERVER['PATH_INFO'] ?? '' );
if ( $n >= 1 && $n <= TESTBED_ITEMS ) {
	$item = array( 'name' => "Item $n" );
	page_start( "Item $n" );
	echo '<h1>Item ' . $n . '</h1><p>Template for invoice line ' . $n . '.</p>';
	if ( 317 === $n && testbed_broken( 's45' ) ) {
		echo '<p>' . htmlspecialchars( $item['price'] ) . '</p>';
	}
	page_end();
	exit;
}
page_start( 'Catalog' );
echo '<h1>Catalog</h1><ul>';
for ( $i = 1; $i <= TESTBED_ITEMS; $i++ ) {
	echo '<li><a href="/catalog.php/item-' . $i . '">Item ' . $i . '</a></li>';
}
echo '</ul>';
page_end();
