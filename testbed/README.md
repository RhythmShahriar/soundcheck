# Soundcheck testbed

89 broken-site scenarios in one Docker Compose stack, for Soundcheck's end-to-end tests and its public benchmark ([docs/benchmark.md](../docs/benchmark.md)).

Each scenario breaks one thing the way a plugin update, a theme update, a PHP upgrade, a host or a setting would, or puts Soundcheck itself in a hard spot (Ctrl+C, two runs at once, a busy machine). Every scenario has a **good** and a **broken** state. `toggle.sh` switches between them right away, without a rebuild or restart.

## Start

```sh
make -C testbed up      # about 30 s once the images are pulled
```

| Site | URL | What it is |
|---|---|---|
| WordPress | http://localhost:18800/ | WordPress 6.8.3 on PHP 8.3 with MariaDB 11.4: a coffee roaster's site with pages, posts, a menu, a contact form, a shop and a cart. Each WordPress scenario has its own page (`/s01-fatal/`, `/s02-warning/`…) and `/scenarios/` lists them. |
| Plain PHP | http://localhost:18810/ | "Ledger", a few pages of plain PHP 8.3 (no framework), for s03 and s34 to s45 |
| Plain PHP, https | https://localhost:18811/ | the same site over https with a self-signed certificate made at start (use `-k`), for s25 |
| Plain PHP, frame origin | http://127.0.0.1:18812/ | the same site again, as another origin: `a11y/embed.php` (s84) loads its cross-origin frame from it |

Every `make up` installs and seeds a fresh site. The database lives in tmpfs and `make down` deletes the WordPress volume, so every run starts from the same state. Ports are bound to 127.0.0.1. Set `TESTBED_WP_PORT` / `TESTBED_PHP_PORT` / `TESTBED_PHP_HTTPS_PORT` / `TESTBED_PHP_FRAME_PORT` to move them. The benchmark and the tests read the same variables (or pass `-wp`/`-php`/`-php-https` to the benchmark).

Accounts (local test accounts only):

| User | Password | Role | Used for |
|---|---|---|---|
| `admin` | `admin` | administrator | looking around in wp-admin |
| `soundcheck` | `soundcheck-testbed` | editor, display name "Sam Soundcheck" | Soundcheck's logged-in checks: `SOUNDCHECK_USER=soundcheck SOUNDCHECK_PASSWORD=soundcheck-testbed` |

### Plain PHP without Docker

The plain-PHP site also runs under PHP's built-in server. Point it and `toggle.sh` at the same state folder with `TESTBED_STATE` (the containers use `testbed/state`):

```sh
mkdir -p /tmp/s
TESTBED_STATE=/tmp/s php -S 127.0.0.1:18810 -t testbed/php
TESTBED_STATE=/tmp/s testbed/toggle.sh s03 broken   # in another terminal
```

If your `php.ini` hides errors (`display_errors = Off`), add `-d display_errors=1` so s03's notice shows. For s84's cross-origin frame, serve the same folder a second time on the port two above, so `a11y/embed.php` on localhost:18810 can load it from 127.0.0.1:18812 (or set `TESTBED_FRAME_ORIGIN`):

```sh
TESTBED_STATE=/tmp/s php -S 127.0.0.1:18812 -t testbed/php
```

WordPress reads `TESTBED_STATE` too (default `wp-content/testbed-state`), for a WordPress you run yourself with the testbed's plugins.

## Make targets

| Target | Does |
|---|---|
| `make up` | start the stack, seed WordPress, wait until both sites answer |
| `make down` | stop and delete everything (containers, database, WordPress volume) |
| `make reset` | `down` then `up` |
| `make good` | every scenario good (`make good ID=s05` for one) |
| `make broken ID=s05` | break one scenario (without `ID`, every scenario) |
| `make status` | list broken scenarios |
| `make logs` | follow the web server logs |
| `make wp ARGS="post list --post_type=page"` | run WP-CLI against the site |
| `make wp-update-reset` | put the ten testbed-update plugins back at 1.0 (s47, s48) |
| `make wp-updates` | list the plugin updates WordPress offers |
| `make bench-eaa` | EAA mode's benchmark (`-only eaa`) from the module root; `make build` first, `ARGS="-tools axe,pa11y"` adds flags |

## Switching a scenario

```sh
testbed/toggle.sh s05 broken   # or: 5 broken, 05 broken
testbed/toggle.sh s05 good
testbed/toggle.sh all good
testbed/toggle.sh status
```

