<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/banner-dark.svg">
    <img alt="rhythm soundcheck" src="docs/assets/banner-light.svg" width="100%">
  </picture>
</p>

<p align="center">
  <b>Did your change break anything?</b><br>
  Snapshot your whole site, update a plugin or deploy, then see exactly what broke: PHP errors, dead buttons, forms that stopped sending, leaking caches, layout shifts and slowdowns. Built for PHP &amp; WordPress, works on any site.
</p>

<p align="center">
  <a href="https://github.com/RhythmShahriar/soundcheck/releases/latest"><img alt="Release" src="https://img.shields.io/github/v/release/RhythmShahriar/soundcheck?style=flat-square&color=ff7c52&label=release"></a>
  <a href="https://github.com/RhythmShahriar/soundcheck/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/RhythmShahriar/soundcheck/ci.yml?branch=master&style=flat-square&label=ci"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/license-MIT-100f0d?style=flat-square"></a>
  <a href="go.mod"><img alt="Go" src="https://img.shields.io/github/go-mod/go-version/RhythmShahriar/soundcheck?style=flat-square&color=100f0d"></a>
</p>

<p align="center">
  <a href="#install">Install</a> ·
  <a href="#check-a-change">Check a change</a> ·
  <a href="#what-it-catches">What it catches</a> ·
  <a href="https://github.com/RhythmShahriar/rhythm/wiki/Soundcheck">Docs</a> ·
  <a href="https://github.com/RhythmShahriar/rhythm/wiki/Soundcheck-CI">CI</a> ·
  <a href="https://github.com/RhythmShahriar/rhythm/wiki/Soundcheck-Benchmark">Benchmark</a>
</p>

<br>

<p align="center">
  <img alt="A Soundcheck comparison after a plugin update: a new PHP warning and a JavaScript error on five pages, new blocks at the top and bottom of every page at desktop and mobile size, and the verdict panel" src="docs/assets/compare.svg" width="720">
</p>

<br>

## The Rhythm set

