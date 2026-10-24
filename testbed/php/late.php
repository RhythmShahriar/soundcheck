<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

require __DIR__ . '/layout.php';

// Scenario s35, content loaded late. In both states the team section is
// fetched by a script 1.5 s after the page loads (a lazy-loading widget).
// In the broken state that late part shows a photo that doesn't exist,
// so only a checker that waits for late content can see the break.
if ( isset( $_GET['part'] ) ) {
	header( 'Content-Type: text/html; charset=utf-8' );
	$photo = testbed_broken( 's35' ) ? '/team-photo-2.svg' : '/pixel.svg';
	echo '<h2>The team</h2><p><img src="' . $photo . '" width="320" height="120" alt="The Ledger team"></p><p>Four people build Ledger.</p>';
	exit;
}
page_start( 'Team' );
?>
<h1>Team</h1>
<div id="late">Loading…</div>
<script>
setTimeout(function () {
	fetch('/late.php?part=1').then(function (r) { return r.text(); }).then(function (html) {
		document.getElementById('late').innerHTML = html;
	});
}, 1500);
</script>
<?php
page_end();
