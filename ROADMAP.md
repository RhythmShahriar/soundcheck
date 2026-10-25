# Roadmap

What we plan for Soundcheck. Plans change as we learn, so treat this as a direction, not a promise.

Have an idea or a kind of breakage we miss? Start a thread in [Discussions: Ideas](https://github.com/RhythmShahriar/soundcheck/discussions/categories/ideas). Want to help now? Pick a [good first issue](https://github.com/RhythmShahriar/soundcheck/labels/good%20first%20issue).

## Now

- **Soundcheck 1.2.** Review and accept, zero-noise results with `--confirm`, CI baselines with a pull request comment, `wp-update` with culprit finding, accessibility and SEO modules, fleet mode with client reports, error rules in YAML, history and trends, `--resume` and import from BackstopJS and Lost Pixel.
- **Pinned Chrome for Testing.** A verified browser download for machines without Chrome.
- **Windows and macOS.** CI on all three systems for every pull request.

## Next

- **One CLI contract.** Move onto rhythm's `kit/cli.Main` for `version --json`, shared exit codes, signal handling and the update prompt.
- **WCAG 2.1 AA.** Let the accessibility module run the 2.1 rules too.
- **A safer Lighthouse option.** Pin an Unlighthouse tree without known advisories.
- **Public benchmark with false alarms.** Rerun the shared testbed and publish a false-alarm column next to the catch rate.

## Later

- **Firefox and WebKit.** Capture in more than one engine.
- **Big sites.** Stream pages to disk so sites with tens of thousands of pages fit in memory.
- **Update night with Tuner and Echo.** One run that orders security updates, checks what each one stopped sending and finds the culprit.
- **One rule format.** Soundcheck error rules, Tuner checks and Echo custom rules share one YAML loader.
