<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Scenario s71: a select that changes the page when it gets focus.
// Good: "Show invoices for" is a select with a Show button, so nothing
// happens until the visitor asks. Broken: a "quicker" version loads the
// chosen view as soon as the select is focused, so tabbing onto it takes
// the visitor to another page (WCAG 3.2.1). keyboard/context-change must
// report it.

require __DIR__ . '/../layout.php';
$broken = testbed_broken( 's71' );
$view   = preg_replace( '/[^a-z]/', '', (string) ( $_GET['view'] ?? 'month' ) );
page_start(
	'Invoices by period',
	'<style>
	select, button { font: inherit; padding: 6px 12px; }
	</style>'
);
?>
<h1>Invoices this <?= htmlspecialchars( $view ) ?></h1>
<form method="get" action="/a11y/autonav.php">
	<label for="view">Show invoices for</label>
	<select id="view" name="view">
		<option value="month">this month</option>
		<option value="quarter">this quarter</option>
		<option value="year">this year</option>
	</select>
	<button type="submit">Show</button>
</form>
<p><a href="/report.php">Monthly report</a></p>
<script>
(function () {
	var select = document.getElementById('view');
	var go = function () {
		var url = '/a11y/autonav.php?view=' + encodeURIComponent(select.value === 'month' ? 'quarter' : 'month');
		// The self-test asks the page what it would do instead.
		if (window.testbedDry) window.testbedDry.push(url);
		else location.assign(url);
	};
	<?php if ( $broken ) : ?>
	select.addEventListener('focus', go);
	<?php endif; ?>
})();
</script>
<?php
testbed_selftest(
	sprintf(
		<<<'JS'
	window.testbedDry = [];
	document.getElementById('view').focus();
	var navigated = window.testbedDry.length > 0;
	window.testbedDry = null;
	return navigated === %s ? true : 'navigated ' + navigated;
JS,
		$broken ? 'true' : 'false'
	)
);
page_end();
