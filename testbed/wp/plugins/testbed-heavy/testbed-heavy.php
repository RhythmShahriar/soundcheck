<?php
/**
 * Plugin Name: Testbed Heavy Slider
 * Description: Scenario s20: "installing" a slider plugin that adds a 2 MB uncompressed image to the top of every page and a render-blocking script that answers slowly and then busies the main thread. In the good state the plugin does nothing, as if it weren't installed.
 * Version: 1.0.0
 */

defined( 'ABSPATH' ) || exit;

add_action(
	'wp_enqueue_scripts',
	function () {
		if ( testbed_broken( 's20' ) ) {
			// In the head, without defer or async: blocks rendering.
			wp_enqueue_script( 'testbed-heavy-slider', plugins_url( 'slow.php', __FILE__ ), array(), '1.0.0', false );
		}
	}
);

add_action(
	'wp_body_open',
	function () {
		if ( testbed_broken( 's20' ) ) {
			printf(
				'<div class="heavy-slider"><img src="%s" width="1000" height="700" alt="Our autumn roasts"></div>',
				esc_url( plugins_url( 'slide.php', __FILE__ ) )
			);
		}
	}
);
