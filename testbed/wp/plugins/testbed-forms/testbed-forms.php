<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name: Testbed Forms
 * Description: A contact form, [testbed_contact_form]. It posts to the page it's on, checks the fields and the nonce, and shows a success or error message after redirecting back. Nothing is emailed: a good submission is only counted. Scenario s11 broken: the mail step fails, so every valid message gets "could not be sent".
 * Version: 1.0.0
 */

defined( 'ABSPATH' ) || exit;

add_action(
	'template_redirect',
	function () {
		if ( 'POST' !== ( $_SERVER['REQUEST_METHOD'] ?? '' ) || ! isset( $_POST['tb_contact'] ) ) {
			return;
		}
		$back = remove_query_arg( 'form', wp_get_referer() ?: home_url( '/contact/' ) );
		if ( ! wp_verify_nonce( sanitize_text_field( wp_unslash( $_POST['_tb_nonce'] ?? '' ) ), 'tb_contact' ) ) {
			wp_safe_redirect( add_query_arg( 'form', 'expired', $back ) );
			exit;
		}
		$name    = sanitize_text_field( wp_unslash( $_POST['tb_name'] ?? '' ) );
		$email   = sanitize_email( wp_unslash( $_POST['tb_email'] ?? '' ) );
		$message = sanitize_textarea_field( wp_unslash( $_POST['tb_message'] ?? '' ) );
		if ( '' === $name || ! is_email( $email ) || '' === $message ) {
			wp_safe_redirect( add_query_arg( 'form', 'invalid', $back ) );
			exit;
		}
		if ( testbed_broken( 's11' ) ) {
			// The mailer was misconfigured by an update: sending fails.
			wp_safe_redirect( add_query_arg( 'form', 'failed', $back ) );
			exit;
		}
		update_option( 'testbed_contact_count', (int) get_option( 'testbed_contact_count', 0 ) + 1, false );
		wp_safe_redirect( add_query_arg( 'form', 'sent', $back ) );
		exit;
	},
	5
);

add_shortcode(
	'testbed_contact_form',
	function () {
		$messages = array(
			'sent'    => array( 'form-success', 'status', 'Thank you for your message. It has been sent.' ),
			'invalid' => array( 'form-error', 'alert', 'One or more fields have an error. Please check and try again.' ),
			'failed'  => array( 'form-error', 'alert', 'There was an error trying to send your message. Please try again later.' ),
			'expired' => array( 'form-error', 'alert', 'The form expired. Please reload the page and try again.' ),
		);
		$state = sanitize_key( $_GET['form'] ?? '' ); // phpcs:ignore WordPress.Security.NonceVerification
		$out   = '';
		if ( isset( $messages[ $state ] ) ) {
			[ $class, $role, $text ] = $messages[ $state ];
			$out .= sprintf( '<div class="form-message %s" role="%s">%s</div>', $class, $role, esc_html( $text ) );
		}
		$out .= '<form class="tb-contact" method="post" action="">'
			. wp_nonce_field( 'tb_contact', '_tb_nonce', true, false )
			. '<input type="hidden" name="tb_contact" value="1">'
			. '<label for="tb_name">Your name (required)</label><input id="tb_name" name="tb_name" type="text" required autocomplete="name">'
			. '<label for="tb_email">Your email (required)</label><input id="tb_email" name="tb_email" type="email" required autocomplete="email">'
			. '<label for="tb_message">Your message (required)</label><textarea id="tb_message" name="tb_message" rows="5" required></textarea>'
			. '<button type="submit">Send message</button>'
			. '</form>';
		return $out;
	}
);
