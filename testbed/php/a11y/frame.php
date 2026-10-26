<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// The frames embed.php (s84) shows.
//
//   /a11y/frame.php/map  same origin: a delivery map stand-in whose
//                        content has accessibility problems a frame's
//                        owner would have to fix (no alt, no label, low
//                        contrast); s84 broken adds more of them
//   /a11y/frame.php/pay  loaded from another origin: a card form that
//                        carries the canaries from <state>/canaries in a
//                        field value, data-* and aria-label attributes,
//                        a link's query string and its text

require __DIR__ . '/../layout.php';
$kind = trim( (string) ( $_SERVER['PATH_INFO'] ?? '' ), '/' );
header( 'Content-Type: text/html; charset=utf-8' );
?><!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title><?= 'pay' === $kind ? 'Card payment' : 'Delivery area' ?></title>
<style>
body { margin: 8px; font: 15px/1.5 system-ui, sans-serif; }
.faint { color: #aaa; }
:focus-visible { outline: 3px solid #2f6fdb; outline-offset: 2px; }
</style></head>
<body>
<?php if ( 'pay' === $kind ) : ?>
	<?php $c = testbed_canaries(); ?>
	<form id="pay-form" data-customer="<?= htmlspecialchars( $c['data'] ?? '' ) ?>" onsubmit="return false">
		<label for="card-name">Name on card</label>
		<input id="card-name" name="card-name" type="text" autocomplete="cc-name" value="<?= htmlspecialchars( $c['field'] ?? '' ) ?>">
		<button type="button" aria-label="Pay as <?= htmlspecialchars( $c['aria'] ?? '' ) ?>">Pay</button>
		<p><a href="/a11y/frame.php/pay?token=<?= rawurlencode( $c['query'] ?? '' ) ?>">Receipt for <?= htmlspecialchars( $c['text'] ?? '' ) ?></a></p>
	</form>
<?php else : ?>
	<div id="map-area">
		<img src="/pixel.svg" width="160" height="60">
		<p class="faint">Grey area: next-day delivery.</p>
		<input type="text" name="postcode">
		<?php if ( testbed_broken( 's84' ) ) : ?>
			<select name="zone"><option>Zone 1</option><option>Zone 2</option></select>
			<img src="/pixel.svg" width="160" height="60">
		<?php endif; ?>
	</div>
<?php endif; ?>
</body>
</html>
