# Soundcheck testbed

48 broken-site scenarios in one Docker Compose stack, for Soundcheck's end-to-end tests and its public benchmark ([docs/benchmark.md](../docs/benchmark.md)).

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

Every `make up` installs and seeds a fresh site. The database lives in tmpfs and `make down` deletes the WordPress volume, so every run starts from the same state. Ports are bound to 127.0.0.1. Set `TESTBED_WP_PORT` / `TESTBED_PHP_PORT` / `TESTBED_PHP_HTTPS_PORT` to move them. If you do, pass `-wp`/`-php`/`-php-https` to the benchmark too.

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

If your `php.ini` hides errors (`display_errors = Off`), add `-d display_errors=1` so s03's notice shows. WordPress reads `TESTBED_STATE` too (default `wp-content/testbed-state`), for a WordPress you run yourself with the testbed's plugins.

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

## Switching a scenario

```sh
testbed/toggle.sh s05 broken   # or: 5 broken, 05 broken
testbed/toggle.sh s05 good
testbed/toggle.sh all good
testbed/toggle.sh status
```

A scenario is broken while `testbed/state/<id>` exists (or `$TESTBED_STATE/<id>`, see above). Both containers mount `state/` read-only and check it on every request. The mu-plugin `wp/mu-plugins/00-testbed-state.php` provides `testbed_broken('s05')`; the theme, plugins and mu-plugins switch on it. The testbed's page cache keys on the set of broken scenarios, so it never serves a page that was rendered in the other state.

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
| s17 | `/s17-title/` | the theme loses `title-tag` support, so pages have no `<title>` | CHANGED health |
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
| s30 | `/s30-stale/` | the page cache serves a 36-hour-old copy with old content and an expired nonce | NEW cache (stale, expired nonce) |
| s31 | `/checkout/` | Place order hits a fatal error in the payment step | BROKE interact (submit level) |
| s32 | `/s32-captcha/` | the form gains a reCAPTCHA that automated submits can't pass | a "can't submit" warning, not BROKE (pending) |
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
| s46 | `/s46-widgets/` | an Elementor add-on is gone: "Widget ... not found" (caught by a YAML rule) | NEW health |
| s47 | `/contact/` | ten plugin updates, one of which (testbed-update-07 1.1) breaks the contact page; `wp-update --bisect --rollback` | culprit testbed-update-07 in at most 4 compares |
| s48 | `/contact/` | the same ten updates; `wp-update --one-by-one --rollback` | culprit testbed-update-07 in at most 10 compares |

Site-wide breakage (s02, s06, s12, s14, s16, s17, s20 to s24, s27, s36, s37) shows on every page. The benchmark limits each run to the scenario's own pages (`include`) and breaks one scenario at a time.

**Pending** scenarios expect a detector or fix that isn't built yet (today only s32: Soundcheck has no CAPTCHA detector). They run like the others; the scoreboard lists them apart. Each one's `note` in `scenarios.yaml` says what it waits for; drop `pending: true` once it's caught.

### Mail through Echo (s33)

s33 checks that a form which says "sent" really sends its email, with Soundcheck's `--echo`. WordPress sends mail to `TESTBED_SMTP` (host:port) when it's set. Run Echo (`rhythm echo up`, SMTP on :1025) with its SMTP port reachable from Docker containers. Start the testbed with `TESTBED_SMTP=host.docker.internal:1025 make -C testbed up` and pass `-echo http://127.0.0.1:8025` to the benchmark. Without `-echo`, s33 shows "not run".

### Plugin updates (s47, s48)

s47 and s48 are the target for `rhythm-soundcheck wp-update`, which updates plugins and finds the one that broke the site. They don't use a state file. The site has ten small plugins, `testbed-update-01` to `testbed-update-10`, installed at 1.0, each with a 1.1 update. Nine of the updates change nothing anyone can see. `testbed-update-07` 1.1 calls a function the release left out on the contact page, so `/contact/` shows WordPress's critical error screen with a 500 and every other page still works.

