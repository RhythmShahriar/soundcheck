# Soundcheck testbed

Twenty broken sites in one Docker Compose stack, for Soundcheck's end-to-end tests and its public benchmark ([docs/benchmark.md](../docs/benchmark.md)).

Each scenario breaks one thing the way a plugin update, a theme update, a PHP upgrade or a setting would. Every scenario has a **good** and a **broken** state. `toggle.sh` switches between them right away, without a rebuild or restart.

## Start

```sh
make -C testbed up      # about 30 s once the images are pulled
```

| Site | URL | What it is |
|---|---|---|
| WordPress | http://localhost:18800/ | WordPress 6.8.3 on PHP 8.3 with MariaDB 11.4: a coffee roaster's site with pages, posts, a menu, a contact form, a shop and a cart. Each WordPress scenario has its own page (`/s01-fatal/`, `/s02-warning/`…), and `/scenarios/` lists them. |
| Plain PHP | http://localhost:18810/ | "Ledger", three pages of plain PHP 8.3 (no framework), for scenario s03 |

Every `make up` installs and seeds a fresh site. The database lives in tmpfs and `make down` deletes the WordPress volume, so every run starts from the same state. Ports are bound to 127.0.0.1. Set `TESTBED_WP_PORT` / `TESTBED_PHP_PORT` to move them. If you do, pass `-wp`/`-php` to the benchmark too.

Accounts (local test accounts only):

| User | Password | Role | Used for |
|---|---|---|---|
| `admin` | `admin` | administrator | looking around in wp-admin |
| `soundcheck` | `soundcheck-testbed` | editor, display name "Sam Soundcheck" | Soundcheck's logged-in checks: `SOUNDCHECK_USER=soundcheck SOUNDCHECK_PASSWORD=soundcheck-testbed` |

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

## Switching a scenario

```sh
testbed/toggle.sh s05 broken   # or: 5 broken, 05 broken
testbed/toggle.sh s05 good
testbed/toggle.sh all good
testbed/toggle.sh status
```

A scenario is broken while `testbed/state/<id>` exists. Both containers mount `state/` read-only and check it on every request. The mu-plugin `wp/mu-plugins/00-testbed-state.php` provides `testbed_broken('s05')`, and the theme, plugins and mu-plugins switch on it. The testbed's page cache keys on the set of broken scenarios, so it never serves a page that was rendered in the other state.

## Scenarios

[`scenarios.yaml`](scenarios.yaml) is the manifest. For each scenario it gives the page, the module that should catch it, the expected change (class, check, title), and where the broken state is implemented.

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

Site-wide breakage (s02, s06, s12, s14, s16, s17, s20) shows on every page. The benchmark limits each run to the scenario's own pages (`include`) and breaks one scenario at a time.

### Page cache

`wp/mu-plugins/testbed-cache.php` stands in for a page cache such as WP Super Cache or a CDN. It caches anonymous GET requests for pages for ten minutes and sends `X-Cache: MISS|HIT|BYPASS`, `Age` and `Cache-Control: public, max-age=0, s-maxage=600`. In the good state, `/cart/`, `/checkout/` and `/my-account/` bypass the cache with `no-store, private`, and logged-in visitors (with a `wordpress_logged_in_*` cookie) neither read nor fill it. s18 and s19 break each of those rules.

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
2. runs `rhythm-soundcheck snapshot <url> --name good --json -o good.json` in `testbed/bench/work/<id>/`, limited to the scenario's pages by a `.soundcheck/config.yaml` it writes (`--config`), with `--level` when the scenario needs it
3. breaks the scenario
4. runs `rhythm-soundcheck compare --against-snapshot good --json -o compare.json`
5. passes when the comparison has a change matching one of the scenario's `expect` entries

Scenarios with `login: true` get `SOUNDCHECK_USER=soundcheck` and `SOUNDCHECK_PASSWORD=soundcheck-testbed` unless you set them yourself.

BackstopJS, lychee and Unlighthouse run on the same states when they're available:

- `-tools auto` (the default) uses the tools that are already installed, and lychee's Docker image if it has been pulled.
- `-tools all` (or e.g. `-tools lychee,unlighthouse`) fetches them through npx and Docker.
- `-tools none` skips them.

Tools that don't run show "not run" in the scoreboard, and the scoreboard says how to include them.

Flags:

| Flag | Default | |
|---|---|---|
| `-bin` | `bin/rhythm-soundcheck` | the binary to benchmark |
| `-only` | all | `s01,s05`: some scenarios |
| `-tools` | `auto` | `auto`, `none`, `all`, or a list |
| `-out` | `docs/benchmark.md` | the scoreboard |
| `-work` | `testbed/bench/work` | runs and `results.json` |
| `-wp`, `-php` | from `scenarios.yaml` | site URLs |
| `-timeout` | `5m` | per command |

The bench package has the `testbed` build tag, so a plain `go test ./...` never needs Docker. With the testbed up, its tests check the sites themselves with the packages directly (health module, runner and compare):

```sh
go test -tags testbed -v ./testbed/bench/
```

- `TestHealthScenarios`: snapshot, break and compare for the health scenarios (s01–s10, s16, s17, plus s15 through health's "not responsive" check). It prints the score.
- `TestGoodStateIsClean`: with every scenario good, both sites have nothing above a notice.
- `TestClickScenarios`: drives s12 and s13 in Chrome to prove the dead button and the click error.

## Adding a scenario

1. Add a page in `wp/seed.sh`, or a file in `php/`.
2. Implement the broken state behind `testbed_broken('sNN')`, next to the code it breaks (theme, plugin or `wp/mu-plugins/testbed-scenarios.php`).
3. Add it to `scenarios.yaml` with a precise `expect`.
4. Check both states with curl or the tests, and make sure the good state stays clean (`TestGoodStateIsClean`).
