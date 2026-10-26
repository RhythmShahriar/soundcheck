<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Scenarios s82 and s83: a page with an accessibility backlog. The
// invoice list has 300 rows of grey text too light to read (contrast
// 2.8:1), in both states: old problems that a compare must keep quiet
// about. s83 broken fixes one of the first 20 rows (row 5), which must
// come out as one FIXED a11y/color-contrast and nothing NEW, even though
// a per-rule element cap would now let row 21 in. s82 changes nothing.

require __DIR__ . '/../layout.php';
$fixed = testbed_broken( 's83' ) ? 5 : 0;
page_start(
	'Invoice archive',
	'<style>
	.old { color: #999; }
	.fixed { color: #555; }
	#rows { list-style: none; padding: 0; columns: 2; }
	</style>'
);
?>
<h1>Invoice archive</h1>
<p>Every invoice since 2019. Grey rows are archived.</p>
<ul id="rows">
	<?php for ( $i = 1; $i <= 300; $i++ ) : ?>
		<li id="row-<?= $i ?>" class="<?= $i === $fixed ? 'fixed' : 'old' ?>">Invoice <?= 1000 + $i ?>, archived</li>
	<?php endfor; ?>
</ul>
<?php
testbed_selftest(
	sprintf(
		<<<'JS'
	var grey = document.querySelectorAll('#rows li.old').length;
	return grey === %d ? true : grey + ' grey rows';
JS,
		$fixed ? 299 : 300
	)
);
page_end();
