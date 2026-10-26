<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name: Testbed Shop
 * Description: A stand-in for WooCommerce: [testbed_products] lists products with add-to-cart links, [testbed_cart] shows the cart kept in a cookie, [testbed_checkout] takes the order (counted in state/orders.txt), [testbed_account] shows the logged-in customer's details. The cart is private, so the page cache must bypass it (scenario s18 breaks that in the testbed-cache mu-plugin). Scenario s31 broken: placing an order hits a fatal error in the payment step; s86 broken: so does showing the checkout. The checkout's accessibility scenarios (s49, s50, s59, s60, s61, s70) and the account form's (s68) are switched in testbed_checkout_form() and testbed_account().
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
		testbed_bump( 'orders' ); // the benchmark's orders harness (s70)
		wp_safe_redirect( add_query_arg( 'order', 'received', $back ) );
		exit;
	},
	5
);

// s86: the payment gateway update from s31 also breaks loading the
// checkout page, before anything is sent, so the page is WordPress's
// critical error screen (500) and nothing on it can be audited.
add_action(
	'template_redirect',
	function () {
		if ( testbed_broken( 's86' ) && is_page( 'checkout' ) ) {
			$gateway = 'Testbed_Gateway_Pay_On_Delivery';
			( new $gateway() )->payment_fields();
		}
	},
	6
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
		return $out . testbed_checkout_form() . do_shortcode( '[testbed_newsletter]' );
	}
);

/**
 * The checkout form. Good: every field has a <label for> and a valid
 * autocomplete token, the coupon field lets Tab through and Place order
 * is a real, dark button. Each broken state changes one thing:
 *
 *   s49  the coupon field's script cancels Tab and Shift+Tab (a trap)
 *   s50  name, email and address lose their <label for> (plain text)
 *   s59  the autocomplete tokens become autocomplete="nope"
 *   s60  the labels are replaced by placeholders
 *   s61  Place order's text turns #c8c8c8 on white
 *   s70  Place order is taken out of the Tab order (tabindex=-1) and
 *        submits from a click handler, so only a mouse can place an order
 */
function testbed_checkout_form(): string {
	$fields = array(
		array( 'tb_billing_name', 'Full name', 'input type="text"', 'name' ),
		array( 'tb_billing_email', 'Email address', 'input type="email"', 'email' ),
		array( 'tb_billing_address', 'Delivery address', 'textarea rows="3"', 'street-address' ),
	);
	$out = '<form class="tb-checkout" id="tb-checkout" method="post" action="">'
		. wp_nonce_field( 'tb_checkout', '_tb_checkout_nonce', true, false )
		. '<input type="hidden" name="tb_checkout" value="1">';
	foreach ( $fields as [ $id, $label, $tag, $token ] ) {
		$auto  = testbed_broken( 's59' ) ? 'nope' : $token;
		$attrs = sprintf( 'id="%1$s" name="%1$s" required autocomplete="%2$s"', $id, $auto );
		if ( testbed_broken( 's60' ) ) {
			$attrs .= ' placeholder="' . esc_attr( $label ) . '"';
		} elseif ( testbed_broken( 's50' ) ) {
			$out .= '<span class="tb-label">' . esc_html( $label ) . '</span>';
		} else {
			$out .= '<label for="' . $id . '">' . esc_html( $label ) . '</label>';
		}
		[ $name ] = explode( ' ', $tag );
		$out     .= '<' . $tag . ' ' . $attrs . '>' . ( 'textarea' === $name ? '</textarea>' : '' );
	}
	$out .= '<label for="tb_coupon">Coupon code</label><input id="tb_coupon" name="tb_coupon" type="text" autocomplete="off">';
	$out .= '<p>Payment: pay on delivery.</p>';
	$style = testbed_broken( 's61' ) ? ' style="color:#c8c8c8;background:#fff;border:1px solid #c8c8c8"' : '';
	if ( testbed_broken( 's70' ) ) {
		$out .= '<button type="button" id="place_order" name="tb_place_order" tabindex="-1"' . $style . '>Place order</button>';
	} else {
		$out .= '<button type="submit" id="place_order" name="tb_place_order"' . $style . '>Place order</button>';
	}
	$out .= '</form>';
	if ( testbed_broken( 's49' ) ) {
		// A coupon plugin's "smart" field swallows Tab and Shift+Tab to
		// keep the visitor typing the code.
		$out .= "<script>document.getElementById('tb_coupon').addEventListener('keydown', function (e) { if (e.key === 'Tab') e.preventDefault(); });</script>";
	}
	if ( testbed_broken( 's70' ) ) {
		$out .= "<script>document.getElementById('place_order').addEventListener('click', function () { document.getElementById('tb-checkout').requestSubmit(); });</script>";
	}
	return $out;
}

// [testbed_account] (s68): the logged-in customer's details, prefilled
// with the canaries the benchmark wrote into the state folder (field
// values, a data attribute, an aria-label and a link's query string),
// so a leak of any of them into Soundcheck's files is caught. s68 broken:
// the phone field loses its label in a template update.
add_shortcode(
	'testbed_account',
	function () {
		if ( ! is_user_logged_in() ) {
			return '<p>Please <a href="' . esc_url( wp_login_url( home_url( '/my-account/' ) ) ) . '">log in</a> to see your account.</p>';
		}
		$c     = testbed_canaries();
		$email = $c['email'] ?? wp_get_current_user()->user_email;
		$out   = '<form class="tb-checkout" id="tb-account" method="post" action="" data-token="' . esc_attr( $c['token'] ?? '' ) . '" onsubmit="return false">'
			. '<label for="acc_name">Display name</label><input id="acc_name" name="acc_name" type="text" autocomplete="name" value="' . esc_attr( wp_get_current_user()->display_name ) . '">'
			. '<label for="acc_email">Email address</label><input id="acc_email" name="acc_email" type="email" autocomplete="email" value="' . esc_attr( $email ) . '">'
			. ( testbed_broken( 's68' ) ? '' : '<label for="acc_phone">Phone</label>' )
			. '<input id="acc_phone" name="acc_phone" type="tel" autocomplete="tel" value="' . esc_attr( $c['phone'] ?? '' ) . '">'
			. '<button type="button" aria-label="' . esc_attr( 'Send a sign-in link to ' . $email ) . '">Send a sign-in link</button>'
			. '</form>';
		return $out . '<p><a rel="nofollow" href="' . esc_url( add_query_arg( 'token', $c['token'] ?? '', home_url( '/my-account/' ) ) ) . '">Download your data</a></p>';
	}
);
