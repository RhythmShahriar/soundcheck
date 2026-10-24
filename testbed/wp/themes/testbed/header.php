// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

<!doctype html>
<html <?php language_attributes(); ?>>
<head>
<meta charset="<?php bloginfo( 'charset' ); ?>">
<meta name="viewport" content="width=device-width, initial-scale=1">
<?php wp_head(); ?>
</head>
<body <?php body_class(); ?>>
<?php wp_body_open(); ?>
<header class="site-header">
	<a class="brand" href="<?php echo esc_url( home_url( '/' ) ); ?>"><?php bloginfo( 'name' ); ?></a>
	<nav class="primary-nav" aria-label="Primary">
		<?php
		wp_nav_menu(
			array(
				'theme_location' => 'primary',
				'container'      => false,
				'fallback_cb'    => false,
			)
		);
		?>
	</nav>
	<button type="button" class="menu-toggle" aria-controls="browse-panel" aria-expanded="false">Browse</button>
</header>
<div id="browse-panel" class="browse-panel" hidden>
	<ul>
		<li><a href="<?php echo esc_url( home_url( '/shop/' ) ); ?>">Coffee beans</a></li>
		<li><a href="<?php echo esc_url( home_url( '/blog/' ) ); ?>">Brewing guides</a></li>
		<li><a href="<?php echo esc_url( home_url( '/s09-links/' ) ); ?>">Offers</a></li>
	</ul>
</div>
<main class="site-main">
