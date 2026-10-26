<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Scenario s72: an off-canvas menu whose links stay tabbable when it's
// closed. Good: the closed drawer is moved off screen and hidden
// (visibility: hidden), so Tab skips its links. Broken: a theme update
// keeps only the transform, so Tab lands on links nobody can see.
// keyboard/focus-invisible must report it.

require __DIR__ . '/../layout.php';
$broken = testbed_broken( 's72' );
page_start(
	'Settings',
	'<style>
	.drawer { position: fixed; top: 0; left: 0; width: 260px; height: 100%; background: #1d2433; padding: 24px; transform: translateX(-110%); transition: transform .15s, visibility .15s; z-index: 5; }
	.drawer a { display: block; color: #fff; margin: 8px 0; }
	.drawer.open { transform: none; visibility: visible; }
	' . ( $broken ? '' : '.drawer:not(.open) { visibility: hidden; }' ) . '
	#drawer-toggle { font: inherit; padding: 8px 16px; border: 2px solid #1d2433; border-radius: 4px; background: #fff; color: #1d2433; }
	</style>'
);
?>
<h1>Settings</h1>
<p><button type="button" id="drawer-toggle" aria-expanded="false" aria-controls="drawer">Settings menu</button></p>
<nav class="drawer" id="drawer" aria-label="Settings">
	<a href="/about.php">Profile</a>
	<a href="/report.php">Invoices</a>
	<a href="/a11y/index.php">Payments</a>
</nav>
<p>Choose what you want to change from the settings menu.</p>
<script>
(function () {
	var toggle = document.getElementById('drawer-toggle');
	var drawer = document.getElementById('drawer');
	toggle.addEventListener('click', function () {
		var open = drawer.classList.toggle('open');
		toggle.setAttribute('aria-expanded', String(open));
	});
})();
</script>
<?php
testbed_selftest(
	sprintf(
		<<<'JS'
	var link = document.querySelector('#drawer a');
	link.focus();
	var reachable = document.activeElement === link;
	var r = link.getBoundingClientRect();
	link.blur();
	return reachable === %s && r.right <= 0 ? true : 'reachable ' + reachable;
JS,
		$broken ? 'true' : 'false'
	)
);
page_end();