- `wp/updates/` holds the plugins' sources. `reset.sh` builds a 1.0 and a 1.1 zip of each in `wp-content/testbed-updates/` and installs every plugin at 1.0. `make up` runs it once and `make wp-update-reset` runs it again.
- `wp/mu-plugins/testbed-updates.php` is the update server. It adds each plugin's newest zip to the `update_plugins` transient (the `pre_set_site_transient_update_plugins` and `site_transient_update_plugins` filters) and answers `plugins_api` and the downloads, so `wp plugin update` and the rollback `wp plugin install <slug> --version=1.0 --force` work the way they do with WordPress.org. Nothing leaves the containers: requests to WordPress.org get an empty update check or an error.
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
5. passes when the result matches one of the scenario's `expect` entries (a change, a warning, the interrupted marker, nothing at or above NEW, or an exit code) and has none of its `forbid` changes

Then the **control pass** checks for noise: with every scenario good, it takes a snapshot of each `control` site (at most `control.limit` pages) and compares twice, for `-control` rounds (10 by default). Any change at or above NEW is a false alarm; the scoreboard shows the count next to the catches. `-control 0` skips it; a run with `-only` skips it unless `-control` is given.

Scenarios with `login: true` get `SOUNDCHECK_USER=soundcheck` and `SOUNDCHECK_PASSWORD=soundcheck-testbed` unless you set them yourself.

BackstopJS, lychee and Unlighthouse run on the same states when they're available:

- `-tools auto` (the default) uses the tools that are already installed and lychee's Docker image if it has been pulled.
- `-tools all` (or e.g. `-tools lychee,unlighthouse`) fetches them through npx and Docker.
- `-tools none` skips them.

Tools that don't run show "not run" in the scoreboard, which says how to include them.

Flags:

| Flag | Default | |
|---|---|---|
| `-bin` | `bin/rhythm-soundcheck` | the binary to benchmark |
| `-only` | all | `s01,s05`: some scenarios |
| `-tools` | `auto` | `auto`, `none`, `all`, or a list |
| `-out` | `docs/benchmark.md` | the scoreboard |
| `-work` | `testbed/bench/work` | runs and `results.json` |
| `-wp`, `-php`, `-php-https` | from `scenarios.yaml` | site URLs |
| `-control` | `10` | control pass rounds; `0` skips it (and so does `-only`, unless `-control` is set) |
| `-echo` | none | Echo's URL, for the scenarios that need it (s33) |
| `-compose` | `docker compose -f testbed/docker-compose.yml` | the compose command for the stack's WP-CLI (s47, s48), split at spaces; set `COMPOSE_PROJECT_NAME` for a stack started under another name |
| `-timeout` | `5m` | per command |

The bench package has the `testbed` build tag, so a plain `go test ./...` never needs Docker. With the testbed up, its tests check the sites themselves with the packages directly (health module, runner and compare):

```sh
go test -tags testbed -v ./testbed/bench/
```

- `TestHealthScenarios`: snapshot, break and compare for the health scenarios (s01–s10, s16, s17, plus s15 through health's "not responsive" check). It prints the score.
- `TestGoodStateIsClean`: with every scenario good, both sites have nothing above a notice.
- `TestClickScenarios`: drives s12 and s13 in Chrome to prove the dead button and the click error.
- `TestManifest`, `TestLoadManifestRejects`, `TestEvaluate`, `TestExpectString`, `TestScoreboardPendingAndControl`: the manifest, its validation and the scoring, without the testbed.
- `TestWPUpdateTestbed`: through the stack's WP-CLI, checks that all ten plugin updates are offered after a reset, that testbed-update-07 1.1 breaks `/contact/` and nothing else and that the rollback fixes it. It needs Docker and the stack, not Chrome. Set `TESTBED_WP_PORT` (and `COMPOSE_PROJECT_NAME`) for a stack started with them.
- `TestParseWPUpdate`, `TestEvaluateWPUpdate`: reading and scoring wp-update's output, without the testbed.
- `TestStateFromEnv`: serves the plain-PHP site with `php -S` and a `TESTBED_STATE` folder and toggles s03 on and off. It needs only `php` (skipped without it), not Docker or Chrome.

## Adding a scenario

1. Add a page in `wp/seed.sh`, or a file in `php/`.
2. Implement the broken state behind `testbed_broken( 'sNN' )`, next to the code it breaks (theme, plugin or `wp/mu-plugins/testbed-scenarios.php`). `TestManifest` checks that every scenario that changes the site has one.
3. Add it to `scenarios.yaml` with a precise `expect` (and `forbid` for the false alarm it must not cause). Mark it `pending: true` with a `note` when its detector isn't merged yet.
4. Check both states with curl or the tests and make sure the good state stays clean (`TestGoodStateIsClean`).
