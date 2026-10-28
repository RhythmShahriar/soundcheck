<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Scenario s91: selectors that hold secrets. Good: a labelled select.
// Broken: a template update adds controls without a label whose id,
// name or class carries the benchmark's canaries from <state>/canaries
// (a GitHub token, a JWT and an email address) and a coupon field that
// traps the keyboard, keeping a select whose id is another email
// address out of reach. Soundcheck names elements by their id or name,
// so the a11y and keyboard issues, their details, the keyboard trace
// and every report would hold the values unless it masks them. No
// canary may reach any file Soundcheck writes.

require __DIR__ . '/../layout.php';
$broken = testbed_broken( 's91' );
$c      = testbed_canaries();
$attr   = function ( string $name ) use ( $c ): string {
	return htmlspecialchars( $c[ $name ] ?? '' );
};
page_start( 'Billing' );
?>
<h1>Billing</h1>
<form method="get" action="/a11y/ids.php" onsubmit="return false">
	<label for="plan">Plan</label>
	<select id="plan" name="plan"><option>Starter</option><option>Pro</option></select>
	<?php if ( $broken ) : ?>
		<select id="<?= $attr( 'idtoken' ) ?>"><option>Monthly</option><option>Yearly</option></select>
		<select name="<?= $attr( 'namejwt' ) ?>"><option>EUR</option><option>GBP</option></select>
		<select class="<?= $attr( 'classtoken' ) ?>"><option>Card</option><option>Invoice</option></select>
		<button type="button" id="<?= $attr( 'idemail' ) ?>"></button>
		<input id="coupon" aria-label="Coupon" onkeydown="if (event.key === 'Tab') { event.preventDefault(); this.focus(); }">
		<select id="<?= $attr( 'trapemail' ) ?>" aria-label="Region"><option>EU</option><option>UK</option></select>
	<?php endif; ?>
</form>
<?php
page_end();
