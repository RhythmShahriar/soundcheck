<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name: Testbed Shop
 * Description: A stand-in for WooCommerce: [testbed_products] lists products with add-to-cart links, [testbed_cart] shows the cart kept in a cookie, [testbed_checkout] takes the order. The cart is private, so the page cache must bypass it (scenario s18 breaks that in the testbed-cache mu-plugin). Scenario s31 broken: placing an order hits a fatal error in the payment step.
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

// Placing an order: the checkout form posts back to /checkout/.
add_action(
	'template_redirect',
	function () {
		if ( 'POST' !== ( $_SERVER['REQUEST_METHOD'] ?? '' ) || ! isset( $_POST['tb_checkout'] ) ) {
			return;
		}
		$back = home_url( '/checkout/' );
		if ( ! wp_verify_nonce( sanitize_text_field( wp_unslash( $_POST['_tb_checkout_nonce'] ?? '' ) ), 'tb_checkout' ) ) {
			wp_safe_redirect( add_query_arg( 'order', 'expired', $back ) );
			exit;
		}
		$name    = sanitize_text_field( wp_unslash( $_POST['tb_billing_name'] ?? '' ) );
		$email   = sanitize_email( wp_unslash( $_POST['tb_billing_email'] ?? '' ) );
		$address = sanitize_textarea_field( wp_unslash( $_POST['tb_billing_address'] ?? '' ) );
		if ( '' === $name || ! is_email( $email ) || '' === $address ) {
			wp_safe_redirect( add_query_arg( 'order', 'invalid', $back ) );
			exit;
		}
		if ( testbed_broken( 's31' ) ) {
			// s31: a payment gateway update calls a class its new version
			// renamed, so placing an order is a fatal error (WordPress's
			// critical error screen, 500).
			$gateway = 'Testbed_Gateway_Pay_On_Delivery';
			( new $gateway() )->process_payment( $name );
		}
		update_option( 'testbed_order_count', (int) get_option( 'testbed_order_count', 0 ) + 1, false );
		wp_safe_redirect( add_query_arg( 'order', 'received', $back ) );
		exit;
	},
	5
);

add_shortcode(
	'testbed_checkout',
	function () {
		$state = sanitize_key( $_GET['order'] ?? '' ); // phpcs:ignore WordPress.Security.NonceVerification
		$notes = array(
			'received' => array( 'order-received', 'status', 'Thank you. Your order has been received.' ),
			'invalid'  => array( 'order-error', 'alert', 'Please fill in your name, email and address.' ),
			'expired'  => array( 'order-error', 'alert', 'Your session expired. Please try again.' ),
		);
		$out = '';
		if ( isset( $notes[ $state ] ) ) {
			[ $class, $role, $text ] = $notes[ $state ];
			$out .= sprintf( '<div class="%s" role="%s">%s</div>', $class, $role, esc_html( $text ) );
		}
		return $out . '<form class="tb-checkout" method="post" action="">'
			. wp_nonce_field( 'tb_checkout', '_tb_checkout_nonce', true, false )
			. '<input type="hidden" name="tb_checkout" value="1">'
			. '<label for="tb_billing_name">Full name</label><input id="tb_billing_name" name="tb_billing_name" type="text" required autocomplete="name">'
			. '<label for="tb_billing_email">Email address</label><input id="tb_billing_email" name="tb_billing_email" type="email" required autocomplete="email">'
			. '<label for="tb_billing_address">Delivery address</label><textarea id="tb_billing_address" name="tb_billing_address" rows="3" required></textarea>'
			. '<p>Payment: pay on delivery.</p>'
			. '<button type="submit" name="tb_place_order">Place order</button>'
			. '</form>';
	}
);
