<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Scenario s53: a dialog that doesn't take focus. Good: opening the
// "Add a client" dialog moves focus to its first field (modal.js). Broken:
// a rewrite drops that line, so the dialog appears while focus stays on
// the button behind it and Tab goes on through the page underneath.
// keyboard/dialog-focus must report it.

require __DIR__ . '/../layout.php';
$broken = testbed_broken( 's53' );
page_start(
	'Clients',
	'<style>
	button { font: inherit; padding: 8px 16px; border: 2px solid #1d2433; border-radius: 4px; background: #fff; color: #1d2433; }
	.modal { position: fixed; inset: 20% auto auto 50%; transform: translateX(-50%); width: min(420px, 90vw); background: #fff; border: 2px solid #1d2433; border-radius: 8px; padding: 24px; }
	.modal label { display: block; margin-top: 8px; }
	</style>'
);
?>
<h1>Clients</h1>
<p>Twelve clients, three with an overdue invoice.</p>
<p><button type="button" id="client-open" aria-haspopup="dialog" aria-expanded="false" aria-controls="client">Add a client</button></p>
<p><a href="/report.php">This month's report</a></p>
<div class="modal" id="client" role="dialog" aria-modal="true" aria-labelledby="client-title" hidden>
	<h2 id="client-title">Add a client</h2>
	<label for="client-name">Company name</label>
	<input id="client-name" name="company" type="text" autocomplete="organization">
	<p><button type="button" id="client-save">Add</button> <button type="button" id="client-close">Cancel</button></p>
</div>
<script src="/a11y/modal.js"></script>
<script>
var client = testbedDialog('client-open', 'client', 'client-close', {focusOnOpen: <?= $broken ? 'false' : 'true' ?>});
document.getElementById('client-save').addEventListener('click', client.hide);
</script>
<?php
testbed_selftest(
	sprintf(
		<<<'JS'
	var d = document.getElementById('client');
	var opener = document.getElementById('client-open');
	opener.focus();
	opener.click();
	var inside = !d.hidden && d.contains(document.activeElement);
	var stayed = !d.hidden && document.activeElement === opener;
	document.getElementById('client-close').click();
	return (%s ? stayed : inside) && d.hidden ? true : 'focus on open';
JS,
		$broken ? 'true' : 'false'
	)
);
page_end();
