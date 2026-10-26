<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Scenario s78 (hard good): a custom modal dialog built right
// (modal.js): focus moves in on open, Tab stays inside, Escape closes it
// and focus goes back to the opener. Containment is what a modal should
// do, so it must not be reported as a trap. The broken state adds a
// labelled field to the dialog: still correct, nothing may be reported.

require __DIR__ . '/../layout.php';
$broken = testbed_broken( 's78' );
page_start(
	'Reminder settings',
	'<style>
	button { font: inherit; padding: 8px 16px; border: 2px solid #1d2433; border-radius: 4px; background: #fff; color: #1d2433; }
	.modal { position: fixed; inset: 20% auto auto 50%; transform: translateX(-50%); width: min(420px, 90vw); background: #fff; border: 2px solid #1d2433; border-radius: 8px; padding: 24px; }
	.modal label { display: block; margin-top: 8px; }
	</style>'
);
?>
<h1>Reminder settings</h1>
<p>Ledger reminds clients when an invoice is overdue.</p>
<p><button type="button" id="remind-open" aria-haspopup="dialog" aria-expanded="false" aria-controls="remind">Change reminders</button></p>
<div class="modal" id="remind" role="dialog" aria-modal="true" aria-labelledby="remind-title" hidden>
	<h2 id="remind-title">Reminders</h2>
	<label for="remind-days">Days after the due date</label>
	<input id="remind-days" name="days" type="number" value="7" min="1">
	<?php if ( $broken ) : ?>
		<label for="remind-copy">Send a copy to</label>
		<input id="remind-copy" name="copy" type="email" autocomplete="email">
	<?php endif; ?>
	<p><button type="button" id="remind-save">Save</button> <button type="button" id="remind-close">Cancel</button></p>
</div>
<script src="/a11y/modal.js"></script>
<script>
var remind = testbedDialog('remind-open', 'remind', 'remind-close');
document.getElementById('remind-save').addEventListener('click', remind.hide);
</script>
<?php
testbed_selftest(
	<<<'JS'
	var d = document.getElementById('remind');
	document.getElementById('remind-open').click();
	var inside = !d.hidden && d.contains(document.activeElement);
	d.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape', bubbles: true}));
	return inside && d.hidden && document.activeElement === document.getElementById('remind-open') ? true : 'modal';
JS
);
page_end();
