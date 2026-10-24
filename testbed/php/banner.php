<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

require __DIR__ . '/layout.php';

// Scenario s40, a snapshot whose screenshots are missing. The broken
// state adds a large red banner (a visual change); the benchmark deletes
// the snapshot's screenshots before comparing.
page_start( 'Plans' );
if ( testbed_broken( 's40' ) ) {
	echo '<div style="background:#c62828;color:#fff;font-size:40px;padding:80px 32px">Prices change on Monday</div>';
}
?>
<h1>Plans</h1>
<p>Starter is free for three clients. Pro is £9 a month for any number.</p>
<?php
page_end();
