<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Scenario s75: a dialog that ignores Escape. "Filters" opens a
// non-modal dialog (focus moves in, Tab may leave it, as a non-modal
// dialog allows). Good: Escape closes it. Broken: Escape does nothing;
// the Close button still works, so it isn't a trap. keyboard/escape must
// report it and keyboard/trap must not.

require __DIR__ . '/../layout.php';
$broken = testbed_broken( 's75' );
page_start(
	'Filters',
	'<style>
	button { font: inherit; padding: 8px 16px; border: 2px solid #1d2433; border-radius: 4px; background: #fff; color: #1d2433; }
	.panel { border: 2px solid #1d2433; border-radius: 8px; background: #fff; padding: 16px 24px; margin: 16px 0; }
	.panel label { display: block; margin-top: 8px; }
	</style>'
);
?>
<h1>Invoices</h1>
<p><button type="button" id="filters-open" aria-haspopup="dialog" aria-expanded="false" aria-controls="filters">Filters</button></p>
<div class="panel" id="filters" role="dialog" aria-labelledby="filters-title" hidden>
	<h2 id="filters-title">Filters</h2>
	<label for="filters-status">Status</label>
	<select id="filters-status" name="status"><option>Any</option><option>Overdue</option><option>Paid</option></select>
	<p><button type="button" id="filters-close">Close</button></p>
</div>
<p><a href="/report.php">Monthly report</a></p>
<script src="/a11y/modal.js"></script>
<script>
testbedDialog('filters-open', 'filters', 'filters-close', {contain: false, escape: <?= $broken ? 'false' : 'true' ?>});
</script>
<?php
testbed_selftest(
	sprintf(
		<<<'JS'
	var d = document.getElementById('filters');
	document.getElementById('filters-open').click();
	var inside = d.contains(document.activeElement);
	document.activeElement.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape', bubbles: true}));
	var closed = d.hidden;
	document.getElementById('filters-close').click();
	return inside && closed === %s && d.hidden ? true : 'escape closed ' + closed;
JS,
		$broken ? 'false' : 'true'
	)
);
page_end();
