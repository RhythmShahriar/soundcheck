<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Scenario s77 (hard good): a native <dialog> opened with showModal().
// The browser keeps focus inside it, closes it on Escape and gives focus
// back to the opener, so a keyboard walk must not call it a trap or lost
// focus. The broken state only changes what the dialog says and adds a
// link to it: still a correct dialog, so nothing may be reported.

require __DIR__ . '/../layout.php';
$broken = testbed_broken( 's77' );
page_start(
	'Delivery times',
	'<style>
	button { font: inherit; padding: 8px 16px; border: 2px solid #1d2433; border-radius: 4px; background: #fff; color: #1d2433; }
	dialog { max-width: 420px; border: 2px solid #1d2433; border-radius: 8px; padding: 24px; }
	</style>'
);
?>
<h1>Delivery times</h1>
<p>We send invoices and reminders by email the same day.</p>
<p><button type="button" id="times-open">Show delivery times</button></p>
<dialog id="times" aria-labelledby="times-title">
	<h2 id="times-title">Delivery times</h2>
	<?php if ( $broken ) : ?>
		<p>New: invoices sent before 6pm go out the same evening. <a href="/about.php">About Ledger</a></p>
	<?php else : ?>
		<p>Invoices sent before noon go out the same day.</p>
	<?php endif; ?>
	<button type="button" id="times-close">Close</button>
</dialog>
<script>
(function () {
	var dialog = document.getElementById('times');
	var opener = document.getElementById('times-open');
	opener.addEventListener('click', function () { dialog.showModal(); });
	document.getElementById('times-close').addEventListener('click', function () { dialog.close(); });
	dialog.addEventListener('close', function () { opener.focus(); });
})();
</script>
<?php
testbed_selftest(
	<<<'JS'
	var dialog = document.getElementById('times');
	var opener = document.getElementById('times-open');
	opener.click();
	var inside = dialog.open && dialog.contains(document.activeElement);
	// The close event (and with it the focus return) comes a task later,
	// and on a busy machine later still: wait for it instead of a fixed
	// delay, with a deadline so a dialog that never closes still fails.
	var closed = new Promise(function (resolve) {
		dialog.addEventListener('close', resolve, { once: true });
		setTimeout(resolve, 5000);
	});
	document.getElementById('times-close').click();
	return closed.then(function () {
		if (!inside) {
			return 'dialog: focus did not move into it';
		}
		return !dialog.open && document.activeElement === opener ? true : 'dialog: focus did not come back';
	});
JS
);
page_end();
