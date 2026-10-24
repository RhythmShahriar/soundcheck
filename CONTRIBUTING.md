# Contributing to Soundcheck

Setup, commit messages and the review process are shared by every Rhythm tool and live in the
[shared CONTRIBUTING](https://github.com/RhythmShahriar/.github/blob/master/CONTRIBUTING.md).
This page covers what is particular to Soundcheck: how a module plugs into a run.

```sh
make hooks   # Conventional Commits check
make check   # lint + tests (browser tests need Chrome or Chromium)
```

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
- `Configurable` decodes the module's own block of `config.yaml`.
- `IssueOwner` is a `Comparer` that reports every change in its own issues itself.

A module registers itself with `check.Register` from its package's `init`. The command then only
blank-imports the package (until every module does, it also lists them in
`cmd/rhythm-soundcheck/modules.go`).

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
pages run at the same time, so a module that keeps state between hooks guards it with a mutex,
and one that changes `cc.Run` holds `cc.Lock()`.

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
