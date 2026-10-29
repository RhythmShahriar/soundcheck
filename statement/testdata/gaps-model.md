> **Draft for you to review. Not ready to publish.**
>
> Rhythm Soundcheck 1.3.0 wrote this on 2026-10-26 from a check of 4 pages of https\://shop.example.
>
> Automated and keyboard tests find some accessibility problems, not all. This is evidence for your own review, not a statement that the site meets WCAG, EN 301 549 or the European Accessibility Act.
>
> Fill in every \[TO DO\], test the criteria marked "needs a person", then delete this box.

# Accessibility statement for Example Coffee Ltd

Example Coffee Ltd wants https\://shop.example to be usable by as many people as possible.

This statement covers the 4 pages Soundcheck checked, listed under "Pages checked". [TO DO: set statement.scope to the pages this statement covers, or check that these are all of them.]

## How accessible this website is

[TO DO: choose one after your review: "fully compliant", "partially compliant" or "not compliant" with WCAG 2.2 level AA / EN 301 549 V4.1.1. Soundcheck does not choose this for you. Its tests found failures of 0 of 55 criteria and could not test 28 of them.]

## What isn't accessible yet

### Problems found

On 2026-10-26 Soundcheck's tests found no failures on the pages where they finished. That doesn't mean there are none: 28 criteria need a person and some pages weren't fully tested (see "Not tested").

### Disproportionate burden

[TO DO: say if fixing any of this would be a disproportionate burden and why, or delete this section.]

### Content outside the scope of the law

[TO DO: list content the law doesn't cover, such as third-party content you don't control, or delete this section.]

## How this statement was prepared

This statement was prepared on [TO DO: date] by [TO DO: who].

Rhythm Soundcheck 1.3.0 checked 4 pages of https\://shop.example on 2026-10-26 with automated tests (axe-core 4.14.0, 2 rules for WCAG 2.2 A and AA) and a keyboard walkthrough of 3 pages (0 widgets tested, 0 key flows run).

It was also checked by [TO DO: your manual review, such as a screen reader test of checkout].

It was last reviewed on [TO DO: date].

### What these tests can and can't tell

- Soundcheck's EAA mode gives you evidence about your site. It is not an audit, not a certification and not legal advice.
- Many success criteria need a person: meaningful alt text, captions, reading order, plain language. The draft marks them not tested: needs a person, never passed.
- Only the pages and flows Soundcheck visited, in the states it saw, in Chrome, with the keyboard walk at desktop width. Content in iframes from other sites, PDFs, videos, apps and emails was not tested.
- The keyboard walkthrough uses Chrome's keyboard events. It is not a screen reader test, not a voice control test and not a test with disabled users.
- The site decides what Soundcheck sees. A site that serves headless browsers a different page can pass while visitors fail. Overlay widgets don't make a site accessible.
- EAA mode presses keys on your pages. It holds requests that would change data, blocks downloads and follows no links except skip links. A site that changes data on a GET or on focus can still change. Run it on staging and list your live site under production.
- Reports contain what your pages showed. Soundcheck masks form values, credentials and tokens it recognises and emails, phone numbers and card numbers in text it writes. Masking is pattern-based: it is pseudonymised, not anonymised. Screenshots are not masked.
- A signed evidence pack proves who produced it and that it wasn't changed afterwards. It doesn't prove the site was in that state for every visitor.
- Nothing leaves your machine except requests to your own site and hosts you allow. No telemetry, no hosted service, no AI.

## Feedback and contact

If something on this website doesn't work for you, tell us: email access\@shop.example.

We reply within [TO DO: days; or set statement.response\_days] days.

## Enforcement

[TO DO: name the authority that enforces accessibility law where you operate and how to reach it; or set statement.enforcement.]

## Appendix: every WCAG 2.2 A and AA criterion

