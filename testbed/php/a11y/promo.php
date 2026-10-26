<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Scenario s69: content that doesn't reflow. Good: the promo bar fits
// any width. Broken: a page builder gives it a fixed 600 px width, so at
// 320 CSS px (a phone, or a desktop zoomed to 400%) the page scrolls
// sideways (WCAG 1.4.10). a11y/reflow must report it.

require __DIR__ . '/../layout.php';
$broken = testbed_broken( 's69' );
page_start(
	'Offers',
	'<style>
	.promo { background: #1d4fa0; color: #fff; padding: 12px 16px; border-radius: 4px; margin: 16px 0; max-width: 100%; }
	' . ( $broken ? '.promo { width: 600px; max-width: none; }' : '' ) . '
	</style>'
);
?>
<h1>Offers</h1>
<div class="promo" id="promo">Pay yearly and get two months free.</div>
<p>Offers end on the last day of the month.</p>
<?php
testbed_selftest(
	sprintf(
		<<<'JS'
	var fixed = getComputedStyle(document.getElementById('promo')).maxWidth === 'none';
	return fixed === %s ? true : 'promo width';
JS,
		$broken ? 'true' : 'false'
	)
);
page_end();
