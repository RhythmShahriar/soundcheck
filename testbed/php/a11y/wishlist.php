<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Scenario s57: a custom button that ignores the keyboard. "Save for
// later" is a <div role="button" tabindex="0"> with a click listener. In
// the good state it also answers Enter and Space; the broken state lost
// that handler in a rewrite, so it can be focused yet does nothing by
// keyboard while a click still works. keyboard/not-operable must
// report it.

require __DIR__ . '/../layout.php';
$broken = testbed_broken( 's57' );
page_start(
	'Saved invoices',
	'<style>
	.save { display: inline-block; padding: 8px 16px; border: 2px solid #1d2433; border-radius: 4px; background: #fff; color: #1d2433; cursor: pointer; }
	</style>'
);
?>
<h1>Invoice 1042</h1>
<p>Café Nero Ltd, £420.00, due 30 November.</p>
<p><span class="save" id="save" role="button" tabindex="0">Save for later</span></p>
<p id="save-status" role="status"></p>
<script>
(function () {
	var button = document.getElementById('save');
	var status = document.getElementById('save-status');
	var save = function () {
		status.textContent = 'Saved. Find it under Saved invoices.';
		button.textContent = 'Saved';
	};
	button.addEventListener('click', save);
	<?php if ( ! $broken ) : ?>
	button.addEventListener('keydown', function (e) {
		if (e.key === 'Enter' || e.key === ' ') {
			e.preventDefault();
			save();
		}
	});
	<?php endif; ?>
})();
</script>
<?php
testbed_selftest(
	sprintf(
		<<<'JS'
	var button = document.getElementById('save');
	var status = document.getElementById('save-status');
	button.dispatchEvent(new KeyboardEvent('keydown', {key: 'Enter', bubbles: true}));
	var byKey = status.textContent !== '';
	status.textContent = '';
	button.click();
	var byClick = status.textContent !== '';
	status.textContent = '';
	button.textContent = 'Save for later';
	return byClick && byKey === %s ? true : 'keys ' + byKey + ', click ' + byClick;
JS,
		$broken ? 'false' : 'true'
	)
);
page_end();
