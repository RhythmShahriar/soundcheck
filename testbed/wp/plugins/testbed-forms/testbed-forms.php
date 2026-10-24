<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name: Testbed Forms
 * Description: A contact form, [testbed_contact_form]. It posts to the page it's on, checks the fields and the nonce, then shows a success or error message after redirecting back. A good submission is counted; with mail="1" it is also emailed to the admin address. Scenario s11 broken: the mail step fails, so every valid message gets "could not be sent". Scenario s32 broken: a form with captcha="1" gets a reCAPTCHA that automated submits can't pass. Scenario s33 broken: a form with mail="1" says "sent" but sends nothing.
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
		// s32 broken: a security plugin adds reCAPTCHA to the form. Without
		// a solved challenge (which a test browser never has) it refuses.
		if ( isset( $_POST['tb_captcha'] ) && '' === trim( (string) wp_unslash( $_POST['g-recaptcha-response'] ?? '' ) ) ) {
			wp_safe_redirect( add_query_arg( 'form', 'robot', $back ) );
			exit;
		}
		if ( testbed_broken( 's11' ) ) {
			// The mailer was misconfigured by an update: sending fails.
			wp_safe_redirect( add_query_arg( 'form', 'failed', $back ) );
			exit;
		}
		// s33 broken: an SMTP plugin update fails without telling anyone, so
		// the visitor still reads "sent" while no email goes out.
		if ( isset( $_POST['tb_mail'] ) && ! testbed_broken( 's33' ) ) {
			wp_mail(
				get_option( 'admin_email' ),
				'Wholesale enquiry from ' . $name,
				$message . "\n\nFrom: " . $name . ' <' . $email . '>',
				array( 'Reply-To: ' . $name . ' <' . $email . '>' )
			);
		}
		update_option( 'testbed_contact_count', (int) get_option( 'testbed_contact_count', 0 ) + 1, false );
		wp_safe_redirect( add_query_arg( 'form', 'sent', $back ) );
		exit;
	},
	5
);

add_shortcode(
	'testbed_contact_form',
	function ( $atts ) {
		$messages = array(
			'sent'    => array( 'form-success', 'status', 'Thank you for your message. It has been sent.' ),
			'invalid' => array( 'form-error', 'alert', 'One or more fields have an error. Please check and try again.' ),
			'failed'  => array( 'form-error', 'alert', 'There was an error trying to send your message. Please try again later.' ),
			'expired' => array( 'form-error', 'alert', 'The form expired. Please reload the page and try again.' ),
			'robot'   => array( 'form-error', 'alert', 'Please verify that you are not a robot.' ),
		);
		$atts = shortcode_atts(
			array(
				'captcha' => '',
				'mail'    => '',
			),
			$atts
		);
		$state = sanitize_key( $_GET['form'] ?? '' ); // phpcs:ignore WordPress.Security.NonceVerification
		$out   = '';
		// The markup Google's reCAPTCHA v2 widget needs; its script isn't
		// loaded, so the testbed never calls out to Google.
		$captcha = '';
		if ( $atts['captcha'] && testbed_broken( 's32' ) ) {
			$captcha = '<input type="hidden" name="tb_captcha" value="1">'
				. '<div class="g-recaptcha" data-sitekey="6LeTestbedLocalOnlyKeyNotRealxxxxxxxxxx"></div>'
				. '<textarea name="g-recaptcha-response" class="g-recaptcha-response" style="display:none"></textarea>';
		}
		if ( isset( $messages[ $state ] ) ) {
			[ $class, $role, $text ] = $messages[ $state ];
			$out .= sprintf( '<div class="form-message %s" role="%s">%s</div>', $class, $role, esc_html( $text ) );
		}
		$out .= '<form class="tb-contact" method="post" action="">'
			. wp_nonce_field( 'tb_contact', '_tb_nonce', true, false )
			. '<input type="hidden" name="tb_contact" value="1">'
			. ( $atts['mail'] ? '<input type="hidden" name="tb_mail" value="1">' : '' )
			. '<label for="tb_name">Your name (required)</label><input id="tb_name" name="tb_name" type="text" required autocomplete="name">'
			. '<label for="tb_email">Your email (required)</label><input id="tb_email" name="tb_email" type="email" required autocomplete="email">'
			. '<label for="tb_message">Your message (required)</label><textarea id="tb_message" name="tb_message" rows="5" required></textarea>'
			. $captcha
			. '<button type="submit">Send message</button>'
			. '</form>';
		return $out;
	}
);
