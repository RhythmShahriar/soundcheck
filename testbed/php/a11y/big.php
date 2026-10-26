<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Scenario s74: a page too big to audit. Good: a short price table.
// Broken: an import fills the page with 20,000 elements and a runaway
// price ticker keeps the page's main thread busy 98% of the time, so the
// audits can't finish: the keyboard walk runs out of time and, on a
// slower machine, axe-core too (axe alone gets through 20,000 elements in
// seconds now). Soundcheck must say the page was not
// checked (a "not checked" warning, exit 3 under --eaa) and keep the
// snapshot's issues rather than count them as fixed.

require __DIR__ . '/../layout.php';
$broken = testbed_broken( 's74' );
page_start(
	'Price list',
	'<style>
	.grid span { color: #777; }
	.grid span:nth-child(3n) { background: #eef1f6; }
	</style>'
);
?>
<h1>Price list</h1>
<table>
	<tr><th>Plan</th><th>Monthly</th></tr>
	<tr><td>Starter</td><td>£0</td></tr>
	<tr><td>Pro</td><td>£9</td></tr>
</table>
<?php if ( $broken ) : ?>
	<div class="grid" id="grid">
	<?php
	for ( $row = 0; $row < 1000; $row++ ) {
		echo '<p>';
		for ( $i = 0; $i < 19; $i++ ) {
			echo '<span>', 20 * $row + $i, '</span> ';
		}
		echo "</p>\n";
	}
	?>
	</div>
	<script>
	// The ticker: after the load, 2950 ms of every 3 s in a loop.
	addEventListener('load', function () {
		setTimeout(function () {
			setInterval(function () { var end = performance.now() + 2950; while (performance.now() < end) {} }, 3000);
		}, 300);
	});
	</script>
<?php endif; ?>
<?php
testbed_selftest(
	sprintf(
		<<<'JS'
	var n = document.getElementsByTagName('*').length;
	return (n >= 20000) === %s ? true : n + ' elements';
JS,
		$broken ? 'true' : 'false'
	)
);
page_end();
