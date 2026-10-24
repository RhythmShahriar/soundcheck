<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// A 1000×700 PNG of random pixels, stored uncompressed: about 2 MB that no
// encoder can shrink, like an unoptimized photo. Generated once per
// container and kept in /tmp, so the repository stays free of binaries.
$file = sys_get_temp_dir() . '/testbed-slide.png';
if ( ! is_file( $file ) ) {
	$w   = 1000;
	$h   = 700;
	$img = imagecreatetruecolor( $w, $h );
	mt_srand( 20 );
	for ( $y = 0; $y < $h; $y++ ) {
		for ( $x = 0; $x < $w; $x++ ) {
			imagesetpixel( $img, $x, $y, mt_rand( 0, 0xFFFFFF ) );
		}
	}
	imagepng( $img, $file . '.tmp', 0 );
	rename( $file . '.tmp', $file );
}
header( 'Content-Type: image/png' );
header( 'Content-Length: ' . filesize( $file ) );
header( 'Cache-Control: no-cache' );
readfile( $file );
