<!-- eaa:begin -->
## EAA mode

The scenarios s49 to s89 run with `--eaa` (`go run -tags testbed ./testbed/bench -only eaa`). Broken ones must be caught; good ones (nothing broken, or a page built to be hard) must stay quiet. Each scenario's harness also checks what must never happen: an order placed or a side effect on a GET or on focus, a request to a host outside the testbed, a canary value from the page in any file Soundcheck wrote, or a statement draft that claims compliance.

> Automated and keyboard tests find some accessibility problems, not all. This is evidence for your own review, not a statement that the site meets WCAG, EN 301 549 or the European Accessibility Act.
>
> The testbed is written by the same people as the tool. It shows each detector works on a known case, not how much of a real site's accessibility it covers. Deque reports axe-based tests found 57% of issues by count in its own audits; that is a vendor figure counting issues, not success criteria.

**Caught 1 of 2 broken scenarios.** 1 more waits for a detector that isn't merged yet (1 of them caught already). **False alarms: 0 of 0 good scenarios, 1** in 12 `--eaa` compares of unchanged sites. Good scenarios still pending: 1 of 2 with a false alarm. Target (PRD 16.3): every broken scenario caught, none pending, no false alarm.

Run on 2026-10-26 with rhythm-soundcheck 1.3.0 (OS/ARCH).

### Scenarios

| # | Scenario | Expected | Soundcheck | Detector | Unlighthouse | axe-core CLI | pa11y |
|---|---|---|---|---|---|---|---|
| s49 | Keyboard trap in the coupon field (broken) | BROKE keyboard/trap in #tb_coupon (keyboard)<br>harness: orders | **caught** | pending (keyboard lane) | not run | missed | error |
| s50 | Checkout fields lose their labels (broken) | NEW a11y/label (3×) (a11y)<br>harness: statement | missed | merged | not run | **caught** | **caught** |
| s61 | Low-contrast Place order (broken) | NEW a11y/color-contrast (a11y) | **caught** | merged | not run | **caught** | **caught** |
| s77 | Correct native dialog (good) | nothing at or above NEW (keyboard) | false alarm | pending (keyboard lane) | not run | **caught** | – |
| s89 | A fully accessible small site (good) | nothing at or above NEW (keyboard)<br>harness: statement, requests | **caught** | pending (statement lane) | not run | – | – |

