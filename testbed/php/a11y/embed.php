<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Scenario s84 (hard good): frames. The page embeds a delivery map from
// its own site (same origin) whose content has accessibility problems,
// and a payment form from another origin (another host and port) that
// shows the benchmark's canaries. Soundcheck doesn't audit inside
// frames, so a compare must report nothing new, the statement must list
// the frames as not tested and no canary may reach any file Soundcheck
// writes. The broken state adds more problems inside the map frame.

require __DIR__ . '/../layout.php';
page_start(
	'Delivery and payment',
	'<style>
	iframe { display: block; width: 100%; max-width: 520px; height: 180px; border: 1px solid #5b6577; margin: 12px 0 24px; }
	</style>'
);
?>
<h1>Delivery and payment</h1>
<h2>Where we deliver</h2>
<iframe id="map" title="Delivery area map" src="/a11y/frame.php/map"></iframe>
<h2>Pay by card</h2>
<iframe id="pay" title="Card payment" src="<?= htmlspecialchars( testbed_frame_origin() ) ?>/a11y/frame.php/pay"></iframe>
<p><a href="/a11y/index.php">Back to the order</a></p>
<?php
testbed_selftest(
	<<<'JS'
	var map = document.getElementById('map');
	var pay = document.getElementById('pay');
	var same = false;
	try { same = !!map.contentDocument.getElementById('map-area'); } catch (e) {}
	var other = false;
	try { other = !pay.contentDocument; } catch (e) { other = true; }
	return same && other && new URL(pay.src).origin !== location.origin ? true : 'frames';
JS
);
page_end();
