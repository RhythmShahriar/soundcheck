<?php
require __DIR__ . '/layout.php';

// Scenario s03, "after a PHP upgrade". The good state is the code as it
// runs fine; the broken state is the legacy line that PHP 8.1+ deprecates
// (passing null to strlen()), as it shows up once a host moves the site to
// a newer PHP with display_errors on. The php:apache image has no php.ini,
// so PHP's defaults (display_errors on, E_ALL) apply.
$filter = $_GET['client'] ?? null;
if ( testbed_broken( 's03' ) ) {
	$filtered = strlen( $filter ) > 0;
} else {
	$filtered = strlen( $filter ?? '' ) > 0;
}

page_start( 'Monthly report' );
?>
<h1>Monthly report</h1>
<p><?= $filtered ? 'Showing one client.' : 'Showing all clients.' ?></p>
<table>
	<tr><th>Client</th><th>Invoiced</th><th>Paid</th></tr>
	<tr><td>Northwind</td><td>£4,200</td><td>£4,200</td></tr>
	<tr><td>Contoso</td><td>£1,850</td><td>£900</td></tr>
	<tr><td>Fabrikam</td><td>£3,100</td><td>£3,100</td></tr>
</table>
<?php
page_end();