- **s49**: BROKE keyboard/trap: Focus is trapped [#tb_coupon] (/checkout/)
- **s50**: the expectation held (NEW a11y/label) but harness: statement: says "conform"
- **s61**: NEW a11y/color-contrast: Elements must meet contrast [#place_order] (/checkout/)
- **s77**: NEW keyboard/dialog-focus: x
- **s89**: nothing at or above NEW

### False alarms on unchanged sites

Every scenario good. Each round takes an `--eaa` snapshot and compares twice with nothing changed; the rounds marked by a scenario run on its pages with its flags. Target: 0.

| Site | Rounds | Compares | Pages | False alarms | Errors |
|---|---:|---:|---:|---:|---:|
| wp | 2 | 4 | 38 | 0 | 0 |
| php | 2 | 4 | 36 | 1 | 0 |
| s89 | 2 | 2 | 1 | 0 | 1 |

- NEW a11y/color-contrast: x (/a11y/backlog.php) (1×)
- s89 round 2 didn't finish: snapshot: exit 1

### Determinism

Issue keys of each round's snapshot against the first round's, for the same unchanged pages. Target: 0 differing keys.

| Site | Snapshots | Issue keys in the first | Differing keys |
|---|---:|---:|---:|
| wp | 2 | 1 | 0 |
| php | 2 | 1 | 2 |
| s89 | 1 | 0 | 0 |

- php round 2: only in the first: /a11y/backlog.php a11y/color-contrast\|#row-1
- php round 2: only in this one: /a11y/backlog.php a11y/color-contrast\|#row-2

### Cost

8 cores, linux/amd64, Chrome 141.0.7390.54, axe-core 4.11.0 (tags wcag2a, wcag2aa, wcag21a, wcag21aa), 2026-10-26. Seconds per page, median / p95, over the control pass's pages of each site. A page visit is the time Soundcheck records for the page; the keyboard walk is counted apart. The a11y audit is a run with `--modules health,a11y` minus one with `--modules health`, page by page.

| Measure | wp | php | Target (PRD 16.3) |
|---|---:|---:|---|
| Page visit, default run | 1.20 / 1.40 | 0.50 / 0.50 |  |
| Page visit, `--eaa` run | 3.50 / 6.00 | error |  |
| a11y audit per page | 1.10 / 2.60 | error | median at most 2 s |
| Keyboard walk per page | 2.00 / 4.20 | error | median at most 3 s |
| Whole `--eaa` run ÷ default run | 3.2× (130 s ÷ 40 s) **over** | – | at most 3× |
| One key flow (s70, `/checkout/`) | 9.4 s | | at most 15 s |

- php: eaa: exit 1

### Coverage

Which WCAG A and AA success criteria the tests can find failures of. A criterion with a test still needs a person to judge it: the tests find some failures, never a pass. The rest are `not tested: needs a person`.

| WCAG | Criteria | Automated | Keyboard | Needs a person only |
|---|---:|---:|---:|---:|
| 2.1 | 5 | 2 | 2 | 2 |
| 2.2 | 5 | 2 | 3 | 1 |

<details><summary>Every criterion</summary>

| Criterion | Level | EN 301 549 | WCAG | Automated | Keyboard |
|---|---|---|---|---|---|
| 1.1.1 Non-text Content | A | 9.1.1.1 | 2.1, 2.2 | `axe:image-alt` | – |
| 1.2.1 Audio-only and Video-only (Prerecorded) | A | 9.1.2.1 | 2.1, 2.2 | needs a person | needs a person |
| 2.1.2 No Keyboard Trap | A | 9.2.1.2 | 2.1, 2.2 | – | `keyboard/trap` |
| 2.4.7 Focus Visible | AA | 9.2.4.7 | 2.1, 2.2 | `a11y/focus-visible` | `keyboard/no-focus-ring` |
| 2.4.11 Focus Not Obscured (Minimum) | AA | 9.2.4.11 | 2.2 only | – | `keyboard/focus-obscured` |
| 4.1.1 Parsing | A | 9.4.1.1 | 2.1 only | needs a person | needs a person |

</details>

### Other tools

Run on the same states when installed. Caught means a violation in the broken state that the good state lacks.

| Tool | Caught (broken scenarios) | False alarms (good scenarios) | Violations in good states | Errors |
|---|---:|---:|---:|---:|
| Unlighthouse | not run | | | |
| axe-core CLI | 2 of 3 | 1 of 1 | 4 | 0 |
| pa11y | 2 of 2 | 0 of 0 | 2 | 1 |

- **Unlighthouse**: Lighthouse on the scenario's page in both states (desktop); caught when a category score drops by 0.03 or more (performance: 0.10). Not run; to include it, `npm i -g @unlighthouse/cli@0.19.1` (or `-tools unlighthouse` to run it through npx).
- **axe-core CLI**: axe-core with the WCAG 2.1 A and AA tags on the scenario's page in both states; caught when broken has a violation good lacks. Ran.
- **pa11y**: pa11y (HTML_CodeSniffer, WCAG2AA) on the scenario's page in both states; caught when broken has an error good lacks. Ran.

### Run it

```sh
make build
make -C testbed up
make -C testbed bench-eaa      # go run -tags testbed ./testbed/bench -only eaa (10 control rounds)
go run -tags testbed ./testbed/bench -only eaa -tools axe,pa11y,unlighthouse   # also run the other tools
```

An `-only eaa` run rewrites only this section of `docs/benchmark.md`. Runs are kept in `testbed/bench/work/<id>/` (with `statement.md` for the statement harness), the `--eaa` control pass in `work/control/eaa-<site>/` and the cost pass in `work/cost/`.
<!-- eaa:end -->