| Criterion | Level | EN 301 549 | How Soundcheck tests it | Result |
|---|---|---|---|---|
| 1.1.1 Non-text Content | A | 9.1.1.1 | partly: axe aria-meter-name, aria-progressbar-name, image-alt, input-image-alt, object-alt, role-img-alt, svg-img-alt | no failures found by these tests |
| 1.2.1 Audio-only and Video-only (Prerecorded) | A | 9.1.2.1 | not tested: needs a person | not tested: needs a person |
| 1.2.2 Captions (Prerecorded) | A | 9.1.2.2 | not tested: needs a person | not tested: needs a person |
| 1.2.3 Audio Description or Media Alternative (Prerecorded) | A | 9.1.2.3 | not tested: needs a person | not tested: needs a person |
| 1.2.4 Captions (Live) | AA | 9.1.2.4 | not tested: needs a person | not tested: needs a person |
| 1.2.5 Audio Description (Prerecorded) | AA | 9.1.2.5 | not tested: needs a person | not tested: needs a person |
| 1.3.1 Info and Relationships | A | 9.1.3.1 | partly: axe aria-hidden-body, aria-required-children, aria-required-parent, definition-list, dlitem, list, listitem, td-headers-attr, th-has-data-cells | no failures found by these tests |
| 1.3.2 Meaningful Sequence | A | 9.1.3.2 | not tested: needs a person | not tested: needs a person |
| 1.3.3 Sensory Characteristics | A | 9.1.3.3 | not tested: needs a person | not tested: needs a person |
| 1.3.4 Orientation | AA | 9.1.3.4 | not tested: needs a person | not tested: needs a person |
| 1.3.5 Identify Input Purpose | AA | 9.1.3.5 | partly: axe autocomplete-valid | no failures found by these tests |
| 1.4.1 Use of Color | A | 9.1.4.1 | partly: axe link-in-text-block | no failures found by these tests |
| 1.4.2 Audio Control | A | 9.1.4.2 | partly: axe no-autoplay-audio | no failures found by these tests |
| 1.4.3 Contrast (Minimum) | AA | 9.1.4.3 | partly: axe color-contrast | no failures found by these tests |
| 1.4.4 Resize Text | AA | 9.1.4.4 | partly: axe meta-viewport | no failures found by these tests |
| 1.4.5 Images of Text | AA | 9.1.4.5 | not tested: needs a person | not tested: needs a person |
| 1.4.10 Reflow | AA | 9.1.4.10 | partly: a11y/reflow | no failures found by these tests |
| 1.4.11 Non-text Contrast | AA | 9.1.4.11 | not tested: needs a person | not tested: needs a person |
| 1.4.12 Text Spacing | AA | 9.1.4.12 | partly: axe avoid-inline-spacing | no failures found by these tests |
| 1.4.13 Content on Hover or Focus | AA | 9.1.4.13 | not tested: needs a person | not tested: needs a person |
| 2.1.1 Keyboard | A | 9.2.1.1 | partly: axe frame-focusable-content, scrollable-region-focusable, server-side-image-map; keyboard/not-reachable, keyboard/not-operable, keyboard/menu | no failures found by these tests |
| 2.1.2 No Keyboard Trap | A | 9.2.1.2 | partly: keyboard/trap | no failures found by these tests |
| 2.1.4 Character Key Shortcuts | A | 9.2.1.4 | not tested: needs a person | not tested: needs a person |
| 2.2.1 Timing Adjustable | A | 9.2.2.1 | partly: axe meta-refresh | no failures found by these tests |
| 2.2.2 Pause, Stop, Hide | A | 9.2.2.2 | partly: axe blink, marquee | no failures found by these tests |
| 2.3.1 Three Flashes or Below Threshold | A | 9.2.3.1 | not tested: needs a person | not tested: needs a person |
| 2.4.1 Bypass Blocks | A | 9.2.4.1 | partly: axe bypass; keyboard/skip-link-broken | no failures found by these tests |
| 2.4.2 Page Titled | A | 9.2.4.2 | partly: axe document-title | no failures found by these tests |
| 2.4.3 Focus Order | A | 9.2.4.3 | partly: keyboard/focus-lost, keyboard/dialog-focus | no failures found by these tests |
| 2.4.4 Link Purpose (In Context) | A | 9.2.4.4 | partly: axe area-alt, link-name | no failures found by these tests |
| 2.4.5 Multiple Ways | AA | 9.2.4.5 | not tested: needs a person | not tested: needs a person |
| 2.4.6 Headings and Labels | AA | 9.2.4.6 | not tested: needs a person | not tested: needs a person |
| 2.4.7 Focus Visible | AA | 9.2.4.7 | partly: keyboard/no-focus-ring, keyboard/focus-invisible | no failures found by these tests |
| 2.4.11 Focus Not Obscured (Minimum) | AA | 9.2.4.11 | partly: keyboard/focus-obscured | no failures found by these tests |
| 2.5.1 Pointer Gestures | A | 9.2.5.1 | not tested: needs a person | not tested: needs a person |
| 2.5.2 Pointer Cancellation | A | 9.2.5.2 | not tested: needs a person | not tested: needs a person |
| 2.5.3 Label in Name | A | 9.2.5.3 | partly: axe label-content-name-mismatch | no failures found by these tests |
| 2.5.4 Motion Actuation | A | 9.2.5.4 | not tested: needs a person | not tested: needs a person |
| 2.5.7 Dragging Movements | AA | 9.2.5.7 | not tested: needs a person | not tested: needs a person |
| 2.5.8 Target Size (Minimum) | AA | 9.2.5.8 | partly: axe target-size | no failures found by these tests |
| 3.1.1 Language of Page | A | 9.3.1.1 | partly: axe html-has-lang, html-lang-valid, html-xml-lang-mismatch | no failures found by these tests |
| 3.1.2 Language of Parts | AA | 9.3.1.2 | partly: axe valid-lang | no failures found by these tests |
| 3.2.1 On Focus | A | 9.3.2.1 | partly: keyboard/context-change | no failures found by these tests |
| 3.2.2 On Input | A | 9.3.2.2 | not tested: needs a person | not tested: needs a person |
| 3.2.3 Consistent Navigation | AA | 9.3.2.3 | not tested: needs a person | not tested: needs a person |
| 3.2.4 Consistent Identification | AA | 9.3.2.4 | not tested: needs a person | not tested: needs a person |
| 3.2.6 Consistent Help | A | 9.3.2.6 | not tested: needs a person | not tested: needs a person |
| 3.3.1 Error Identification | A | 9.3.3.1 | partly: keyboard/error-not-linked | no failures found by these tests |
| 3.3.2 Labels or Instructions | A | 9.3.3.2 | partly: axe form-field-multiple-labels; a11y/placeholder-label | no failures found by these tests |
| 3.3.3 Error Suggestion | AA | 9.3.3.3 | not tested: needs a person | not tested: needs a person |
| 3.3.4 Error Prevention (Legal, Financial, Data) | AA | 9.3.3.4 | not tested: needs a person | not tested: needs a person |
| 3.3.7 Redundant Entry | A | 9.3.3.7 | not tested: needs a person | not tested: needs a person |
| 3.3.8 Accessible Authentication (Minimum) | AA | 9.3.3.8 | not tested: needs a person | not tested: needs a person |
| 4.1.2 Name, Role, Value | A | 9.4.1.2 | partly: axe area-alt, aria-allowed-attr, aria-braille-equivalent, aria-command-name, aria-conditional-attr, aria-deprecated-role, aria-hidden-body, aria-hidden-focus, aria-input-field-name, aria-prohibited-attr, aria-required-attr, aria-roledescription, aria-roles, aria-tab-name, aria-toggle-field-name, aria-tooltip-name, aria-valid-attr, aria-valid-attr-value, button-name, duplicate-id-aria, frame-title, frame-title-unique, input-button-name, input-image-alt, label, link-name, nested-interactive, select-name, summary-name | no failures found by these tests |
| 4.1.3 Status Messages | AA | 9.4.1.3 | not tested: needs a person | not tested: needs a person |

## Not tested

- /broken/: a11y failed (the page answered 500); its criteria count as not tested there.
- /broken/: keyboard was skipped (the page answered 500); its criteria count as not tested there.
- /catalog/: a11y timed out (axe-core timed out after 45s); its criteria count as not tested there.
- Content in frames from js.stripe.com wasn't tested.
- 28 criteria need a person: 1.2.1, 1.2.2, 1.2.3, 1.2.4, 1.2.5, 1.3.2, 1.3.3, 1.3.4, 1.4.5, 1.4.11, 1.4.13, 2.1.4, 2.3.1, 2.4.5, 2.4.6, 2.5.1, 2.5.2, 2.5.4, 2.5.7, 3.2.2, 3.2.3, 3.2.4, 3.2.6, 3.3.3, 3.3.4, 3.3.7, 3.3.8, 4.1.3.
- Only Chrome was used. The keyboard walk ran at desktop width.
- No screen reader, voice control or test with disabled users.
- EN 301 549 clauses 5 (generic requirements, such as closed functionality), 6 (two-way voice communication), 7 (video capabilities), 10 (non-web documents), 11 (software) and 12 (documentation and support services) are outside a website check and weren't tested.

## Pages checked

- /
- /broken/
- /catalog/
- /pay/
