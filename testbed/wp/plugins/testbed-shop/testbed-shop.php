<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name: Testbed Shop
 * Description: A stand-in for WooCommerce: [testbed_products] lists products with add-to-cart links, [testbed_cart] shows the cart kept in a cookie. The cart is private, so the page cache must bypass it (scenario s18 breaks that in the testbed-cache mu-plugin).
 * Version: 1.0.0
 */

defined( 'ABSPATH' ) || exit;

function testbed_products(): array {
	return array(
		11 => array( 'Ethiopia Yirgacheffe, 250 g', 9.50 ),
		12 => array( 'Colombia Huila, 250 g', 8.75 ),
		13 => array( 'House espresso, 1 kg', 28.00 ),
	);
}

// Adding to the cart: /cart/?add-to-cart=ID sets the cookie and redirects.
add_action(
	'template_redirect',
	function () {
		$id = absint( $_GET['add-to-cart'] ?? 0 ); // phpcs:ignore WordPress.Security.NonceVerification
		if ( $id && isset( testbed_products()[ $id ] ) ) {
			$cart   = testbed_cart();
			$cart[] = $id;
			setcookie( 'tb_cart', implode( '-', $cart ), 0, '/' );
			wp_safe_redirect( home_url( '/cart/' ) );
			exit;
		}
	}
);

function testbed_cart(): array {
	return array_values( array_filter( array_map( 'absint', explode( '-', (string) ( $_COOKIE['tb_cart'] ?? '' ) ) ) ) );
}

add_shortcode(
	'testbed_products',
	function () {
		$out = '<ul class="tb-products">';
		foreach ( testbed_products() as $id => $p ) {
			$out .= sprintf(
				'<li>%s, £%.2f <a class="button" href="%s">Add to cart</a></li>',
				esc_html( $p[0] ),
				$p[1],
				esc_url( home_url( '/cart/?add-to-cart=' . $id ) )
			);
		}
		return $out . '</ul>';
	}
);

add_shortcode(
	'testbed_cart',
	function () {
		$cart = testbed_cart();
		if ( ! $cart ) {
			return '<p class="cart-empty">Your cart is currently empty.</p><p><a href="' . esc_url( home_url( '/shop/' ) ) . '">Return to shop</a></p>';
		}
		$out   = '<table class="tb-cart"><tr><th>Product</th><th>Price</th></tr>';
		$total = 0;
		foreach ( $cart as $id ) {
			$p      = testbed_products()[ $id ] ?? null;
			$total += $p ? $p[1] : 0;
			$out   .= $p ? sprintf( '<tr><td>%s</td><td>£%.2f</td></tr>', esc_html( $p[0] ), $p[1] ) : '';
		}
		return $out . sprintf( '<tr><th>Total</th><th>£%.2f</th></tr></table>', $total );
	}
);
