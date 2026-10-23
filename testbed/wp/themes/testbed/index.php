<?php get_header(); ?>
<?php if ( is_home() ) : ?>
	<h1 class="entry-title">Blog</h1>
	<div class="post-list">
	<?php while ( have_posts() ) : the_post(); ?>
		<article>
			<h2><a href="<?php the_permalink(); ?>"><?php the_title(); ?></a></h2>
			<?php the_excerpt(); ?>
		</article>
	<?php endwhile; ?>
	</div>
<?php elseif ( have_posts() ) : ?>
	<?php while ( have_posts() ) : the_post(); ?>
		<article>
			<?php if ( ! is_front_page() ) : ?>
				<h1 class="entry-title"><?php the_title(); ?></h1>
			<?php endif; ?>
			<?php the_content(); ?>
		</article>
	<?php endwhile; ?>
<?php else : ?>
	<h1 class="entry-title">Page not found</h1>
	<p>Sorry, that page isn't here. Try the <a href="<?php echo esc_url( home_url( '/' ) ); ?>">home page</a>.</p>
<?php endif; ?>
<?php get_footer(); ?>
