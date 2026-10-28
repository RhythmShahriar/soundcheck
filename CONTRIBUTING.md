# Contributing to Soundcheck

Setup, commit messages and the review process are shared by every Rhythm tool and live in the
[shared CONTRIBUTING](https://github.com/RhythmShahriar/.github/blob/master/CONTRIBUTING.md).
This page covers what is particular to Soundcheck: how a module plugs into a run.

```sh
make build   # bin/rhythm-soundcheck; --version says dev (<next release>+<commit>)
make hooks   # Conventional Commits check
make check   # lint + tests (browser tests need Chrome or Chromium)
```

A build from source names the release it is heading for: `nextRelease` in
[cmd/rhythm-soundcheck/version.go](cmd/rhythm-soundcheck/version.go). The pull request that starts
a release's work raises it.

## Modules

A module is a named group of checks in its own folder under `checks/`. It is a type with a
`Name()` and any of the hook interfaces in [`check`](check/check.go). The runner finds out which
hooks a module has by type assertion, so a module implements only the ones it needs.

| Interface | When it runs | Gets |
|---|---|---|
| `Preparer`, `TabPreparer` | on a fresh tab, before it navigates | the tab |
| `Live`, `TabLive` | with the page loaded at the desktop viewport | the tab and the recorded page |
| `PageChecker` | after the mobile pass, without the browser | the recorded page |
| `SiteChecker` | once, after every page | the whole run |
| `Comparer` | when two runs are compared | both runs |
| `ChangeFilter` | after every comparer | the full list of changes |

Optional interfaces tune how the runner treats the module:

- `Describer` lists the module's checks (`Checks() []check.CheckInfo`), so they can be listed,
  linked to docs and skipped one by one.
- `Phaser` puts the module in a phase. Modules run in ascending phase and keep their order within
  one. Use `check.PhaseMutate` when the module changes the page (clicks, submits), so every other
  module's `Live` hook sees the page as it loaded.
- `CaptureNeeds` asks for screenshots (`NeedsShots`) and a second capture in snapshots
  (`NeedsSecondShot`). Without it no screenshots are taken for the module.
- `NeedsText` keeps `Page.Text` after the page's `PageChecker`s ran, for a module that reads it in
  `CheckSite`.
- `Configurable` decodes the module's own block of `config.yaml`, the top-level key named after
  the module (`speed:` for speed). Registering the module lets the file have that key; the CLI
  hands each module its block before anything runs, so a bad value stops the command (exit 2).
  Decode it with `config.DecodeBlock`, which rejects unknown keys and wrong types with the file's
  line, as the rest of the config does.
- `PageComparer` is a `Comparer` that also compares two runs a pair of pages at a time, so a big
  site's snapshot is never read whole. Every built-in comparer is one; a module that isn't makes
  `compare` read both runs into memory. It must give the changes `Compare` gives (a test in
  `compare` holds the built-in ones to that).
- `IssueOwner` is a `Comparer` that reports every change in its own issues itself.
- `NeedsChrome` marks a module whose `TabPreparer` or `TabLive` hooks use `Tab.Rod()`. See
  [Browser engines](#browser-engines).

A module registers itself with `check.Register` from its package's `init`; `--list-checks` reads
the registry. Add its name to `config.Modules` (and to `config.DefaultModules` when it should run
by default) and a builder to `cmd/rhythm-soundcheck/modules.go`, which makes the module for each
command. A test fails when the three don't agree, or when a `Check:` id in `checks/` is missing
from its module's `Checks()`.

Hooks that get no context of their own (`PageChecker.CheckPage`) take the run's from
`cc.RunContext()`. Pass it to anything slow they start, such as a child process, so it stops with
the run on Ctrl-C.

### A page's life cycle

```
 for each page, in its own tab (up to `tabs` at once)
 ┌────────────────────────────────────────────────────────────────┐
 │ Prepare / PrepareTab         fresh tab, nothing loaded yet     │
 │ navigate                     desktop viewport, wait to settle  │
 │ Live / LiveTab               desktop, page as loaded           │
 │   (phase order: PhaseMutate modules last)                      │
 │ mobile pass                  reloads at the mobile viewport    │
 │ PageChecker                  recorded page, no browser         │
 └────────────────────────────────────────────────────────────────┘
                               │
                               ▼
 SiteChecker                   once, with every page recorded
                               │
                     compare (snapshot vs now)
                               ▼
 Comparer                      each module's own changes
 ChangeFilter                  tidies the full list
```

A hook that panics doesn't end the run: the runner calls each one through `check.Safe` and the
panic becomes a warning naming the module, the hook and where it happened. Hooks of different
pages run at the same time, so a module that keeps state between hooks guards it with a mutex.
One that changes `cc.Run` holds `cc.Lock()`. Release it with `defer cc.Unlock()` whenever
the code in between could panic: the panic warning takes the same lock, so a lock left held stops
the whole run.

### Browser engines

Pages are captured in Chrome by default and in Firefox or WebKit when a run asks for them
(`--browser firefox`, `--browser webkit`, or `firefox=<path>` for a binary of your own). Chrome
is driven through its DevTools protocol (rod); the other engines through WebDriver BiDi, or
classic WebDriver for a WebKit driver without BiDi (package
[`capture/webdriver`](capture/webdriver)). Every engine gives a `capture.Tab`, which is a
`check.Tab` plus navigation, its settings and its network.

`check.ChromeOnly` decides which modules run in the other engines. A module with a `Preparer` or
`Live` hook (they get rod's tab) or with `NeedsChrome` returning true runs only in Chrome: in
another engine the runner skips it with the warning `<module> was skipped in firefox: it needs
Chrome`. A module written against `check.Tab` (`TabPreparer`, `TabLive`) and the
recorded page runs everywhere, so prefer those hooks. `Tab.Rod()` is nil outside Chrome.

The engines report the same facts in the same words where they can: resource types use Chrome's
names (`Image`, `Script`, `Fetch`…), uncaught exceptions read `Uncaught <message> (<url>:<line>)`
and console levels use Chrome's (`warning`, not `warn`). Some things differ: network errors keep
the engine's own name (`net::ERR_…` in Chrome, `NS_ERROR_…` in Firefox), the mobile viewport is
only a size outside Chrome (no phone emulation, so a page without a meta viewport isn't reported
as not responsive) and classic WebDriver has no events, preload scripts or full-page shots.
Screenshots of another engine are named after it (`shots/<page>-desktop-firefox.png`).

A change to the engines runs the driver contract,
[capture/drivertest](capture/drivertest/drivertest.go), against Chrome always and against Firefox
and WebKit when they are installed (the tests skip otherwise).
[runner/engine_test.go](runner/engine_test.go) checks that one broken site gives the same health
issues in Chrome and Firefox.

### Issues and their key

Every issue has a `Check` (`"<module>/<rule>"`, for example `health/php-error`), a `Level`, a
`Title` and usually a `Source`. `check.Page` and `Context.AddIssue` set the issue's key from
`Check`, `Title` and `Source`. A compare matches issues across runs by that key, so:

- Keep the `Title` the same every time the problem happens. Put what changes between runs
  (timestamps, request IDs, counts, line noise) in `Detail`, which is not part of the key.
  `PHP Warning: Undefined array key "id"` is a good title; the full message with a time in it is not.
- Make `Source` where the problem comes from, as stable as the code is: a theme or plugin file and
  line (`themes/brew/functions.php:212`), a script URL without its cache-busting query, a plugin
  slug. Leave it empty when there is nothing better than the page.
- Two issues with the same key on one page are one problem. Pick the title and source so that two
  different problems never share them.

### Tests

Each check needs a positive and a negative fixture: a page (an `httptest` server or a recorded
`model.Page`) where the check reports the problem and one where it doesn't. A check that can
raise false alarms needs a negative fixture for each near miss it must ignore.
[checks/health/health_test.go](checks/health/health_test.go) is a good template.

Most modules need no browser to test:

- A `PageChecker` or `SiteChecker` takes a `model.Page` or a run you build in the test.
- A `TabPreparer` or `TabLive` hook takes a `checktest.Tab`, a fake that answers `Eval` from a
  table, returns a fixed screenshot and plays back events; `checktest.Context` gives it a context.
  See the example in [check/example_test.go](check/example_test.go).
- `Tab.Rod()` is the way out to everything else rod can do. Tests of a hook that uses it need
  Chrome: start them with a check that skips when no browser is installed, like `needBrowser` in
  [runner/runner_test.go](runner/runner_test.go).

Fixtures only ever run on `127.0.0.1`. Tests never contact real sites.

Browser tests run on slow CI runners too, so a wait in a test is never a fixed number:

- Write it as `testenv.Timeout(d)` (or `testenv.Seconds(n)` for a config setting), with `d` what
  it takes on a quiet machine. [internal/testenv](internal/testenv/testenv.go) scales it by 2 on
  Linux CI and 3 on Windows and macOS CI; `SOUNDCHECK_TEST_SCALE=4` sets the factor by hand.
  When a duration is what the test checks, keep the timeout fixed and scale only the room left
  around it, as `TestVisitSlow` does.
- Open tabs with `browser.NewTab`, as runs do. A plain `sess.Page` tab can be hidden and never
  paint.
- Time a page against its own calibration, not a fixed number. A reading that is only higher than
  expected may be load from other tests: measure it again (see `TestMetricsInParallelTabs`),
  while a reading that shows the bug fails at once.
