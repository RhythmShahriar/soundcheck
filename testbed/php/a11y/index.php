<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Scenario s89: a small site with nothing for --eaa to find. It has the
// parts accessibility checks trip over on real sites: a skip link,
// landmarks, a native <dialog> opened with showModal() that closes on
// Escape and gives focus back, visible focus rings and a labelled
// checkout form with valid autocomplete tokens. Nothing on it changes
// between the states; s89 checks the clean result, the statement and
// every control round.

require __DIR__ . '/../layout.php';
page_start(
	'Accessible checkout',
	'<style>
	form.checkout label { display: block; margin-top: 12px; font-weight: 600; }
	form.checkout input, form.checkout select { width: 100%; max-width: 420px; padding: 8px; font: inherit; border: 1px solid #5b6577; border-radius: 4px; }
	button { font: inherit; padding: 8px 16px; border-radius: 4px; border: 2px solid #1d2433; background: #fff; color: #1d2433; cursor: pointer; }
	button.primary { background: #1d4fa0; border-color: #1d4fa0; color: #fff; margin-top: 16px; }
	dialog { max-width: 420px; border: 2px solid #1d2433; border-radius: 8px; padding: 24px; }
	dialog::backdrop { background: rgb(0 0 0 / 40%); }
	</style>'
);
?>
<h1>Order coffee for the office</h1>
<p>Three steps: pick a bag, tell us where to send it, pay on delivery.</p>
<p><button type="button" id="delivery-open">Delivery times</button></p>
<dialog id="delivery" aria-labelledby="delivery-title">
	<h2 id="delivery-title">Delivery times</h2>
	<p>Orders before noon leave the same day. Delivery takes one or two working days.</p>
	<button type="button" id="delivery-close">Close</button>
</dialog>
<h2 id="checkout-title">Checkout</h2>
<form class="checkout" method="get" action="/a11y/index.php" aria-labelledby="checkout-title" onsubmit="return false">
	<label for="bag">Bag</label>
	<select id="bag" name="bag">
		<option>House espresso, 1 kg</option>
		<option>Colombia Huila, 250 g</option>
	</select>
	<label for="name">Full name</label>
	<input id="name" name="name" type="text" autocomplete="name" required>
	<label for="email">Email address</label>
	<input id="email" name="email" type="email" autocomplete="email" required>
	<label for="street">Street address</label>
	<input id="street" name="street" type="text" autocomplete="street-address" required>
	<label for="postcode">Postcode</label>
	<input id="postcode" name="postcode" type="text" autocomplete="postal-code" required>
	<button type="submit" class="primary">Review order</button>
</form>
<script>
(function () {
	var dialog = document.getElementById('delivery');
	var opener = document.getElementById('delivery-open');
	document.getElementById('delivery-open').addEventListener('click', function () { dialog.showModal(); });
	document.getElementById('delivery-close').addEventListener('click', function () { dialog.close(); });
	// A native modal dialog closes on Escape by itself and returns focus
	// to the element that opened it.
	dialog.addEventListener('close', function () { opener.focus(); });
})();
</script>
<?php
testbed_selftest(
	<<<'JS'
	var dialog = document.getElementById('delivery');
	var opener = document.getElementById('delivery-open');
	opener.click();
	var inside = dialog.open && dialog.contains(document.activeElement);
	// The close event (and with it the focus return) comes a task later,
	// and on a busy machine later still: wait for it instead of a fixed
	// delay, with a deadline so a dialog that never closes still fails.
	var closed = new Promise(function (resolve) {
		dialog.addEventListener('close', resolve, { once: true });
		setTimeout(resolve, 5000);
	});
	document.getElementById('delivery-close').click();
	return closed.then(function () {
		if (!inside) {
			return 'dialog: focus did not move into it';
		}
		return !dialog.open && document.activeElement === opener ? true : 'dialog: focus did not come back';
	});
JS
);
page_end();
