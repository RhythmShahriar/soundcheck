<?php
// A render-blocking slider script: the server takes 1.5 s to answer, then
// the script keeps the main thread busy for 400 ms.
usleep( 1500000 );
header( 'Content-Type: application/javascript; charset=utf-8' );
header( 'Cache-Control: no-cache' );
echo "(function(){var t=Date.now();while(Date.now()-t<400){}document.documentElement.classList.add('heavy-slider-ready');})();\n";
