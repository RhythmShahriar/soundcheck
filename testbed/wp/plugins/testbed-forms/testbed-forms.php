<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name: Testbed Forms
 * Description: A contact form, [testbed_contact_form]. It posts to the page it's on, checks the fields and the nonce, then shows a success or error message after redirecting back. A good submission is counted; with mail="1" it is also emailed to the admin address. Scenario s11 broken: the mail step fails, so every valid message gets "could not be sent". Scenario s32 broken: a form with captcha="1" gets a reCAPTCHA that automated submits can't pass. Scenario s33 broken: a form with mail="1" says "sent" but sends nothing. A form with validate="1" (the contact page) checks its fields in the browser first; scenario s64 broken: it shows errors by colour alone.
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
				'captcha'  => '',
				'mail'     => '',
				'validate' => '',
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
		$validate = (bool) $atts['validate'];
		$field    = function ( string $id, string $label, string $control ) use ( $validate ): string {
			$error = $validate ? '<span class="tb-field-error" id="' . $id . '-error"></span>' : '';
			return '<label for="' . $id . '">' . $label . '</label>' . $control . $error;
		};
		$out .= '<form class="tb-contact" id="tb-contact" method="post" action=""' . ( $validate ? ' novalidate' : '' ) . '>'
			. wp_nonce_field( 'tb_contact', '_tb_nonce', true, false )
			. '<input type="hidden" name="tb_contact" value="1">'
			. ( $atts['mail'] ? '<input type="hidden" name="tb_mail" value="1">' : '' )
			. $field( 'tb_name', 'Your name (required)', '<input id="tb_name" name="tb_name" type="text" required autocomplete="name">' )
			. $field( 'tb_email', 'Your email (required)', '<input id="tb_email" name="tb_email" type="email" required autocomplete="email">' )
			. $field( 'tb_message', 'Your message (required)', '<textarea id="tb_message" name="tb_message" rows="5" required></textarea>' )
			. $captcha
			. '<button type="submit">Send message</button>'
			. '</form>';
		if ( $validate ) {
			$out .= testbed_contact_validation( testbed_broken( 's64' ) );
		}
		return $out;
	}
);

/**
 * The contact page's browser-side check (validate="1"). Good: each wrong
 * field gets aria-invalid and an error message tied to it with
 * aria-describedby. Focus moves to the first one. s64 broken: a
 * theme restyle shows errors by colour alone: the fields turn red with
 * no message, nothing is marked invalid and focus stays on the button.
 */
function testbed_contact_validation( bool $broken ): string {
	$messages = wp_json_encode(
		array(
			'tb_name'    => 'Enter your name.',
			'tb_email'   => 'Enter an email address like name@example.com.',
			'tb_message' => 'Write a message.',
		)
	);
	$broken = $broken ? 'true' : 'false';
	return <<<HTML
<script>
(function () {
	var form = document.getElementById('tb-contact');
	var messages = $messages;
	var colourOnly = $broken;
	form.addEventListener('submit', function (e) {
		var wrong = [];
		Object.keys(messages).forEach(function (id) {
			var field = document.getElementById(id);
			var error = document.getElementById(id + '-error');
			var ok = field.checkValidity();
			if (!ok) wrong.push(field);
			if (colourOnly) {
				field.classList.toggle('tb-invalid', !ok);
				return;
			}
			field.setAttribute('aria-invalid', String(!ok));
			error.textContent = ok ? '' : messages[id];
			if (ok) field.removeAttribute('aria-describedby');
			else field.setAttribute('aria-describedby', error.id);
		});
		if (wrong.length) {
			e.preventDefault();
			if (!colourOnly) wrong[0].focus();
		}
	});
})();
</script>
HTML;
}
