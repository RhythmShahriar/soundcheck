<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Scenario s66: zoom switched off. A theme update sets the viewport to
// user-scalable=no, maximum-scale=1, so phone users can't pinch to zoom
// (WCAG 1.4.4). a11y/meta-viewport must report it.

require __DIR__ . '/../layout.php';
$broken   = testbed_broken( 's66' );
$viewport = $broken ? 'width=device-width, initial-scale=1, user-scalable=no, maximum-scale=1' : 'width=device-width, initial-scale=1';
page_start( 'Yearly totals', '', $viewport );
?>
<h1>Yearly totals</h1>
<p>Invoiced this year: £48,210. Paid: £44,980. Outstanding: £3,230.</p>
<?php
testbed_selftest(
	sprintf(
		<<<'JS'
	var content = document.querySelector('meta[name=viewport]').getAttribute('content');
	return /user-scalable=no/.test(content) === %s ? true : content;
JS,
		$broken ? 'true' : 'false'
	)
);
page_end();
