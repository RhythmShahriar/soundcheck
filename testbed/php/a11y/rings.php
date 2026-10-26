<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Scenario s79 (hard good): focus shown without an outline. Each control
// removes the outline and shows focus another way: a box-shadow ring, a
// thick underline, a background colour. That meets WCAG 2.4.7, so
// keyboard/no-focus-ring, which compares pixels rather than reading the
// outline, must not fire. The broken state swaps the colours and which
// control gets which style: still visible focus everywhere.

require __DIR__ . '/../layout.php';
$broken = testbed_broken( 's79' );
$shadow = $broken ? '#b3261e' : '#2f6fdb';
$fill   = $broken ? '#ffe08a' : '#cfe0ff';
page_start(
	'Plans',
	"<style>
	.ring, .underline, .fill { font: inherit; padding: 8px 16px; margin: 8px; border-radius: 4px; outline: none; }
	.ring, .fill { border: 2px solid #1d2433; background: #fff; color: #1d2433; }
	.ring:focus { outline: none; box-shadow: 0 0 0 4px $shadow; }
	.underline { color: #1d4fa0; text-decoration: none; }
	.underline:focus { outline: none; text-decoration: underline 4px; text-underline-offset: 4px; }
	.fill:focus { outline: none; background: $fill; }
	</style>"
);
?>
<h1>Plans</h1>
<p>Pick a plan. You can change it at any time.</p>
<?php if ( $broken ) : ?>
	<p><button type="button" class="fill" id="starter">Choose Starter</button>
	<a class="underline" id="compare" href="/about.php">Compare plans</a>
	<button type="button" class="ring" id="pro">Choose Pro</button></p>
<?php else : ?>
	<p><button type="button" class="ring" id="starter">Choose Starter</button>
	<a class="underline" id="compare" href="/about.php">Compare plans</a>
	<button type="button" class="fill" id="pro">Choose Pro</button></p>
<?php endif; ?>
<?php
testbed_selftest(
	<<<'JS'
	var changed = ['starter', 'compare', 'pro'].every(function (id) {
		var el = document.getElementById(id);
		var before = getComputedStyle(el);
		var look = [before.boxShadow, before.textDecorationLine, before.backgroundColor].join('|');
		el.focus();
		var after = getComputedStyle(el);
		var focused = [after.boxShadow, after.textDecorationLine, after.backgroundColor].join('|');
		var noOutline = after.outlineStyle === 'none';
		el.blur();
		return look !== focused && noOutline;
	});
	return changed ? true : 'focus styles';
JS
);
page_end();