| | Asks | Checks |
|---|---|---|
| [Tuner](https://github.com/RhythmShahriar/tuner) | Is my site safe? | what an attacker can find |
| **Soundcheck** | **Did my change break anything?** | **what visitors see and do** |
| [Echo](https://github.com/RhythmShahriar/echo) | Is my site still saying what it should? | everything the site sends |

## Install

**macOS · Linux**

```sh
curl -fsSL https://rythm.sh/install | sh
```

**Windows** (PowerShell)

```powershell
irm https://rythm.sh/install.ps1 | iex
```

**Go**

```sh
go install github.com/RhythmShahriar/soundcheck/cmd/rhythm-soundcheck@latest
```

`rhythm-soundcheck` works on its own; `rhythm soundcheck` is the same tool through the rhythm CLI. `rhythm-soundcheck --version` prints the version you have; `rhythm-soundcheck --help` lists every command and flag.

Soundcheck drives a real browser. It uses Chrome or Chromium if you have one, and otherwise downloads Chromium once, on first use, into your cache folder. The installers check every download against the release checksums, which are [signed with cosign](https://github.com/RhythmShahriar/rhythm/wiki/Verifying-Releases).

## Check a change

No site handy? `rhythm-soundcheck demo` serves a small built-in site, snapshots it, breaks it and shows the comparison, in a few seconds and without touching your project folder.

```sh
rhythm-soundcheck snapshot staging.yoursite.com   # before: record how every page works
# …update plugins, switch PHP, deploy…
rhythm-soundcheck compare --open                  # after: what changed, in a report
```

Soundcheck finds your pages from the sitemap and links, opens each one in headless Chrome at desktop and mobile size, clicks what can be clicked, measures, screenshots, and stores it all in `./.soundcheck`. `compare` checks the same pages again, with the snapshot's modules, levels and viewports unless you set others, and shows **only what's different**, most serious first:

| Class | Means |
|---|---|
| **BROKE** | worked in the snapshot, fails now: a 500, a fatal error, a form that no longer sends |
| **NEW** | a problem that wasn't there before: a PHP warning, a JS error, a dead button |
| **CHANGED** | looks or reads differently: layout, title, lost content |
| **SLOWER** | measurably slower, past a noise floor |
| **FIXED** | a problem from the snapshot is gone |

Add `--echo` (with [Echo](https://github.com/RhythmShahriar/echo) running) and each form submit and flow also records the emails, SMS and webhooks it sent, so "Form says sent, but no message went out" is caught too. See [Soundcheck with Echo](https://github.com/RhythmShahriar/rhythm/wiki/Echo-With-Soundcheck).

| You want to… | Run |
|---|---|
| Compare staging with production right now | `rhythm-soundcheck staging.site.com --against site.com` |
| Check a site once, no snapshot | `rhythm-soundcheck run site.test` |
| A fast pass: desktop only, health and interactions | `rhythm-soundcheck run site.test --quick` |
| Test that forms still send (staging only) | `rhythm-soundcheck snapshot staging.site.com --level submit` |
| Catch a cache serving logged-in pages to visitors | `SOUNDCHECK_USER=test SOUNDCHECK_PASSWORD=… rhythm-soundcheck snapshot …` |
| A report for the pull request | `rhythm-soundcheck compare --md -o soundcheck.md` |
| Every report format from one run | `rhythm-soundcheck compare -o r.json -o r.md -o r.xml -o r.html` |
| Fail CI when something broke | `rhythm-soundcheck compare --fail-on broke` |
| Move over from BackstopJS | `rhythm-soundcheck import backstop.json` |
| Move over from Lost Pixel (page paths only) | `rhythm-soundcheck import lostpixel.config.ts --pages-only` |

Every flag is in the [CLI reference](https://github.com/RhythmShahriar/rhythm/wiki/Soundcheck-CLI-Reference).

> [!IMPORTANT]
> Check only sites you own or have permission to test. Soundcheck browses like a visitor at 5 page loads a second; it only submits forms when you turn the `submit` level on, and refuses to on sites you list as production. See [Responsible use](#responsible-use--disclaimer).

## What it catches

<table>
<tr>
<td width="50%" valign="top">

### 🩺 Page health
PHP fatals, warnings, notices and deprecations with **file and line**, the WordPress "critical error" screen, white screens, JavaScript errors, failed XHR, REST and `admin-ajax` calls, broken links and assets, mixed content, lost titles and a new `noindex`.

</td>
<td width="50%" valign="top">

### 🖱️ Interactions
Menus, tabs, accordions and modals that stopped responding (**dead clicks**), clicks that throw, and forms that lost their nonce, labels or action. Opt in to submit forms with test data: Contact Form 7, WPForms, Gravity Forms, Elementor and WooCommerce, plus your own YAML flows.

</td>
</tr>
<tr>
<td valign="top">

### 🖼️ Visual
Desktop and mobile screenshots of every page, diffed pixel by pixel with anti-aliasing tolerance. Sliders, ads and clocks are learned and ignored. A header change on 300 pages is **one** line, not 300.

</td>
<td valign="top">

### 🗄️ Cache correctness
Page cache hits and misses across Cloudflare, LiteSpeed, Varnish, nginx and WordPress plugins; carts and accounts served from cache; **logged-in content leaking to visitors**; cached pages carrying expired nonces; stale cache.

</td>
</tr>
<tr>
<td valign="top">

### ⚡ Speed
Lab LCP, CLS, TBT, TTFB, weight and requests, compared with noise floors so only real slowdowns show. Site-wide suggestions grouped by cause, including **what each plugin costs** every page. `--lighthouse` hands off to Unlighthouse.

</td>
<td valign="top">

### 📋 Reports
A live terminal view; a standalone HTML report with before/after sliders and filters; Markdown for pull requests; JUnit and JSON for CI; and a [GitHub Action](https://github.com/RhythmShahriar/rhythm/wiki/Soundcheck-CI).

</td>
</tr>
</table>

## Why Soundcheck

- **Answers one question.** Not a 400-line audit of everything imperfect: only what your change did, ranked.
- **The whole site, not five pages.** Pages are grouped by URL pattern: once a folder has more than 10 pages, the rest of `/product/{slug}/` is sampled 3 at a time, so a shop with 3,000 products is checked through 13 pages, not 3,000. A 100-page site takes about a minute and a half to snapshot and one to compare (8 tabs on a 16-core machine).
- **Knows WordPress.** PHP errors traced to the plugin file and line, plugin-aware speed costs, form plugin profiles, nonce and cache rules no generic tool checks.
- **Local and free.** One binary, your machine or your CI. No account, no cloud, no page limits. Snapshots stay in your project.
- **Safe on real sites.** Polite rate limits, a destructive-click blocklist, form submits off by default and refused on production, credentials only from environment variables, typed only on the site being checked and never written to reports.

How it compares with other tools is measured, not claimed: see the [benchmark](https://github.com/RhythmShahriar/rhythm/wiki/Soundcheck-Benchmark) of 20 deliberately broken sites.

## CI

```yaml
- uses: RhythmShahriar/soundcheck@v1
  with:
    url: https://staging.yoursite.com
    against: https://yoursite.com
    fail-on: broke
```

The Markdown report goes in the job summary, and the HTML report and a JUnit file are attached as the `soundcheck-report` artifact. More in the [CI guide](https://github.com/RhythmShahriar/rhythm/wiki/Soundcheck-CI).

## Docs

| | |
|---|---|
| [Your first check](https://github.com/RhythmShahriar/rhythm/wiki/Soundcheck-First-Check) | snapshot, change, compare, in ten minutes |
| [Reading results](https://github.com/RhythmShahriar/rhythm/wiki/Soundcheck-Reading-Results) | the five classes, grouping, the HTML report |
| [Interactions](https://github.com/RhythmShahriar/rhythm/wiki/Soundcheck-Interactions) | levels, dead clicks and safety rules |
| [Forms and flows](https://github.com/RhythmShahriar/rhythm/wiki/Soundcheck-Forms-And-Flows) | form profiles, test data, YAML flows, logging in |
| [Cache](https://github.com/RhythmShahriar/rhythm/wiki/Soundcheck-Cache) | hits, private pages, leaks, nonces |
| [Configuration](https://github.com/RhythmShahriar/rhythm/wiki/Soundcheck-Configuration) | `.soundcheck/config.yaml` |
| [CLI reference](https://github.com/RhythmShahriar/rhythm/wiki/Soundcheck-CLI-Reference) | every command, flag, exit code and file |

All of it lives in the **[Rhythm documentation wiki](https://github.com/RhythmShahriar/rhythm/wiki)**.

## Contributing

```sh
git clone https://github.com/RhythmShahriar/soundcheck && cd soundcheck
make hooks   # Conventional Commits check
make check   # lint + tests (browser tests need Chrome or Chromium)
make test-short # the quick pass: tests that honour -short skip slow browser work
make testbed # 20 broken sites in Docker, for end-to-end tests and the benchmark
```

See [CONTRIBUTING](https://github.com/RhythmShahriar/.github/blob/master/CONTRIBUTING.md).

## Responsible use & disclaimer

Soundcheck is built to help people **check their own websites** before and after changes.

- **Use it only on sites you own, or have explicit permission to test.** Automated browsing, and especially submitting forms, on systems you aren't authorized to test may be illegal or break their terms of service.
- **You are responsible for how you use it.** The authors and contributors are not responsible for misuse, or for any damage, data, messages or orders created by tests you run, or any other loss arising from its use.
- **Test data is real data.** With the `submit` level on, forms really send: messages arrive, sign-ups are created. Use it on staging, route mail to a catcher such as Mailpit, and list your live site under `production` in the config.
- **No warranty.** Soundcheck is provided "as is", without warranty of any kind, as set out in the [license](LICENSE). Lab measurements and screenshots vary; verify before acting.

Found a vulnerability in Soundcheck itself? Please [report it privately](https://github.com/RhythmShahriar/soundcheck/security/advisories/new).

## License

[MIT](LICENSE).

<p align="center"><sub>Part of <a href="https://github.com/RhythmShahriar/rhythm"><b>rhythm</b></a> · Useful ideas. Working systems.</sub></p>
