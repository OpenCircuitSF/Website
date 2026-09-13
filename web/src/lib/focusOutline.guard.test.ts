// @vitest-environment jsdom
//
// #0517 / #0535: pins that no CSS rule -- in app.css or any component
// <style> block -- gives a green (var(--accent)) or any other visible
// outline to a non-interactive tabindex="-1" focus target (every route
// heading, the four named in-page targets, or any other tabindex="-1"
// element such as a modal container), an <a>, a .nav-tab, or a .subtab,
// including through the SHARED :where(...):focus-visible rule in app.css.
// That shared rule is exactly the mechanism that made a first attempt at
// fixing only h1[tabindex='-1']:focus insufficient: it also matches every
// [tabindex] and every <button> (nav-tab/subtab included), so a fix that
// only touches the rule named after the bug leaves this one still painting
// it. This guard also asserts the positive side: ordinary buttons, summary,
// and form fields keep their accent-based focus indicator unchanged, and
// links/.nav-tab/.subtab get a real (non-outline, non-green) replacement --
// an underline -- rather than silently losing keyboard-focus visibility
// altogether.
//
// Proved against real CSS, not a copy: app.css is read from disk with
// node:fs and every .svelte file's <style> block is read via
// import.meta.glob, so an edit to the actual stylesheet changes what this
// test evaluates -- the oracle is not satisfiable by an edit to the subject
// (CLAUDE.md §8). Rule selectors
// are matched against real DOM elements using jsdom's native
// Element.matches() (nwsapi) -- the same selector semantics a browser uses
// for :where(), :not(), and descendant combinators -- rather than a
// hand-rolled selector matcher that could disagree with a browser about a
// selector shape it wasn't written to expect. The one thing this cannot
// check is real :focus-visible input-modality heuristics (jsdom does not
// implement them, and neither would a hand-rolled matcher); every rule
// checked here already contains a literal `:focus` or `:focus-visible`
// pseudo-class in its selector, which is stripped before matching, so what
// is being asked is "IF this rule's focus trigger fires on this element,
// what does it visually do" -- exactly the question a static CSS test can
// answer. Verified separately in real Safari and a Chromium browser (see
// the issues' ## Verification) for the actual focus-trigger behaviour.
//
// This was run against the pre-fix app.css (the original
// `:where(a, button, summary, [tabindex]):focus-visible` rule) and failed
// as expected: 12 of the 20 tests below failed -- all six of the
// tabindex="-1" candidates (the generic heading, the generic non-h1
// element, and the four named in-page targets) and all six of the
// <a>/.nav-tab/.subtab candidates, every one of them because that single
// shared rule matched and supplied `outline: 2px solid var(--accent)`.
// The eight other tests (ordinary button, summary, the three form fields,
// and the two DOM focus-move assertions) passed unchanged, since the
// pre-fix CSS never touched their behaviour. See the issues' ##
// Verification for the full transcript.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import postcss from 'postcss';

// Read app.css from disk rather than `?raw` importing it: Vite's own CSS
// plugin intercepts a plain `.css` module before a `?raw` query can apply
// to it, so `import x from '../app.css?raw'` silently resolves to an EMPTY
// string under Vitest (measured directly -- length 0) instead of erroring,
// which would have made this guard vacuously pass every check against
// app.css. `.svelte` files have no such special-cased loader, so
// import.meta.glob's `?raw` query works correctly for them below.
const appCssSource = readFileSync(path.join(path.dirname(fileURLToPath(import.meta.url)), '../app.css'), 'utf-8');

