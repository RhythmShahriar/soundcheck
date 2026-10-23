#!/bin/sh
# Seeds the testbed WordPress site: install, theme, plugins, users, pages
# for every scenario, a few posts and the menu. Run by `make up` through
# `docker compose run --rm cli`; does nothing if the site is installed.
set -eu
cd /var/www/html

for _ in $(seq 1 90); do
	[ -f wp-config.php ] && [ -f wp-includes/version.php ] && break
	sleep 1
done
for _ in $(seq 1 60); do
	php -r 'exit(@mysqli_connect(getenv("WORDPRESS_DB_HOST"), getenv("WORDPRESS_DB_USER"), getenv("WORDPRESS_DB_PASSWORD"), getenv("WORDPRESS_DB_NAME")) ? 0 : 1);' && break
	sleep 1
done

if wp core is-installed 2>/dev/null; then
	echo "testbed: already seeded"
	exit 0
fi

URL=${TESTBED_URL:-http://localhost:18800}
wp core install --url="$URL" --title="Brew & Co" --admin_user=admin --admin_password=admin \
	--admin_email=admin@testbed.invalid --skip-email
wp option update blogdescription "Fresh coffee, roasted in small batches"
wp option update timezone_string "Europe/London"
wp rewrite structure '/%postname%/'
cat > .htaccess <<'EOF'
# BEGIN WordPress
<IfModule mod_rewrite.c>
RewriteEngine On
RewriteRule .* - [E=HTTP_AUTHORIZATION:%{HTTP:Authorization}]
RewriteBase /
RewriteRule ^index\.php$ - [L]
RewriteCond %{REQUEST_FILENAME} !-f
RewriteCond %{REQUEST_FILENAME} !-d
RewriteRule . /index.php [L]
</IfModule>
# END WordPress
EOF

wp theme activate testbed
wp plugin activate testbed-pricing testbed-widgets testbed-shop testbed-forms testbed-heavy
wp plugin delete akismet hello >/dev/null 2>&1 || true
wp post delete 1 2 3 --force >/dev/null 2>&1 || true # sample post, page and privacy page

# The account Soundcheck logs in with (cache leak, logged-in checks):
# SOUNDCHECK_USER=soundcheck SOUNDCHECK_PASSWORD=soundcheck-testbed
wp user create soundcheck soundcheck@testbed.invalid --role=editor --user_pass=soundcheck-testbed \
	--display_name="Sam Soundcheck" --first_name=Sam --last_name=Soundcheck

# page SLUG TITLE CONTENT [PARENT_ID] prints the new page's ID.
page() {
	wp post create --post_type=page --post_status=publish --post_name="$1" --post_title="$2" \
		--post_content="$3" --post_parent="${4:-0}" --porcelain
}

HERO='<section class="hero"><h2>Roasted on Monday, at your door by Wednesday.</h2><p>Single-origin beans from farms we visit every year.</p></section>'

home=$(page home "Home" "$HERO
<p>Brew &amp; Co is a small roastery. We roast twice a week and ship whole beans or ground to order.</p>
<p><a href=\"/shop/\">Browse the coffee</a> or read our <a href=\"/blog/\">brewing guides</a>.</p>")
blog=$(page blog "Blog" "")
page about "About us" "<p>We started roasting in a garage in 2014. Today six of us roast, pack and ship every order by hand.</p><p>Read about the <a href=\"/s17-title/\">history of the roastery</a>.</p>" >/dev/null
page shop "Shop" "<p>Whole beans, roasted this week.</p>[testbed_products]<p><a href=\"/cart/\">View cart</a></p>" >/dev/null
page cart "Cart" "[testbed_cart]" >/dev/null
page contact "Contact" "<p>Questions about an order? Write to us and we'll answer within a day.</p>[testbed_contact_form]" >/dev/null

# One page per WordPress scenario; testbed/scenarios.yaml lists them.
page s01-fatal "Subscription plans" "<p>Choose how much coffee you want each month.</p>[testbed_pricing]" >/dev/null
page s02-warning "Opening hours" "<p>Our roastery shop is open Tuesday to Saturday, 9am to 5pm.</p>" >/dev/null
page s04-blank "Gift cards" "<p>Gift cards from £10, delivered by email. They never expire.</p>" >/dev/null
page s05-js-error "Find a shop" "<p>Visit one of our cafés:</p><div id=\"store-locator\">Loading shops…</div>" >/dev/null
page s06-jquery "Gallery" "<p>Photos from the roastery and our farm visits.</p>" >/dev/null
page s07-ajax "Stock" "<p>Ethiopia Yirgacheffe: <span class=\"tb-stock\" data-product=\"yirgacheffe\">checking stock…</span></p>" >/dev/null
page s08-rest "Reviews" "<p>What customers say:</p><div class=\"tb-reviews\">Loading reviews…</div>" >/dev/null
offers=$(page s09-links "Offers" "<p>This month: <a href=\"/s09-links/spring-offer/\">our spring offer</a>, 20% off every 1 kg bag.</p>")
page spring-offer "Spring offer" "<p>20% off every 1 kg bag until the end of the month.</p>" "$offers" >/dev/null
page s10-image "Our team" "<p>Meet the six people who roast, pack and ship your coffee.</p><p><img src=\"/wp-content/plugins/testbed-shop/assets/team.svg\" width=\"640\" height=\"320\" alt=\"The Brew &amp; Co team\"></p>" >/dev/null
page s11-form "Wholesale enquiries" "<p>Cafés and offices: tell us what you need.</p>[testbed_contact_form]" >/dev/null
page s12-menu "Brewing guides" "<p>Use the <strong>Browse</strong> button in the header to find guides by method.</p>" >/dev/null
page s13-button "Gesha, 100 g" "<p>A rare washed Gesha with jasmine and bergamot notes. £24.</p><button type=\"button\" class=\"tb-wishlist\" data-product=\"gesha-100\">Add to wishlist</button> <span class=\"tb-wishlist-status\" aria-live=\"polite\"></span>" >/dev/null
page s14-layout "Autumn roasts" "$HERO
<p>Three new roasts for autumn: a Kenyan, a Guatemalan and a decaf.</p>" >/dev/null
page s15-mobile "Price list" "<div class=\"promo-strip\">Free delivery on orders over £30</div><table><tr><th>Coffee</th><th>250 g</th><th>1 kg</th></tr><tr><td>Yirgacheffe</td><td>£9.50</td><td>£32</td></tr><tr><td>Huila</td><td>£8.75</td><td>£29</td></tr><tr><td>House espresso</td><td>£8.00</td><td>£28</td></tr></table>" >/dev/null
page s16-noindex "Press" "<p>For press enquiries and images, contact press@brew.invalid.</p>" >/dev/null
page s17-title "Our history" "<p>From a garage roaster in 2014 to a team of six.</p>" >/dev/null
page s19-account "Members" "<p>Members get early access to new roasts and 10% off every order.</p>" >/dev/null
page s20-heavy "Events" "<p>Cupping sessions every first Saturday of the month.</p>" >/dev/null

{
	echo "<p>Every page below is one benchmark scenario (testbed/scenarios.yaml).</p><ul>"
	for slug in s01-fatal s02-warning s04-blank s05-js-error s06-jquery s07-ajax s08-rest s09-links s10-image \
		s11-form s12-menu s13-button s14-layout s15-mobile s16-noindex s17-title s19-account s20-heavy shop cart; do
		echo "<li><a href=\"/$slug/\">/$slug/</a></li>"
	done
	echo "</ul>"
} > /tmp/scenarios.html
page scenarios "Scenarios" "$(cat /tmp/scenarios.html)" >/dev/null

wp option update show_on_front page
wp option update page_on_front "$home"
wp option update page_for_posts "$blog"

cat_guides=$(wp term create category "Brewing guides" --slug=guides --porcelain)
wp post create --post_status=publish --post_title="How to brew with a V60" --post_category="$cat_guides" \
	--post_content="<p>Use 15 g of coffee to 250 g of water at 94°C. Bloom for 30 seconds, then pour in slow circles.</p>" >/dev/null
wp post create --post_status=publish --post_title="Choosing a grinder" --post_category="$cat_guides" \
	--post_content="<p>A burr grinder gives an even grind; that matters more than the brewer.</p>" >/dev/null
wp post create --post_status=publish --post_title="Our trip to Huila" \
	--post_content="<p>In March we visited the farmers who grow our Colombian coffee.</p>" >/dev/null

wp menu create "Primary" >/dev/null
for slug in home about blog shop cart contact scenarios; do
	id=$(wp post list --post_type=page --name="$slug" --field=ID)
	wp menu item add-post primary "$id" >/dev/null
done
wp menu location assign primary primary

wp rewrite flush
echo "testbed: seeded $URL"
