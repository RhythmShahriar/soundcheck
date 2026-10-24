<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Shared page frame for the plain-PHP site (no framework, no WordPress).
function page_start( string $title ): void {
	?><!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<link rel="icon" href="data:,">
<title><?= htmlspecialchars( $title ) ?> · Ledger</title>
<style>
body { margin: 0; font: 16px/1.6 system-ui, sans-serif; color: #1d2433; background: #f6f8fb; }
header, footer { background: #1d2433; color: #fff; padding: 16px 32px; }
header a, footer a { color: #fff; margin-right: 16px; }
main { max-width: 860px; margin: 0 auto; padding: 32px; }
table { border-collapse: collapse; width: 100%; }
td, th { border-bottom: 1px solid #d5dbe5; padding: 8px; text-align: left; }
</style>
</head>
<body>
<header><strong>Ledger</strong> &nbsp; <a href="/">Home</a><a href="/about.php">About</a><a href="/report.php">Monthly report</a></header>
<main>
	<?php
}

function page_end(): void {
	?>
</main>
<footer>Ledger, a small invoicing app written in plain PHP.</footer>
</body>
</html>
	<?php
}

/**
 * Reports whether scenario $id is broken (testbed/toggle.sh). The state
 * folder is TESTBED_STATE, or /var/www/state where the Docker stack
 * mounts it, so the site also runs with PHP's built-in server:
 * TESTBED_STATE=/tmp/s php -S 127.0.0.1:18810 -t testbed/php
 */
function testbed_broken( string $id ): bool {
	$dir = getenv( 'TESTBED_STATE' ) ?: '/var/www/state';
	return file_exists( rtrim( $dir, '/' ) . '/' . $id );
}
