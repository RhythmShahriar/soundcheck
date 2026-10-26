<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// The WebSocket endpoint sidefx.php (s88) opens when its chat field gets
// focus. Neither Apache's PHP nor PHP's built-in server can upgrade a
// connection, so this only counts: any request that reaches it adds one
// to <state>/sidefx.txt, which must not happen during a keyboard walk.

require __DIR__ . '/../layout.php';
testbed_bump( 'sidefx' );
http_response_code( 426 );
header( 'Upgrade: websocket' );
header( 'Content-Type: text/plain; charset=utf-8' );
echo "Upgrade Required\n";
