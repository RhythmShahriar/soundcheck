<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Scenario s65: a cookie banner that traps the keyboard. Good: the
// banner sits at the top of the content with two real buttons that close
// it. Tab moves through it like the rest of the page. Broken: a
// consent plugin update grabs focus for the banner when the page loads,
// catches Tab and Shift+Tab to keep it there and turns the buttons into
// spans only a mouse can press; Escape does nothing. A keyboard user
// can't get past it. keyboard/trap must report it as BROKE.

require __DIR__ . '/../layout.php';
$broken = testbed_broken( 's65' );
page_start(
	'Pricing',
	'<style>
	.consent { border: 2px solid #1d2433; background: #fff; padding: 16px 24px; margin-bottom: 24px; border-radius: 8px; }
	.consent button, .consent .choice { display: inline-block; font: inherit; padding: 8px 16px; margin-right: 8px; border: 2px solid #1d2433; border-radius: 4px; background: #fff; color: #1d2433; cursor: pointer; }
	</style>'
);
?>
<section class="consent" id="consent" aria-labelledby="consent-title" tabindex="-1">
	<h2 id="consent-title">Cookies</h2>
	<p>We use one cookie to keep you signed in. No tracking.</p>
	<?php if ( $broken ) : ?>
		<span class="choice" id="consent-accept">Accept</span><span class="choice" id="consent-reject">Reject</span>
	<?php else : ?>
		<button type="button" id="consent-accept">Accept</button><button type="button" id="consent-reject">Reject</button>
	<?php endif; ?>
</section>
<h1>Pricing</h1>
<p>Starter is free for three clients. <a href="/a11y/index.php">Order coffee</a> or read <a href="/about.php">about Ledger</a>.</p>
<script>
(function () {
	var banner = document.getElementById('consent');
	var dismiss = function () { banner.hidden = true; };
	document.getElementById('consent-accept').addEventListener('click', dismiss);
	document.getElementById('consent-reject').addEventListener('click', dismiss);
	<?php if ( $broken ) : ?>
	banner.tabIndex = 0;
	document.addEventListener('keydown', function (e) {
		if (e.key === 'Tab' && !banner.hidden) {
			e.preventDefault();
			banner.focus();
		}
	}, true);
	// It grabs focus once the page (and its self-test) is ready.
	document.addEventListener('testbed-ready', function () { banner.focus(); });
	<?php endif; ?>
})();
</script>
<?php
testbed_selftest(
	sprintf(
		<<<'JS'
	var banner = document.getElementById('consent');
	var tab = new KeyboardEvent('keydown', {key: 'Tab', bubbles: true, cancelable: true});
	document.getElementById('consent-accept').dispatchEvent(tab);
	var caught = tab.defaultPrevented;
	var native = document.getElementById('consent-accept').localName === 'button';
	return caught === %s && native === !caught ? true : 'Tab caught ' + caught;
JS,
		$broken ? 'true' : 'false'
	)
);
page_end();
