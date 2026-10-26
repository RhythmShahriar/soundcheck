<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT
?>
<!doctype html>
<html <?php language_attributes(); ?>>
<head>
<meta charset="<?php bloginfo( 'charset' ); ?>">
<meta name="viewport" content="width=device-width, initial-scale=1">
<?php wp_head(); ?>
</head>
<body <?php body_class(); ?>>
<?php wp_body_open(); ?>
<?php // s80 (hard good): the 1.1 skip link hides another way (clip) and still shows on focus. ?>
<a class="skip-link<?php echo testbed_broken( 's80' ) ? ' skip-link-clip' : ''; ?>" href="#main">Skip to content</a>
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
	<?php if ( testbed_broken( 's56' ) ) : // s56: the header 2.0 renders the toggle as a div; menu.js still binds its click. ?>
	<div class="menu-toggle" aria-controls="browse-panel" aria-expanded="false">Browse</div>
	<?php else : ?>
	<button type="button" class="menu-toggle" aria-controls="browse-panel" aria-expanded="false">Browse</button>
	<?php endif; ?>
</header>
<div id="browse-panel" class="browse-panel" hidden>
	<ul>
		<li><a href="<?php echo esc_url( home_url( '/shop/' ) ); ?>">Coffee beans</a></li>
		<li><a href="<?php echo esc_url( home_url( '/blog/' ) ); ?>">Brewing guides</a></li>
		<li><a href="<?php echo esc_url( home_url( '/s09-links/' ) ); ?>">Offers</a></li>
	</ul>
</div>
<?php // s55: the header 2.0 renames the main element's id, so the skip link points nowhere. ?>
<main id="<?php echo testbed_broken( 's55' ) ? 'content' : 'main'; ?>" class="site-main">
