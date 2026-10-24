<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT
?>
</main>
<footer class="site-footer">
	<p>&copy; Brew &amp; Co &middot; Roasted in small batches &middot; <a href="<?php echo esc_url( home_url( '/contact/' ) ); ?>">Contact us</a></p>
	<?php
	if ( testbed_broken( 's02' ) ) {
		// s02 broken: theme 1.1 reads a footer setting older installs don't
		// have, on a host that displays PHP errors.
		ini_set( 'display_errors', '1' ); // phpcs:ignore WordPress.PHP.IniSet
		$tb_footer = get_option( 'testbed_footer', array() );
		echo '<p class="footer-note">' . esc_html( $tb_footer['note'] ) . '</p>';
	}
	?>
</footer>
<?php wp_footer(); ?>
<script>jQuery(function ($) { $('body').addClass('js-ready'); });</script>
</body>
</html>
