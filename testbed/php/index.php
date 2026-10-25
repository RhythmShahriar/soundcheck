<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

require __DIR__ . '/layout.php';
page_start( 'Home' );
?>
<h1>Ledger</h1>
<p>Send invoices, track who has paid and see the month at a glance.</p>
<ul>
	<li><a href="/report.php">This month's report</a></li>
	<li><a href="/about.php">About Ledger</a></li>
</ul>
<h2>More pages</h2>
<ul>
	<li><a href="/ttfb.php">Clients</a> · <a href="/late.php">Team</a> · <a href="/banner.php">Plans</a> · <a href="/slow.php">Yearly summary</a></li>
	<li><a href="/archive.php">Archive</a> · <a href="/flaky.php">Payments</a> · <a href="/moved.php">Settings</a> · <a href="/mixed.php">Invoices</a></li>
</ul>
<?php
page_end();
