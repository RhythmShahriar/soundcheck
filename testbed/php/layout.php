<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Shared page frame for the plain-PHP site (no framework, no WordPress).
// $head is extra markup for <head>.
function page_start( string $title, string $head = '' ): void {
	testbed_gate();
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
<?= $head ?>
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

/**
 * Site-wide breaks that sit in front of the whole site, as a firewall or
 * the host's access rules would. They answer before any page renders.
 */
function testbed_gate(): void {
	// s36: the host's firewall starts showing a Cloudflare-style bot
	// challenge to the browser instead of the site.
	if ( testbed_broken( 's36' ) ) {
		http_response_code( 403 );
		header( 'cf-mitigated: challenge' );
		header( 'Cache-Control: private, max-age=0, no-store' );
		?><!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Just a moment...</title>
<script src="/cdn-cgi/challenge-platform/h/b/orchestrate/chl_page/v1?ray=testbed"></script></head>
<body><h1>Checking your browser before accessing the site.</h1><p>This process is automatic.</p></body></html>
		<?php
		exit;
	}
	// s37: the staging site is put behind HTTP basic auth.
	if ( testbed_broken( 's37' ) ) {
		http_response_code( 401 );
		header( 'WWW-Authenticate: Basic realm="Staging", charset="UTF-8"' );
		echo "<!doctype html>\n<title>401 Unauthorized</title><h1>Unauthorized</h1><p>This server could not verify that you are authorized to access the document requested.</p>\n";
		exit;
	}
}

/** The site's own scheme, host and port, from the request. */
function testbed_origin(): string {
	$https = ( $_SERVER['HTTPS'] ?? '' ) !== '' && ( $_SERVER['HTTPS'] ?? '' ) !== 'off';
	return ( $https ? 'https' : 'http' ) . '://' . ( $_SERVER['HTTP_HOST'] ?? 'localhost' );
}

/**
 * Counts calls for $name across requests (in the temp folder, so it works
 * with the state folder mounted read-only) and returns the new count.
 */
function testbed_count( string $name ): int {
	$f = fopen( sys_get_temp_dir() . '/testbed-count-' . $name, 'c+' );
	if ( ! $f ) {
		return 1;
	}
	flock( $f, LOCK_EX );
	$n = (int) stream_get_contents( $f ) + 1;
	ftruncate( $f, 0 );
	rewind( $f );
	fwrite( $f, (string) $n );
	flock( $f, LOCK_UN );
	fclose( $f );
	return $n;
}