const SVELTE_SOURCES = import.meta.glob('../**/*.svelte', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

type Decls = Record<string, string>;

interface FocusRule {
  file: string;
  selector: string;
  decls: Decls;
}

/** Split a selector list on top-level commas only -- commas inside
 * :where(...)/:not(...) parens must NOT split the selector, or
 * ":where(a, button):focus-visible" would be misread as two selectors
 * ("a" and " button):focus-visible"), one of them malformed. */
function splitTopLevel(input: string, sep: string): string[] {
  const parts: string[] = [];
  let depth = 0;
  let cur = '';
  for (const ch of input) {
    if (ch === '(') depth++;
    else if (ch === ')') depth--;
    if (ch === sep && depth === 0) {
      parts.push(cur);
      cur = '';
    } else {
      cur += ch;
    }
  }
  parts.push(cur);
  return parts;
}

/** Strip exactly one trailing :focus or :focus-visible pseudo-class so the
 * remainder is a plain structural selector jsdom's Element.matches() can
 * evaluate against an element that is not (and, for a tabindex="-1"
 * target, cannot meaningfully be) actually focused in a static test. */
function stripFocusPseudo(selector: string): string {
  return selector.replace(/:focus(-visible)?\s*$/, '').trim();
}

function extractStyleBlocks(source: string): string {
  const re = /<style[^>]*>([\s\S]*?)<\/style>/g;
  const blocks: string[] = [];
  let m: RegExpExecArray | null;
  // eslint-disable-next-line no-cond-assign
  while ((m = re.exec(source))) blocks.push(m[1]);
  return blocks.join('\n\n');
}

function collectFocusRules(): FocusRule[] {
  const files: { file: string; text: string }[] = [{ file: 'app.css', text: appCssSource }];
  for (const [key, src] of Object.entries(SVELTE_SOURCES)) {
    const styleText = extractStyleBlocks(src);
    if (styleText.trim()) files.push({ file: key.replace(/^\.\.\//, ''), text: styleText });
  }

  const rules: FocusRule[] = [];
  for (const { file, text } of files) {
    let root;
    try {
      root = postcss.parse(text, { from: undefined });
    } catch (e) {
      throw new Error(`focusOutline.guard: failed to parse CSS extracted from ${file}: ${(e as Error).message}`);
    }
    root.walkRules((rule) => {
      for (const rawSelector of splitTopLevel(rule.selector, ',')) {
        const selector = rawSelector.trim();
        if (!selector.includes(':focus')) continue;
        const decls: Decls = {};
        rule.walkDecls((d) => {
          decls[d.prop] = decls[d.prop] ? `${decls[d.prop]}; ${d.value}` : d.value;
        });
        rules.push({ file, selector, decls });
      }
    });
  }
  return rules;
}

function hasAccentOutline(decls: Decls): boolean {
  const outline = `${decls.outline ?? ''} ${decls['outline-color'] ?? ''}`;
  return /var\(--accent\)/.test(outline);
}

function hasVisibleOutline(decls: Decls): boolean {
  const outline = (decls.outline ?? '').trim().toLowerCase();
  if (!outline) return false;
  return outline !== 'none' && outline !== '0';
}

function hasAccentFocusRing(decls: Decls): boolean {
  const borderColor = decls['border-color'] ?? '';
  const boxShadow = decls['box-shadow'] ?? '';
  return /var\(--accent/.test(borderColor) || /var\(--accent/.test(boxShadow);
}

function hasUnderline(decls: Decls): boolean {
  const deco = `${decls['text-decoration-line'] ?? ''} ${decls['text-decoration'] ?? ''}`;
  return /underline/.test(deco);
}

describe('no green/--accent outline on a link, .nav-tab, .subtab, or non-interactive tabindex="-1" target (#0517, #0535)', () => {
  const focusRules = collectFocusRules();

  // Fail loudly rather than silently passing an empty scan (CLAUDE.md's
  // fail-open warning) -- this guard is worthless if the CSS it exists to
  // check moved out from under the `?raw`/glob it reads.
  it('found real :focus/:focus-visible rules to check', () => {
    expect(focusRules.length).toBeGreaterThan(0);
  });

  function matchingRules(el: Element): FocusRule[] {
    return focusRules.filter((r) => {
      const structural = stripFocusPseudo(r.selector);
      if (!structural) return false;
      try {
        return el.matches(structural);
      } catch {
        return false;
      }
    });
  }

  function attach(html: string, targetSelector: string): Element {
    const wrapper = document.createElement('div');
    wrapper.innerHTML = html;
    document.body.appendChild(wrapper);
    const target = wrapper.querySelector(targetSelector);
    if (!target) {
      throw new Error(`focusOutline.guard: test setup could not find "${targetSelector}" in "${html}"`);
    }
    return target;
  }

  const nonInteractiveTargets: { name: string; el: () => Element }[] = [
    { name: 'generic <h1 tabindex="-1"> (stands for all 25 route headings)', el: () => attach('<h1 tabindex="-1">Title</h1>', 'h1') },
    {
      name: 'generic non-h1 tabindex="-1" element (e.g. a modal container or CampaignEditor\'s <h2>/<h3>)',
      el: () => attach('<div role="dialog" tabindex="-1"></div>', '[role="dialog"]'),
    },
    { name: "Unsubscribe's .headline", el: () => attach('<div class="unsub-shell"><h1 class="headline" tabindex="-1">Leave?</h1></div>', '.headline') },
    { name: "ConfirmSubscription's .confirm-shell h1", el: () => attach('<div class="confirm-shell"><h1 tabindex="-1">Confirmed</h1></div>', 'h1') },
    { name: "PreferenceCenter's .result-message", el: () => attach('<p class="result-message" tabindex="-1">Done</p>', '.result-message') },
    { name: "Login's .sub-section p.text-notice", el: () => attach('<div class="sub-section"><p class="text-notice" tabindex="-1">Sent</p></div>', 'p.text-notice') },
  ];

  // #0517 review: the exact element in the user's Safari screenshot, and a
  // non-h1 heading that receives programmatic focus. The generic <h1> above
  // cannot catch a component rule keyed on a class (e.g. `.app-title:focus`),
  // and no h1-only rule covers CampaignEditor's <h2>/<h3>.
  nonInteractiveTargets.push(
    {
      name: 'Account/Admin header title (.app-header h1.app-title)',
      el: () => attach('<div class="app-shell"><header class="app-header"><h1 class="app-title" tabindex="-1">Open Circuit SF</h1></header></div>', 'h1.app-title'),
    },
    {
      name: "CampaignEditor's h2.editor-heading",
      el: () => attach('<h2 class="editor-heading" tabindex="-1">Campaign</h2>', 'h2'),
    },
  );

  for (const { name, el } of nonInteractiveTargets) {
    it(`${name} gets no visible outline from any rule (not even a non-green one)`, () => {
      const element = el();
      const matches = matchingRules(element);
      for (const rule of matches) {
        expect(
          hasVisibleOutline(rule.decls),
          `${rule.file}: "${rule.selector}" gives ${name} a visible outline (${JSON.stringify(rule.decls)}) -- a tabindex="-1" target is outside the tab order and gets NO focus indicator, per #0517`,
        ).toBe(false);
      }
    });

    // Absence of an author outline is not enough: with no author rule at all,
    // the browser's own UA focus ring still paints on a programmatically
    // focused tabindex="-1" element (measured in Chromium 151 after keyboard
    // navigation; Safari's ring follows the macOS accent colour, which can be
    // green). A plain :focus rule -- not :focus-visible, which Safari does
    // not grant to programmatic focus -- must actively set outline: none.
    it(`${name} is matched by a plain :focus rule that sets outline: none (suppresses the browser's own ring)`, () => {
      const element = el();
      const suppressing = matchingRules(element).filter(
        (r) => /:focus\s*$/.test(r.selector) && (r.decls.outline ?? '').trim().toLowerCase() === 'none',
      );
      expect(suppressing.length, `no plain :focus { outline: none } rule matches ${name}`).toBeGreaterThan(0);
    });
  }

  const linkLikeTargets: { name: string; el: () => Element }[] = [
    { name: 'header nav link (.primary-nav a)', el: () => attach('<nav class="primary-nav"><ul><li><a href="/about">About</a></li></ul></nav>', 'a') },
    { name: 'footer link (.footer-links a)', el: () => attach('<div class="footer-links"><a href="/privacy">Privacy</a></div>', 'a') },
    { name: 'the "Skip to content" link', el: () => attach('<a class="skip-link" href="#main-content">Skip to content</a>', 'a') },
    { name: 'an in-page content link', el: () => attach('<main><p><a href="/x">a link</a></p></main>', 'a') },
    { name: '.nav-tab (Account/Admin header tab)', el: () => attach('<nav class="nav-tabs"><button type="button" class="nav-tab">Account</button></nav>', '.nav-tab') },
    { name: '.subtab (admin section tab)', el: () => attach('<nav class="subtabs"><button type="button" class="subtab">Overview</button></nav>', '.subtab') },
  ];

  for (const { name, el } of linkLikeTargets) {
    it(`${name} gets no outline at all (green or otherwise), and does get a non-outline underline indicator`, () => {
      const element = el();
      const matches = matchingRules(element);
      expect(
        matches.some((r) => hasAccentOutline(r.decls)),
        `no rule should paint ${name} with a var(--accent) outline; matches: ${JSON.stringify(matches)}`,
      ).toBe(false);
      expect(
        matches.some((r) => hasVisibleOutline(r.decls)),
        `no rule should give ${name} ANY visible outline -- #0535 wants a non-outline indicator, not just a non-green one; matches: ${JSON.stringify(matches)}`,
      ).toBe(false);
      expect(
        matches.some((r) => hasUnderline(r.decls)),
        `${name} must still show a keyboard focus indicator (WCAG 2.4.7) -- expected a matching rule to set an underline; matches: ${JSON.stringify(matches)}`,
      ).toBe(true);
    });
  }

  const stillAccentOutlineTargets: { name: string; el: () => Element }[] = [
    { name: 'an ordinary action button (e.g. Sign out, Revoke, Save)', el: () => attach('<button type="button">Sign out</button>', 'button') },
    { name: 'a <summary> disclosure toggle', el: () => attach('<details><summary>More</summary></details>', 'summary') },
  ];

  for (const { name, el } of stillAccentOutlineTargets) {
    it(`${name} keeps its --accent outline unchanged`, () => {
      const element = el();
      const matches = matchingRules(element);
      expect(
        matches.some((r) => hasAccentOutline(r.decls)),
        `expected ${name} to still match a rule painting a var(--accent) outline; matches: ${JSON.stringify(matches)}`,
      ).toBe(true);
    });
  }

  const formFieldTargets: { name: string; el: () => Element }[] = [
    { name: 'a text input', el: () => attach('<input type="text" />', 'input') },
    { name: 'a select', el: () => attach('<select><option>a</option></select>', 'select') },
    { name: 'a textarea', el: () => attach('<textarea></textarea>', 'textarea') },
  ];

  for (const { name, el } of formFieldTargets) {
    it(`${name} keeps its current --accent focus ring (border-color/box-shadow -- unrelated to the outline rules above)`, () => {
      const element = el();
      const matches = matchingRules(element);
      expect(
        matches.some((r) => hasAccentFocusRing(r.decls)),
        `expected ${name} to still match a rule with a var(--accent) border-color or box-shadow; matches: ${JSON.stringify(matches)}`,
      ).toBe(true);
    });
  }

  // #0517 criterion 3: the focus MOVE itself must still land -- outline:none
  // must not accidentally make these targets unfocusable. This is a real
  // DOM-behaviour assertion, unlike appNavigationFocus.structuralGuard
  // .test.ts, which checks only the shape of the .focus() call's arguments
  // in App.svelte's source, not that calling it actually works.
  it('programmatic focus() still lands on a tabindex="-1" heading despite outline:none', () => {
    const h1 = document.createElement('h1');
    h1.setAttribute('tabindex', '-1');
    document.body.appendChild(h1);
    h1.focus({ preventScroll: true });
    expect(document.activeElement).toBe(h1);
  });

  it('programmatic focus() still lands on each of the four named in-page targets', () => {
    const cases: { name: string; el: () => Element }[] = [
      { name: 'Unsubscribe .headline', el: () => attach('<div class="unsub-shell"><h1 class="headline" tabindex="-1">Leave?</h1></div>', '.headline') },
      { name: 'ConfirmSubscription .confirm-shell h1', el: () => attach('<div class="confirm-shell"><h1 tabindex="-1">Confirmed</h1></div>', 'h1') },
      { name: 'PreferenceCenter .result-message', el: () => attach('<p class="result-message" tabindex="-1">Done</p>', '.result-message') },
      { name: 'Login .sub-section p.text-notice', el: () => attach('<div class="sub-section"><p class="text-notice" tabindex="-1">Sent</p></div>', 'p.text-notice') },
    ];
    for (const { name, el } of cases) {
      const element = el() as HTMLElement;
      element.focus({ preventScroll: true });
      expect(document.activeElement, `${name} did not receive focus`).toBe(element);
    }
  });
});
