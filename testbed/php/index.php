<?php
require __DIR__ . '/layout.php';
page_start( 'Home' );
?>
<h1>Ledger</h1>
<p>Send invoices, track who has paid, and see the month at a glance.</p>
<ul>
	<li><a href="/report.php">This month's report</a></li>
	<li><a href="/about.php">About Ledger</a></li>
</ul>
<?php
page_end();
