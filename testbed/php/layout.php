<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Shared page frame for the plain-PHP site (no framework, no WordPress).
// $head is extra markup for <head>. The frame passes the testbed's own
// accessibility audit (step zero in README.md): a skip link to #main as
// the first stop, landmarks, a visible focus ring and nothing wider than
// a 320 px phone. The a11y pages (a11y/) break one of these at a time.
// $viewport is the viewport meta's content (s66 changes it).
function page_start( string $title, string $head = '', string $viewport = 'width=device-width, initial-scale=1' ): void {
	testbed_gate();
	?><!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="<?= htmlspecialchars( $viewport ) ?>">
<link rel="icon" href="data:,">
<title><?= htmlspecialchars( $title ) ?> · Ledger</title>
<style>
body { margin: 0; font: 16px/1.6 system-ui, sans-serif; color: #1d2433; background: #f6f8fb; }
header, footer { background: #1d2433; color: #fff; padding: 16px 32px; }
header a, footer a { color: #fff; margin-right: 16px; }
main { max-width: 860px; margin: 0 auto; padding: 32px; }
table { border-collapse: collapse; width: 100%; }
td, th { border-bottom: 1px solid #d5dbe5; padding: 8px; text-align: left; }
img { max-width: 100%; height: auto; }
:focus-visible { outline: 3px solid #2f6fdb; outline-offset: 2px; }
header :focus-visible, footer :focus-visible { outline-color: #ffd23f; }
.skip-link { position: absolute; left: 8px; top: -48px; background: #fff; color: #1d2433; padding: 8px 16px; z-index: 10; }
.skip-link:focus { top: 8px; }
</style>
<?= $head ?>
</head>
<body>
<a class="skip-link" href="#main">Skip to content</a>
<header><strong>Ledger</strong> &nbsp; <nav aria-label="Main" style="display:inline"><a href="/">Home</a><a href="/about.php">About</a><a href="/report.php">Monthly report</a></nav></header>
<main id="main">
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
 * The state folder: TESTBED_STATE, or /var/www/state where the Docker
 * stack mounts it, so the site also runs with PHP's built-in server:
 * TESTBED_STATE=/tmp/s php -S 127.0.0.1:18810 -t testbed/php
 */
function testbed_state_dir(): string {
	return rtrim( getenv( 'TESTBED_STATE' ) ?: '/var/www/state', '/' );
}

/** Reports whether scenario $id is broken (testbed/toggle.sh). */
function testbed_broken( string $id ): bool {
	return file_exists( testbed_state_dir() . '/' . $id );
}

/**
 * Adds one to the counter <state>/<name>.txt and returns the new count,
 * or -1 when the file can't be written. The benchmark's orders harness
 * reads these counters (sidefx.txt for s88): they must not change while
 * Soundcheck walks a page by keyboard.
 */
function testbed_bump( string $name ): int {
	$f = @fopen( testbed_state_dir() . '/' . $name . '.txt', 'c+' );
	if ( ! $f ) {
		error_log( "testbed: can't write the $name counter in " . testbed_state_dir() );
		return -1;
	}
	flock( $f, LOCK_EX );
	$n = (int) stream_get_contents( $f ) + 1;
	ftruncate( $f, 0 );
	rewind( $f );
	fwrite( $f, $n . "\n" );
	flock( $f, LOCK_UN );
	fclose( $f );
	return $n;
}

/**
 * The canaries the benchmark wrote into <state>/canaries (name=value per
 * line), for s68 and s84. They are made at run time and never committed:
 * a value that shows up in any of Soundcheck's files was leaked. Without
 * the file there are none.
 */
function testbed_canaries(): array {
	$out = array();
	foreach ( @file( testbed_state_dir() . '/canaries', FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES ) ?: array() as $line ) {
		[ $k, $v ] = array_pad( explode( '=', $line, 2 ), 2, '' );
		$out[ trim( $k ) ] = trim( $v );
	}
	return $out;
}

/**
 * Prints the page's self-test (qa.md 6: a fixture whose script silently
 * does nothing makes a good state pass for the wrong reason). $js is the
 * body of a function that returns true, or a promise of true, when the
 * page behaves the way its state says. After load the result goes into
 * <body data-ok="1"> (or "0" with data-why), which the benchmark's
 * preflight reads. Focus is then put back at the top of the page, so a
 * keyboard walk starts where a visitor's would. Then a testbed-ready
 * event fires for pages that do something once the test is over.
 */
function testbed_selftest( string $js ): void {
	?>
<script>
(function () {
	function done(ok, why) {
		// Tab starts from the last focused place, so the test leaves it
		// on an empty element before the skip link. Chrome forgets that
		// place when the element is removed, so it stays.
		var top = document.createElement('span');
		top.tabIndex = -1;
		document.body.insertBefore(top, document.body.firstChild);
		top.focus({preventScroll: true});
		top.blur();
		window.scrollTo(0, 0);
		document.body.setAttribute('data-ok', ok ? '1' : '0');
		if (!ok) document.body.setAttribute('data-why', String(why));
		document.dispatchEvent(new Event('testbed-ready'));
	}
	window.addEventListener('load', function () {
		Promise.resolve().then(function () {
<?= $js ?>
		}).then(function (r) { done(r === true, r); }, function (e) { done(false, e && e.message); });
	});
})();
</script>
	<?php
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

/**
 * Where embed.php (s84) loads its cross-origin frame from:
 * TESTBED_FRAME_ORIGIN, or the request's host name swapped between
 * localhost and 127.0.0.1 with the port plus 2 (18810 gives 18812, the
 * Docker stack's frame port). The other host name makes the frame
 * cross-site as well as cross-origin; the port keeps it apart from the
 * page under PHP's built-in server too.
 */
function testbed_frame_origin(): string {
	$env = getenv( 'TESTBED_FRAME_ORIGIN' );
	if ( $env ) {
		return rtrim( $env, '/' );
	}
	$host = (string) ( $_SERVER['HTTP_HOST'] ?? 'localhost:80' );
	$name = (string) parse_url( 'http://' . $host, PHP_URL_HOST );
	$port = (int) ( parse_url( 'http://' . $host, PHP_URL_PORT ) ?: 80 );
	return 'http://' . ( '127.0.0.1' === $name ? 'localhost' : '127.0.0.1' ) . ':' . ( $port + 2 );
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
