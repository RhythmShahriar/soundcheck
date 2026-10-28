# Roadmap

What we plan for Soundcheck. Plans change as we learn, so treat this as a direction, not a promise.

Have an idea or a kind of breakage we miss? Start a thread in [Discussions: Ideas](https://github.com/RhythmShahriar/soundcheck/discussions/categories/ideas). Want to help now? Pick a [good first issue](https://github.com/RhythmShahriar/soundcheck/labels/good%20first%20issue).

## Now

- **Soundcheck 1.3: EAA mode.** `--eaa` for accessibility evidence under the European Accessibility Act: **WCAG 2.1 AA** rules (`--wcag 2.2` for the 2.2 rules), a keyboard walkthrough of every page and its menus and dialogs, key flows run by keyboard, an accessibility statement draft, an evidence pack with a hash chain and signing, EARL export for WCAG-EM and `eaa`, `statement` and `wcag` inputs for the GitHub Action. Built and tested against the testbed's 41 EAA scenarios; in final review before the release.

## Shipped

- **Soundcheck 1.2.** Review and accept, zero-noise results with `--confirm`, CI baselines with a pull request comment, `wp-update` with culprit finding, accessibility and SEO modules, fleet mode with client reports, error rules in YAML, history and trends, `--resume` and import from BackstopJS and Lost Pixel.
- **Firefox and WebKit.** `--browser firefox` and `--browser webkit` capture pages in those engines through WebDriver (EAA mode stays Chrome only).
- **Pinned Chrome for Testing.** A verified browser download for machines without Chrome.
- **Windows and macOS.** CI on all three systems for every pull request.

## Next

- **Accessibility behind a login.** The a11y audit and keyboard walk on logged-in pages, not only in key flows. Testbed scenario s68 waits for it, so the EAA benchmark reads 29 of 29 with s68 pending.
- **A faster keyboard walk on WordPress.** The walk takes a median 5.8 s per page on the testbed's WordPress site against a 3 s target (1.0 s on plain PHP).
- **JSON on stdout next to report files.** `--json` with several `-o` is refused today, since each file takes its format from its extension; CI that reads JSON from stdout and keeps the files has to read `-o r.json` instead.
- **Progress in the demo's snapshot step.** `demo` shows nothing while it takes its first snapshot, which can take half a minute.
- **One CLI contract.** Move onto rhythm's `kit/cli.Main` for `version --json`, shared exit codes, signal handling and the update prompt.
- **A safer Lighthouse option.** Pin an Unlighthouse tree without known advisories.
- **Public benchmark with false alarms.** Rerun the shared testbed and publish a false-alarm column next to the catch rate for every module, as the EAA section already has.

## Later

- **Statements in German and French.** More statement languages after a legal review in each country.
- **Mobile keyboard walk.** The keyboard walkthrough at phone width too, not only at desktop width.
- **WCAG 2.2 by default.** Once the EU Official Journal cites EN 301 549 V4.1.1, make `--wcag 2.2` the default for `--eaa` in a minor release, never silently.
- **Big sites.** Compare reads a snapshot a pair of pages at a time; measure and trim memory for runs of tens of thousands of pages.
- **Update night with Tuner and Echo.** One run that orders security updates, checks what each one stopped sending and finds the culprit.
- **One rule format.** Soundcheck error rules, Tuner checks and Echo custom rules share one YAML loader.
