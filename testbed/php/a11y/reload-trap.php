<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Scenario s76: a page that reloads when a link gets focus. Broken: a
// "track clicks" script reloads the page from the second link's focus
// handler, so every Tab onto it starts the page over. Soundcheck must
// report a trap or say the walk was capped. It must finish within its cap:
// it must never hang. Good: no such handler.

require __DIR__ . '/../layout.php';
$broken = testbed_broken( 's76' );
page_start( 'Help' );
?>
<h1>Help</h1>
<ul>
	<li><a href="/about.php">Getting started</a></li>
	<li><a id="track" href="/report.php">Reading the monthly report</a></li>
	<li><a href="/a11y/index.php">Ordering coffee</a></li>
</ul>
<script>
(function () {
	var link = document.getElementById('track');
	var reload = function () {
		if (window.testbedDry) window.testbedDry.push('reload');
		else location.reload();
	};
	<?php if ( $broken ) : ?>
	link.addEventListener('focus', reload);
	<?php endif; ?>
})();
</script>
<?php
testbed_selftest(
	sprintf(
		<<<'JS'
	window.testbedDry = [];
	document.getElementById('track').focus();
	var reloaded = window.testbedDry.length > 0;
	window.testbedDry = null;
	return reloaded === %s ? true : 'reloaded ' + reloaded;
JS,
		$broken ? 'true' : 'false'
	)
);
page_end();