A scenario is broken while `testbed/state/<id>` exists (or `$TESTBED_STATE/<id>`, see above). Both containers mount `state/` and check it on every request. They write only two counters there: `orders.txt` (orders the shop took) and `sidefx.txt` (requests that reached s88's side effects), which `make up` creates writable for the web servers. The mu-plugin `wp/mu-plugins/00-testbed-state.php` provides `testbed_broken('s05')`; the theme, plugins and mu-plugins switch on it. The testbed's page cache keys on the set of broken scenarios, so it never serves a page that was rendered in the other state.

## Scenarios

[`scenarios.yaml`](scenarios.yaml) is the manifest. For each scenario it gives the page, the module that should catch it, the expected change (class, check, title) and where the broken state is implemented.

| ID | Page | Broken state | Expected |
|---|---|---|---|
| s01 | `/s01-fatal/` | a plugin's shortcode calls a function it no longer defines, so the page shows WordPress's "critical error" screen with a 500 | BROKE health |
| s02 | `/s02-warning/` | the theme footer prints `Warning: Undefined array key "note" in …/footer.php on line N` on every page | NEW health |
| s03 | `:18810/report.php` | plain PHP: `Deprecated: strlen(): Passing null…`, as after a PHP 8.1+ upgrade | NEW health |
| s04 | `/s04-blank/` | the page answers 200 with an empty body (white screen) | BROKE health |
| s05 | `/s05-js-error/` | a plugin script throws on load (`TypeError`) | NEW health (JS error) |
| s06 | `/s06-jquery/` | jQuery is deregistered while the theme's inline code still calls `jQuery()` | NEW health (JS error) |
| s07 | `/s07-ajax/` | the widget's admin-ajax call answers 500 | BROKE health (failed request) |
| s08 | `/s08-rest/` | the widget's REST route was renamed, so its request 404s | NEW health (failed request) |
| s09 | `/s09-links/` | the linked page `/s09-links/spring-offer/` is gone (404) | BROKE health |
| s10 | `/s10-image/` | the content points at an image size that doesn't exist | NEW health (broken image) |
| s11 | `/s11-form/` | the contact form's handler fails: "There was an error trying to send your message." | BROKE interact (submit level) |
| s12 | `/s12-menu/` | the header's Browse button is dead (its script binds to a class the header doesn't render) | interact: dead click |
| s13 | `/s13-button/` | "Add to wishlist" throws `ReferenceError: WishlistStore is not defined` on click | interact: JS error on click |
| s14 | `/s14-layout/` | the hero disappears and the header flips | CHANGED visual |
| s15 | `/s15-mobile/` | a 1200 px fixed-width promo strip, so the page no longer fits a phone | CHANGED visual / NEW health (not responsive) |
| s16 | `/s16-noindex/` | "Discourage search engines" switched on, so pages get noindex | NEW health |
| s17 | `/s17-title/` | the theme loses `title-tag` support, so pages have no `<title>` | NEW health (no title) |
| s18 | `/cart/` | the page cache caches the cart (`X-Cache: HIT`, public) | cache: private page cached |
| s19 | `/s19-account/` | the page cache stores pages rendered for logged-in users and serves them, with the admin bar and the user's name, to visitors | cache: leak (needs login) |
| s20 | `/s20-heavy/` | a "slider" plugin adds a 2 MB image and a render-blocking script that takes 1.5 s | SLOWER speed |
| s21 | `/s21-maintenance/` | maintenance mode left on: every request gets WordPress's 503 "Briefly unavailable" screen | BROKE health |
| s22 | `/s22-database/` | "Error establishing a database connection" on every page | BROKE health |
| s23 | `/s23-fatal/` | every front-end page runs out of memory, so WordPress shows its critical error screen site-wide | BROKE health |
| s24 | `/s24-styles/` | theme 2.0 enqueues a stylesheet that wasn't deployed, so the site renders unstyled | NEW health (broken stylesheet) / CHANGED visual |
| s25 | `:18811/mixed.php` | over https, the logo still loads from the old http:// URL | NEW health (mixed content) |
| s26 | `/s26-robots/` | robots.txt says `Disallow: /` | BROKE seo |
| s27 | `/s27-seo/` | canonical, hreflang, Open Graph and JSON-LD are gone | seo |
| s28 | `/s28-sitemap/` | `/wp-sitemap.xml` answers 500 | seo |
| s29 | `/s29-redirect/` | a new redirect rule sends the page to the home page | CHANGED health (redirect) |
| s30 | `/s30-stale/` | the page cache serves a 36-hour-old copy with old content and an expired nonce | NEW cache (stale) / BROKE cache (expired nonce) |
| s31 | `/checkout/` | Place order hits a fatal error in the payment step | BROKE interact (submit level) |
| s32 | `/s32-captcha/` | the form gains a reCAPTCHA that automated submits can't pass | a "has a CAPTCHA" warning, not BROKE |
| s33 | `/s33-mail/` | the form still says sent while no email goes out | BROKE echo (needs Echo) |
| s34 | `:18810/ttfb.php` | the server waits 1.5 s before answering; nothing else changes | SLOWER speed (TTFB) |
| s35 | `:18810/late.php` | the section a script loads 1.5 s after load shows a missing photo | NEW health (broken image) |
| s36 | `:18810/about.php` | the whole site answers with a Cloudflare-style bot challenge (403) | BROKE health/blocked |
| s37 | `:18810/about.php` | the whole site asks for basic auth (401) | BROKE health/auth-required |
| s38 | `:18810/archive.php` | each page takes 3 s; the benchmark presses Ctrl+C during compare | marked interrupted, no false BROKE |
| s39 | `:18810/` | nothing; the benchmark runs two compares in one folder at once | both clean, separate folders |
| s40 | `:18810/banner.php` | a red banner appears; the benchmark deletes the snapshot's screenshots | a "screenshots missing" warning |
| s41 | `:18810/slow.php` | the page answers after 5 s with a 6 s timeout | SLOWER speed, no white screen |
| s42 | `:18810/flaky.php` | one request in five answers 500 | UNSTABLE |
| s43 | `:18810/moved.php` | the page redirects to another host name | exit 3 |
| s44 | `:18810/` | nothing; the benchmark keeps every CPU busy during compare | nothing at or above NEW |
| s45 | `:18810/catalog.php` | page 317 of 320 prints a PHP warning | NEW health |
| s46 | `/s46-widgets/` | an Elementor add-on is gone: "Widget ... not found" (caught by a YAML rule) | BROKE health (the rule's level is error) |
| s47 | `/contact/` | ten plugin updates, one of which (testbed-update-07 1.1) breaks the contact page; `wp-update --bisect --rollback` | culprit testbed-update-07 in at most 4 compares |
| s48 | `/contact/` | the same ten updates; `wp-update --one-by-one --rollback` | culprit testbed-update-07 in at most 10 compares |
| s49 | `/checkout/` | the coupon field's script cancels Tab and Shift+Tab | BROKE keyboard/trap and exit 1 with `--fail-on broke`; not the newsletter dialog |
| s50 | `/checkout/` | name, email and address lose their `<label for>` | NEW a11y/label on 3 fields; nothing BROKE |
| s51 | `/s51-newsletter/` | closing the dialog (Escape or its button) removes the focused control, focus falls to the page | NEW keyboard/focus-lost; no trap |
| s52 | `/s52-modal/` | an `aria-modal` dialog stops containing focus | NEW keyboard/dialog-focus |
| s53 | `:18810/a11y/dialog-nofocus.php` | a dialog opens with focus left behind it | NEW keyboard/dialog-focus |
| s54 | `/s54-focus/` | the theme adds `*:focus{outline:none}` on every page | NEW keyboard/no-focus-ring |
| s55 | `/s55-skip/` | `#main` renamed on every page, the skip link points nowhere | NEW keyboard/skip-link-broken |
| s56 | `/s56-toggle/` | the header's Browse toggle becomes a `div` with a click listener | NEW keyboard/not-reachable |
| s57 | `:18810/a11y/wishlist.php` | a `div role=button tabindex=0` loses its key handler | NEW keyboard/not-operable |
| s58 | `/s58-submenu/` | the Guides submenu opens on `:hover` only | NEW keyboard/menu |
| s59 | `/checkout/` | `autocomplete="nope"` on the billing fields | NEW a11y/autocomplete-valid (a WCAG 2.1 rule) |
| s60 | `/checkout/` | labels replaced by placeholders | NEW a11y/placeholder-label; no a11y/label |
| s61 | `/checkout/` | Place order turns `#c8c8c8` on white | NEW a11y/color-contrast |
| s62 | `/s62-lang/` | `<html lang>` dropped on every page | NEW a11y/html-has-lang |
| s63 | `/s63-gallery/` | the gallery images lose their alt | NEW a11y/image-alt |
| s64 | `/contact/` | key flow `flows/keyboard-contact.yaml` (`--level flows`): errors by colour only | NEW keyboard/error-not-linked |
| s65 | `:18810/a11y/consent.php` | the cookie banner grabs focus and keeps it | BROKE keyboard/trap |
| s66 | `:18810/a11y/zoom.php` | `user-scalable=no, maximum-scale=1` | NEW a11y/meta-viewport |
| s67 | `:18810/a11y/widget.php` | a web component's shadow root drops its label | NEW a11y/label with ` >>> ` in the source |
| s68 | `/my-account/` (login) | the phone field loses its label; the form shows runtime canaries; key flow `flows/keyboard-account.yaml` (`login: true`) | NEW a11y/label; no canary in any file (harness canaries); no host outside the testbed while logged in (harness requests). Soundcheck visits pages anonymously, so the label waits for --eaa to audit logged-in pages |
| s69 | `:18810/a11y/promo.php` | a fixed 600 px promo bar | NEW a11y/reflow |
| s70 | `/checkout/` | key flow `flows/keyboard-checkout.yaml` (`--level flows`): Place order only by mouse | BROKE keyboard/flow; no order placed (harness orders) |
| s71 | `:18810/a11y/autonav.php` | a select loads another view when it gets focus | NEW keyboard/context-change |
| s72 | `:18810/a11y/offcanvas.php` | the closed off-canvas menu's links stay tabbable | NEW keyboard/focus-invisible |
| s73 | `/s73-overlay/` | an overlay plugin's script tag (a local stub) on every page | NEW a11y/overlay |
| s74 | `:18810/a11y/big.php` | the page grows to 20,000 elements and a runaway script keeps it busy | exit 3 and a "not checked" warning; nothing FIXED |
| s75 | `:18810/a11y/noescape.php` | a dialog ignores Escape | NEW keyboard/escape; no trap |
| s76 | `:18810/a11y/reload-trap.php` | a focus handler reloads the page | keyboard/trap or the capped warning, compare within 90 s (the 60 s walk cap plus 10% plus startup) |
| s77 | `:18810/a11y/dialog.php` | hard good: a native `<dialog>` with `showModal()` | nothing at or above NEW |
| s78 | `:18810/a11y/modal.php` | hard good: a custom modal done right | nothing at or above NEW |
| s79 | `:18810/a11y/rings.php` | hard good: focus shown by box-shadow, underline and background | nothing at or above NEW |
| s80 | `/s80-skip/` | hard good: a visually hidden skip link that shows on focus | nothing at or above NEW |
| s81 | `:18810/a11y/live.php` | hard good: a carousel, random order, the time, a caret | nothing at or above NEW |
| s82 | `:18810/a11y/backlog.php` | hard good: 300 old contrast problems, nothing changes | nothing NEW or FIXED |
| s83 | `:18810/a11y/backlog.php` | one of the first 20 of them fixed | FIXED a11y/color-contrast; nothing NEW |
| s84 | `:18810/a11y/embed.php` | hard good: same-origin and cross-origin frames | nothing NEW; the statement lists the frames; no canary in any file |
| s85 | `/checkout/` | hard good: nothing changes, every CPU busy | nothing NEW, no trap; no order placed |
| s86 | `/checkout/` | the checkout answers 500 | BROKE health; the statement doesn't claim it was audited |
| s87 | `:18810/a11y/legacy.php` | a 1.2.2 snapshot (`fixtures/s87-snapshot`) compared by 1.3 with `--eaa` | the "used the WCAG 2.0 rules" note and nothing at or above NEW |
| s88 | `:18810/a11y/sidefx.php` | hard good: side effects on GET and on focus | nothing NEW; no counted request, no download |
| s89 | `:18810/a11y/` | a fully accessible small site, also in every control round | nothing from a11y or keyboard; a statement with no automated failures |

Site-wide breakage (s02, s06, s12, s14, s16, s17, s20 to s24, s27, s36, s37, s54 to s56, s58, s62, s73, s80) shows on every page. The benchmark limits each run to the scenario's own pages (`include`) and breaks one scenario at a time.

**Pending** scenarios expect a detector or fix that isn't built yet: today the EAA scenarios, each marked with the Soundcheck 1.3 build lane it waits for (`pending: keyboard`, `pending: axe`…). They run like the others; the scoreboard lists them apart. Their mark or `note` in `scenarios.yaml` says what they wait for; drop the mark once the scenario is caught.

### Accessibility (EAA mode, s49 to s89)

s49 to s89 are the scenarios for `--eaa` (accessibility evidence for the European Accessibility Act): axe-core with the WCAG 2.1 rules, the keyboard walk, key flows and the statement draft. They run with `args: [--eaa]`; `-only eaa` picks them all.

**Step zero.** "A clean site must pass" means nothing until the clean states are clean, so both sites were audited before any scenario was added. `TestGoodStateAudit` does it with the testbed's own audit (`bench/audit.go`): axe-core 4.14.0 with the WCAG 2.1 A and AA tags, a placeholder-only label check, reflow at 320 px and a Tab walk that wants a skip link to `#main` first, no trap, no hidden stop and a visible focus ring on each of the first 40 stops. It is small and separate from Soundcheck's modules on purpose; `TestGoodStateIsClean` adds the `--eaa` pass with the modules themselves once they're merged.

| Site | Before | After |
|---|---|---|
| WordPress | 34 findings on the 17 pages a run discovers: the Browse button's contrast (white on `#c96b2c`) and no skip link, on every page | 0 on every discovered page and every scenario page: 54 pages |
| Plain PHP | 18 findings on 17 pages: no skip link on every page, the invoice logo wider than a 320 px screen | 0 on every discovered page and every scenario page: 36 pages |

The fixes: a skip link to `#main` before the header and `id="main"` on `<main>` on both sites, a visible `:focus-visible` ring, the Browse button on `#a24a17`, a `<nav>` landmark on the plain-PHP site and images no wider than the screen. No scenario's broken behaviour changed. The pages that keep findings on purpose (`backlog: true`: s82 and s83's 300 grey rows, s87's 2.1 failure) are left out. `TestBrokenStatesAudit` is the other half: the same audit finds each broken state it can see (s49, s50, s54, s55, s59 to s63, s65 to s67, s69, s72, s87).

**Pages.** The plain-PHP ones live in `php/a11y/` and run under `php -S` too. Each uses the shared `layout.php` frame, finds elements with `document.getElementById` (never id globals: an id such as `open` names `window.open`) and tests its own behaviour once on load. `<body data-ok="1">` says it did what its state says, `"0"` with `data-why` says it didn't. The WordPress ones are in the theme (`wp/themes/testbed/inc/eaa.php`, `header.php`, `js/dialog.js`), the shop (`testbed-shop`: the checkout and `[testbed_account]` on `/my-account/`) and the contact form (`testbed-forms`, `validate="1"`). `TestPreflight` (`bench/preflight.go`) switches each scenario good and broken and checks the page: the self-test of every `a11y/` page and one reading per WordPress scenario.

**Side effects.** The shop counts orders in `state/orders.txt`; `a11y/sidefx.php` and `a11y/ws.php` count every request that reaches an add-to-cart link, the logout link (`/a11y/sidefx.php/logout`), the download, the popup or the WebSocket in `state/sidefx.txt`. The orders harness fails a scenario that changed either. s88's include pattern is the page's exact path, so page discovery never opens the side-effect links (Soundcheck's discovery ignores `rel="nofollow"`); only the keyboard walk could reach them.

**Canaries.** s68's account form and s84's cross-origin payment frame show the values in `state/canaries` (`email=…`, `phone=…`, `token=…`, one per line) in field values, `data-*` and `aria-label` attributes and a link's query string. The benchmark makes them at run time; none is ever committed. A canary in any file Soundcheck wrote is a leak.

**Frames and overlays.** s84's same-origin frame is `a11y/frame.php/map` and its cross-origin one is `a11y/frame.php/pay` from the frame port: another host name (127.0.0.1 for localhost) on another port. s73's overlay script is a stub at the overlay plugin's real path (`wp/plugins/pojo-accessibility/assets/js/app.js`), so nothing is fetched from another host.

**Key flows and the old snapshot.** s64 and s70 copy `flows/keyboard-contact.yaml` and `flows/keyboard-checkout.yaml` (Soundcheck's key flow templates with the testbed's selectors) into the run's `.soundcheck/flows/`; s68 copies `flows/keyboard-account.yaml`, which logs in, so the canaries reach the walk's trace. s87 compares against `fixtures/s87-snapshot`, a snapshot Rhythm Soundcheck 1.2.2 took of `a11y/legacy.php` (no `options.wcag`, no `options.a11y`), copied in as the snapshot `good` with its address moved to wherever the site runs.

### Mail through Echo (s33)

s33 checks that a form which says "sent" really sends its email, with Soundcheck's `--echo`. WordPress sends mail to `TESTBED_SMTP` (host:port) when it's set. Run Echo (`rhythm echo up`, SMTP on :1025) with its SMTP port reachable from Docker containers. Start the testbed with `TESTBED_SMTP=host.docker.internal:1025 make -C testbed up` and pass `-echo http://127.0.0.1:8025` to the benchmark. Without `-echo`, s33 shows "not run" and is left out of the score: the benchmark doesn't start Echo itself, since Echo is another tool and WordPress must be started pointing at its SMTP port.

### Plugin updates (s47, s48)

s47 and s48 are the target for `rhythm-soundcheck wp-update`, which updates plugins and finds the one that broke the site. They don't use a state file. The site has ten small plugins, `testbed-update-01` to `testbed-update-10`, installed at 1.0, each with a 1.1 update. Nine of the updates change nothing anyone can see. `testbed-update-07` 1.1 calls a function the release left out on the contact page, so `/contact/` shows WordPress's critical error screen with a 500 and every other page still works.

- `wp/updates/` holds the plugins' sources. `reset.sh` builds a 1.0 and a 1.1 zip of each in `wp-content/testbed-updates/` and installs every plugin at 1.0. `make up` runs it once and `make wp-update-reset` runs it again.
- `wp/mu-plugins/testbed-updates.php` is the update server. It adds each plugin's newest zip to the `update_plugins` transient (the `pre_set_site_transient_update_plugins` and `site_transient_update_plugins` filters) and answers `plugins_api` and the downloads, so `wp plugin update` and the rollback `wp plugin install <slug> --version=1.0 --force` work the way they do with WordPress.org. Nothing leaves the containers: requests to WordPress.org get an empty update check or an error.
- `wp/mu-plugins/testbed-local.php` keeps logged-in pages local: it turns off avatars and the front-end admin bar, which would otherwise load Gravatar images from `secure.gravatar.com`. The seed hides the admin bar for the `soundcheck` user as well.
- The page cache keys on the installed versions of these plugins too, so an update is seen at once.

The WordPress image has no WP-CLI, so wp-update drives the `cli` container:

```sh
make -C testbed wp-updates       # ten updates, 1.0 → 1.1
rhythm-soundcheck wp-update http://localhost:18800/contact/ --bisect --rollback \
  --wp 'docker compose -f testbed/docker-compose.yml run --rm --no-deps -T --entrypoint wp cli'
make -C testbed wp-update-reset  # back to 1.0
```

The benchmark does the same with each scenario's `args`. It resets the plugins before and after, reads the culprit and the number of compares from wp-update's output and scores it against the scenario's `culprit` and `compares`. It shows "not run" while the binary has no `wp-update` command. The bisect count starts at the compare that saw the break; one by one counts every compare up to the culprit's.

### Page cache

`wp/mu-plugins/testbed-cache.php` stands in for a page cache such as WP Super Cache or a CDN. It caches anonymous GET requests for pages for ten minutes and sends `X-Cache: MISS|HIT|BYPASS`, `Age` and `Cache-Control: public, max-age=0, s-maxage=600`. In the good state `/cart/`, `/checkout/` and `/my-account/` bypass the cache with `no-store, private`. Logged-in visitors (with a `wordpress_logged_in_*` cookie) neither read nor fill it. s18 and s19 break each of those rules.

To see the s19 leak by hand:

```sh
testbed/toggle.sh s19 broken
curl -s -c jar -b wordpress_test_cookie=WP%20Cookie%20check \
  -d 'log=soundcheck&pwd=soundcheck-testbed&testcookie=1' http://localhost:18800/wp-login.php -o /dev/null
curl -s -b jar http://localhost:18800/s19-account/ >/dev/null         # logged in: fills the cache
curl -s http://localhost:18800/s19-account/ | grep -o 'Howdy, [^<]*<[^>]*>[^<]*'   # anonymous: "Howdy, Sam Soundcheck"
```

## Benchmark

```sh
make build                                  # bin/rhythm-soundcheck
make -C testbed up
go run -tags testbed ./testbed/bench        # writes docs/benchmark.md
make -C testbed down
```

For each scenario, the harness in [`bench/`](bench/) does the following:

1. sets the good state
2. runs `rhythm-soundcheck snapshot <url> --name good --json -o good.json` in `testbed/bench/work/<id>/`, limited to the scenario's pages by a `.soundcheck/config.yaml` it writes (`--config`), with the scenario's `args`, `--level` and `--modules` when it has them
3. breaks the scenario
4. runs `rhythm-soundcheck compare --against-snapshot good --json -o compare.json`, the way the scenario's `harness` says: Ctrl+C after `interrupt_after` (`interrupt`), two compares at once (`parallel`), after deleting the snapshot's screenshots (`delete-shots`) or with every CPU busy (`stress`)
5. passes when the result matches one of the scenario's `expect` entries (a change, a warning, the interrupted marker, nothing at or above NEW, or an exit code), or every one of them with `all: true`. It must also have none of its `forbid` changes

A scenario with `fixture` copies that snapshot folder in instead of step 2, `flows` are copied into `.soundcheck/flows/` first, `same: true` skips step 3 and `within` fails a compare that takes longer.

6. runs the EAA harnesses the scenario lists, after the compare. Any problem makes the scenario missed, even when its expectation held:
   - `statement`: the config the bench writes gets a `statement:` block (owner "Testbed Coffee Roasters", the site's address, a contact at example.test); the bench runs `rhythm-soundcheck statement -o statement.md` and checks the draft says the evidence note, `not tested: needs a person` and the scenario's `says`. It must never say `complian`, `conform`, `certif`, `passes WCAG`, `fully accessible` or `guarantee` outside the fixed disclaimer (the evidence note and PRD 15's sentences) and the "fully / partially / not compliant" choice inside a `[TO DO]` or hold a canary
   - `requests`: reads every run under the run's `.soundcheck/` and fails on a page URL, final URL, redirect, resource or failed request whose host isn't `localhost`, `127.0.0.1`, `::1` or a service of `docker-compose.yml`
   - `orders`: `state/orders.txt` and `state/sidefx.txt` read the same before the good snapshot and after the compare
   - `canaries`: before the good state the bench writes `state/canaries` (`email`, `phone`, `token`, `data`, `field`, `aria`, `query`, `text`), made from parts and random bytes at run time. It removes the file afterwards and searches every file under the run's `.soundcheck/` and every file it wrote next to it (`good.json`, `compare.json`, `statement.md`) for the random part of each and names the canary and the file, never the value

Then the **control pass** checks for noise: with every scenario good, it takes a snapshot of each `control` site (at most `control.limit` pages) and compares twice, for `-control` rounds (10 by default). Any change at or above NEW is a false alarm; the scoreboard shows the count next to the catches. `-control 0` skips it; a run with `-only` skips it unless `-control` is given or the list has `eaa`.

**EAA mode** (`make -C testbed bench-eaa`, the same as `go run -tags testbed ./testbed/bench -only eaa`) runs s49 to s89, then the control pass with `--eaa` on both sites plus the pages of each scenario marked `control: true` (s89, with its flags), then a cost pass: on each control site a default snapshot, one with `--modules health`, one with `--modules health,a11y` and one with `--eaa`. On the checkout it times s70's key flow with and without `--level flows`. It writes only the `EAA mode` section of `docs/benchmark.md` (between `<!-- eaa:begin -->` and `<!-- eaa:end -->`) and keeps the rest; a full run writes the whole file with the section at its end. The section has the scoreboard of s49 to s89 (caught, missed or pending with the lane), the false alarms of the good scenarios and of the `--eaa` control rounds, determinism (the issue keys of each round's snapshot against the first round's), the cost table with PRD 16.3's targets beside it (median and p95 seconds per page with and without `--eaa`, the a11y audit and the keyboard walk per page, one key flow, the whole run against a default one, with core count, OS, Chrome, axe-core and its tags), the coverage table made from the WCAG catalog (`wcag.Criteria`), the other accessibility tools and the framing of PRD 15 item 10 with the evidence note.

Scenarios with `login: true` get `SOUNDCHECK_USER=soundcheck` and `SOUNDCHECK_PASSWORD=soundcheck-testbed` unless you set them yourself.

BackstopJS, lychee and Unlighthouse run on the same states when they're available; on the EAA scenarios the axe-core CLI, pa11y and Unlighthouse do instead (caught means a violation in the broken state that the good state lacks; Unlighthouse a drop in a category score):

- `-tools auto` (the default) uses the tools that are already installed and lychee's Docker image if it has been pulled.
- `-tools all` (or e.g. `-tools lychee,unlighthouse` or `-tools axe,pa11y`) fetches them through npx and Docker.
- `-tools none` skips them.

Tools that don't run show "not run" in the scoreboard, which says how to include them.

Flags:

| Flag | Default | |
|---|---|---|
| `-bin` | `bin/rhythm-soundcheck` | the binary to benchmark |
| `-only` | all | `s01,s05`: some scenarios; `eaa` for s49 to s89 (with the `--eaa` control pass and the cost pass) |
| `-tools` | `auto` | `auto`, `none`, `all`, or a list |
| `-out` | `docs/benchmark.md` | the scoreboard |
| `-work` | `testbed/bench/work` | runs and `results.json` |
| `-wp`, `-php`, `-php-https` | from `scenarios.yaml` | site URLs |
| `-control` | `10` | control pass rounds; `0` skips it (and so does `-only` without `eaa`, unless `-control` is set) |
| `-echo` | none | Echo's URL, for the scenarios that need it (s33) |
| `-compose` | `docker compose -f testbed/docker-compose.yml` | the compose command for the stack's WP-CLI (s47, s48), split at spaces; set `COMPOSE_PROJECT_NAME` for a stack started under another name |
| `-timeout` | `5m` | per command |

The bench package has the `testbed` build tag, so a plain `go test ./...` never needs Docker. With the testbed up, its tests check the sites themselves with the packages directly (health module, runner and compare):

```sh
go test -tags testbed -v ./testbed/bench/
```

- `TestHealthScenarios`: snapshot, break and compare for the health scenarios (s01–s10, s16, s17, plus s15 through health's "not responsive" check). It prints the score.
- `TestGoodStateIsClean`: with every scenario good, both sites have nothing above a notice. Its `--eaa` pass (health, a11y and keyboard) must find no a11y or keyboard issue at any level and no audit that didn't finish.
- `TestGoodStateAudit`, `TestBrokenStatesAudit`: step zero, the testbed's own accessibility audit on every good page and on the broken states it can see. `TestAuditPage` audits the one page `TESTBED_AUDIT_URL` names.
- `TestPreflight`: every accessibility scenario's page in both states (the `a11y/` self-tests and the WordPress readings).
- `TestClickScenarios`: drives s12 and s13 in Chrome to prove the dead button and the click error.
- `TestManifest`, `TestLoadManifestRejects`, `TestEvaluate`, `TestExpectString`, `TestScoreboardPendingAndControl`: the manifest, its validation and the scoring, without the testbed.
- `TestWPUpdateTestbed`: through the stack's WP-CLI, checks that all ten plugin updates are offered after a reset, that testbed-update-07 1.1 breaks `/contact/` and nothing else and that the rollback fixes it. It needs Docker and the stack, not Chrome. Set `TESTBED_WP_PORT` (and `COMPOSE_PROJECT_NAME`) for a stack started with them.
- `TestParseWPUpdate`, `TestEvaluateWPUpdate`: reading and scoring wp-update's output, without the testbed.
- `TestCheckStatement`, `TestNewCanaries`, `TestStartHarnesses`, `TestOutsideHosts`, `TestComposeServices`, `TestSweepCanaries`, `TestWithHarnesses`: the EAA harnesses on fake run folders, without the testbed.
- `TestEAASectionGolden` (`-update` rewrites `bench/testdata/eaa-section.golden.md`), `TestScoreboardKeepsEAAApart`, `TestWriteBoard`, `TestEAAScore`, `TestPercentile`, `TestSymmetricDiff`, `TestIssueKeys`, `TestA11yToolOutput`, `TestToolRuns`, `TestCostSectionWithoutCost`: the EAA section and its numbers from a fixed result, without the testbed.
- `TestStateFromEnv`: serves the plain-PHP site with `php -S` and a `TESTBED_STATE` folder and toggles s03 on and off. It needs only `php` (skipped without it), not Docker or Chrome.

## Adding a scenario

1. Add a page in `wp/seed.sh`, or a file in `php/`.
2. Implement the broken state behind `testbed_broken( 'sNN' )`, next to the code it breaks (theme, plugin or `wp/mu-plugins/testbed-scenarios.php`). `TestManifest` checks that every scenario that changes the site has one.
3. Add it to `scenarios.yaml` with a precise `expect` (and `forbid` for the false alarm it must not cause). Mark it `pending: <lane>` (or `pending: true` with a `note`) when its detector isn't merged yet.
4. Check both states with curl or the tests and make sure the good state stays clean (`TestGoodStateIsClean`; `TestGoodStateAudit` too for an accessibility page). An accessibility page tests itself (`testbed_selftest()` in `php/layout.php`) or gets a probe in `bench/preflight.go`.
