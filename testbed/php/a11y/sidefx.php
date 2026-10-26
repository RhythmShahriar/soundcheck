<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Scenario s88 (hard good): a page that changes things for whoever
// follows its links or focuses its fields, the way some real shops do.
// "Add to cart" and "Log out" are plain GET links, focusing the chat
// field opens a WebSocket, "Download invoice" downloads a file and "Open
// receipt" opens a window. Each request that reaches the server adds
// one to <state>/sidefx.txt. A keyboard walk presses Tab and nothing
// else here. It holds requests while it presses keys, so the counter
// must stay where it was and no file may be downloaded. The links carry
// rel="nofollow" so the page discovery doesn't follow them either. The
// page is the same in both states.

require __DIR__ . '/../layout.php';
$action = trim( (string) ( $_SERVER['PATH_INFO'] ?? '' ), '/' );
if ( isset( $_GET['add-to-cart'] ) || in_array( $action, array( 'logout', 'download', 'popup' ), true ) ) {
	testbed_bump( 'sidefx' );
	if ( 'download' === $action ) {
		header( 'Content-Type: text/plain; charset=utf-8' );
		header( 'Content-Disposition: attachment; filename="invoice-1042.txt"' );
		echo "Invoice 1042\n";
		exit;
	}
}
page_start(
	'Your account',
	'<style>
	button, .button { display: inline-block; font: inherit; padding: 8px 16px; margin: 4px 8px 4px 0; border: 2px solid #1d2433; border-radius: 4px; background: #fff; color: #1d2433; text-decoration: none; }
	input { font: inherit; padding: 8px; border: 1px solid #5b6577; border-radius: 4px; }
	</style>'
);
?>
<h1>Your account</h1>
<p>
	<a class="button" rel="nofollow" href="/a11y/sidefx.php?add-to-cart=1">Add to cart</a>
	<a class="button" rel="nofollow" href="/a11y/sidefx.php/logout">Log out</a>
	<a class="button" rel="nofollow" href="/a11y/sidefx.php/download" download>Download invoice</a>
	<button type="button" id="receipt">Open receipt</button>
</p>
<p><label for="chat">Ask us a question</label> <input id="chat" name="chat" type="text"></p>
<script>
(function () {
	var calls = function (what) {
		if (window.testbedDry) { window.testbedDry.push(what); return true; }
		return false;
	};
	document.getElementById('receipt').addEventListener('click', function () {
		if (!calls('popup')) window.open('/a11y/sidefx.php/popup');
	});
	document.getElementById('chat').addEventListener('focus', function () {
		if (!calls('websocket')) new WebSocket('ws://' + location.host + '/a11y/ws.php');
	});
})();
</script>
<?php
testbed_selftest(
	<<<'JS'
	window.testbedDry = [];
	document.getElementById('chat').focus();
	document.getElementById('receipt').click();
	var seen = window.testbedDry.join(',');
	window.testbedDry = null;
	return seen === 'websocket,popup' ? true : seen;
JS
);
page_end();
