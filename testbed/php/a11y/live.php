<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Scenario s81 (hard good): a page that is different on every load and
// every second. A carousel rotates every few seconds (with a pause
// button, WCAG 2.2.2), the plans come in random order, the page prints
// the time and a search field shows a blinking caret when focused. None
// of that is an accessibility change, so a compare must report nothing
// new. The broken state only rotates faster.

require __DIR__ . '/../layout.php';
$broken = testbed_broken( 's81' );
$plans  = array( 'Starter', 'Solo', 'Team', 'Agency', 'Pro' );
shuffle( $plans );
page_start(
	'Live dashboard',
	'<style>
	.slides { border: 2px solid #1d2433; border-radius: 8px; padding: 16px 24px; background: #fff; }
	.slides button { font: inherit; padding: 6px 12px; border: 2px solid #1d2433; border-radius: 4px; background: #fff; color: #1d2433; }
	input[type=search] { font: inherit; padding: 8px; border: 1px solid #5b6577; border-radius: 4px; }
	</style>'
);
?>
<h1>Dashboard</h1>
<p>Updated at <time id="now" datetime="<?= gmdate( 'c' ) ?>"><?= gmdate( 'H:i:s' ) ?></time>.</p>
<form role="search" action="/a11y/live.php" method="get" onsubmit="return false">
	<label for="q">Search invoices</label>
	<input id="q" name="q" type="search">
</form>
<section class="slides" aria-roledescription="carousel" aria-label="Tips">
	<p id="slide" aria-live="off">Tip 1 of 3: send invoices on the first of the month.</p>
	<button type="button" id="pause" aria-pressed="false">Pause tips</button>
</section>
<h2>Plans</h2>
<ul id="plans">
	<?php foreach ( $plans as $plan ) : ?>
		<li><a href="/a11y/index.php"><?= htmlspecialchars( $plan ) ?></a></li>
	<?php endforeach; ?>
</ul>
<script>
(function () {
	var tips = ['send invoices on the first of the month.', 'remind clients a week after the due date.', 'export the year for your accountant.'];
	var slide = document.getElementById('slide');
	var pause = document.getElementById('pause');
	var now = document.getElementById('now');
	var i = 0;
	var timer = setInterval(function () {
		i = (i + 1) % tips.length;
		slide.textContent = 'Tip ' + (i + 1) + ' of 3: ' + tips[i];
	}, <?= $broken ? 2000 : 3000 ?>);
	pause.addEventListener('click', function () {
		clearInterval(timer);
		pause.setAttribute('aria-pressed', 'true');
	});
	setInterval(function () { now.textContent = new Date().toISOString().slice(11, 19); }, 1000);
})();
</script>
<?php
testbed_selftest(
	<<<'JS'
	var first = document.getElementById('slide').textContent;
	return new Promise(function (resolve) {
		setTimeout(function () {
			resolve(document.getElementById('slide').textContent !== first ? true : 'carousel stood still');
		}, 3200);
	});
JS
);
page_end();
