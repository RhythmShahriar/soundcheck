<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Scenario s87: an upgrade must not flood a comparison. The profile form
// uses autocomplete tokens that don't exist ("mail", "phonenumber"),
// which only the WCAG 2.1 rules flag (1.3.5, axe autocomplete-valid).
// The snapshot in testbed/fixtures/s87-snapshot was taken of this page
// by Soundcheck 1.2, which ran the WCAG 2.0 rules and stored no
// options.wcag. Compared with 1.3 --eaa, the page must still come out
// clean: the compare keeps the snapshot's 2.0 rules and says so. The
// page is the same in both states.

require __DIR__ . '/../layout.php';
page_start(
	'Your profile',
	'<style>
	form label { display: block; margin-top: 12px; font-weight: 600; }
	form input { width: 100%; max-width: 420px; padding: 8px; font: inherit; border: 1px solid #5b6577; border-radius: 4px; }
	</style>'
);
?>
<h1>Your profile</h1>
<form method="get" action="/a11y/legacy.php" onsubmit="return false">
	<label for="p-name">Full name</label>
	<input id="p-name" name="name" type="text" autocomplete="name">
	<label for="p-mail">Email address</label>
	<input id="p-mail" name="mail" type="email" autocomplete="mail">
	<label for="p-phone">Phone</label>
	<input id="p-phone" name="phone" type="tel" autocomplete="phonenumber">
</form>
<?php
testbed_selftest(
	<<<'JS'
	return document.getElementById('p-mail').getAttribute('autocomplete') === 'mail' ? true : 'autocomplete';
JS
);
page_end();
