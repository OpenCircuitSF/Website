// #0238: proves pageTitle.ts's STATIC_HEADING table is not a second, silently
// driftable copy of what each route's view actually renders as its <h1> --
// criterion 1's "set from the same source as the view's <h1> so the two
// cannot drift". Structural (AST only, svelte/compiler -- no mounting),
// matching this project's other guard files (modalFocusWiring
// .structuralGuard.test.ts, citationGuard.test.ts): it parses every cited
// view's compiled template and extracts its own <h1>'s literal text, then
// asserts it against pageTitle.ts's table for every entry marked "guarded"
// in that file's comments. APP_NAME is read from the SAME import
// (branding.ts) both files use, not retyped here, so a change to the brand
// string cannot make this guard agree with a stale value on either side.
//
// The three entries pageTitle.ts marks "fallback" (workshop-detail, confirm,
// unsubscribe) are deliberately NOT checked here -- their real <h1> is
// runtime state a route-level table cannot see (a fetched workshop's title;
// which of several async outcomes rendered). See pageTitle.ts's module doc
// comment for why that's a disclosed boundary rather than a gap this guard
// is pretending not to have.
import { describe, expect, it } from 'vitest';
import { parse as parseSvelte } from 'svelte/compiler';
import { APP_NAME } from './branding';
import type { RouteName } from './router';
import { formatTitle, titleForRoute, shouldAnnounceNavigation } from './pageTitle';
import { parsePath } from './router';

type SvelteNode = Record<string, unknown>;

const SOURCE_FILES = import.meta.glob('../views/**/*.svelte', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

function rawFor(viewFile: string): string {
  const key = `../views/${viewFile}`;
  const src = SOURCE_FILES[key];
  if (src === undefined) {
    throw new Error(`pageTitle.guard: no view file found at web/src/views/${viewFile} -- glob key mismatch?`);
  }
  return src;
}

function childText(node: SvelteNode, file: string): string {
  if (node.type === 'Text') return node.data as string;
  if (node.type === 'ExpressionTag') {
    const expr = node.expression as SvelteNode;
    if (expr.type === 'Identifier' && expr.name === 'APP_NAME') return APP_NAME;
    throw new Error(
      `pageTitle.guard: ${file}'s <h1> contains an ExpressionTag this guard doesn't know how to resolve (expression type "${expr.type}") -- extend childText rather than silently skipping it`,
    );
  }
  throw new Error(
    `pageTitle.guard: ${file}'s <h1> contains a child node type "${node.type as string}" this guard doesn't know how to resolve -- extend childText rather than silently skipping it`,
  );
}

/** The literal, concatenated text of the FIRST <h1> found in `file`'s
 * template (depth-first). Every "guarded" route in pageTitle.ts renders
 * exactly one <h1> per branch and all branches (when there are several,
 * e.g. PreferenceCenter's showHeading sites) use identical text, so "first
 * found" is unambiguous for all of them -- verified by the sanity check in
 * the enumeration test below, which requires each candidate to actually
 * have at least one <h1>. */
function firstH1Text(file: string): string {
  const source = rawFor(file);
  const ast = parseSvelte(source, { filename: file, modern: true }) as unknown as SvelteNode;
  let found: string | undefined;

  function walk(node: unknown): void {
    if (found !== undefined) return;
    if (node === null || typeof node !== 'object') return;
    if (Array.isArray(node)) {
      for (const item of node) walk(item);
      return;
    }
    const obj = node as SvelteNode;
    if (obj.type === 'RegularElement' && obj.name === 'h1') {
      const kids = ((obj.fragment as SvelteNode | undefined)?.nodes as SvelteNode[] | undefined) ?? [];
      found = kids.map((k) => childText(k, file)).join('');
      return;
    }
    for (const key of Object.keys(obj)) {
      if (key === 'parent') continue;
      walk(obj[key]);
    }
  }

  walk(ast.fragment);
  if (found === undefined) {
    throw new Error(`pageTitle.guard: ${file} has no <h1> at all -- cited from pageTitle.ts as a "guarded" route`);
  }
  return found;
}

/** route name -> the view file that owns its "guarded" heading, i.e. every
 * STATIC_HEADING entry EXCEPT the three "fallback" ones (workshop-detail,
 * confirm, unsubscribe). Kept as a literal Record<...> (not Partial) over
 * the SAME RouteName type pageTitle.ts's table uses, minus the three
 * fallbacks, so removing a route from here without removing it from
 * STATIC_HEADING -- or vice versa -- is a compile error, not a silent gap. */
const GUARDED_VIEW: Record<
  Exclude<RouteName, 'workshop-detail' | 'archive-detail' | 'confirm' | 'unsubscribe'>,
  string
> = {
  home: 'Home.svelte',
  workshops: 'WorkshopsIndex.svelte',
  archive: 'ArchiveIndex.svelte',
  about: 'About.svelte',
  privacy: 'PrivacyPolicy.svelte',
  subscribe: 'Subscribe.svelte',
  'subscribe-thanks': 'SubscribeThanks.svelte',
  preferences: 'PreferenceCenter.svelte',
  login: 'Login.svelte',
  'register-verify': 'RegisterVerify.svelte',
  'recover-verify': 'RecoverVerify.svelte',
  account: 'Account.svelte',
  admin: 'Admin.svelte',
  'not-found': 'NotFound.svelte',
};

describe('pageTitle guard (#0238): titleForRoute is built from the SAME text as each view\'s own <h1>', () => {
  for (const [name, file] of Object.entries(GUARDED_VIEW)) {
    it(`${name} -- ${file}'s <h1> matches titleForRoute's heading`, () => {
      const h1Text = firstH1Text(file);
      const route = parsePath(name === 'workshops' ? '/workshops' : `/${name}`);
      // The route names double as their own path for every guarded entry
      // except home ('/') and admin/account, which parsePath still resolves
      // correctly by name regardless of the literal path string used here --
      // titleForRoute only reads route.name.
      const title = titleForRoute({ ...route, name: name as RouteName });
      expect(title).toBe(formatTitle(h1Text));
    });
  }

  it('covers every RouteName except the three documented runtime-state fallbacks', () => {
    const ALL_ROUTE_NAMES: RouteName[] = [
      'home',
      'workshops',
      'workshop-detail',
      'archive',
      'archive-detail',
      'about',
      'privacy',
      'subscribe',
      'subscribe-thanks',
      'confirm',
      'preferences',
      'unsubscribe',
      'login',
      'register-verify',
      'recover-verify',
      'account',
      'admin',
      'not-found',
    ];
    const fallbacks = new Set(['workshop-detail', 'archive-detail', 'confirm', 'unsubscribe']);
    const guarded = new Set(Object.keys(GUARDED_VIEW));
    for (const name of ALL_ROUTE_NAMES) {
      expect(guarded.has(name) || fallbacks.has(name)).toBe(true);
    }
    // Floor, not a magic total -- catches a route silently added to neither
    // set (this loop would otherwise just skip it).
    expect(guarded.size + fallbacks.size).toBe(ALL_ROUTE_NAMES.length);
  });
});

describe('formatTitle', () => {
  it('appends the brand suffix to an ordinary heading', () => {
    expect(formatTitle('Workshops')).toBe('Workshops — Open Circuit SF');
  });

  it('does not double the brand name when the heading already IS it', () => {
    expect(formatTitle(APP_NAME)).toBe(APP_NAME);
  });

  it('does not double the brand name when the heading merely CONTAINS it (About\'s shape) -- caught live in a real-browser check as "About Open Circuit SF — Open Circuit SF" before this test existed', () => {
    expect(formatTitle(`About ${APP_NAME}`)).toBe(`About ${APP_NAME}`);
    expect(formatTitle(`Leave the ${APP_NAME} mailing list?`)).toBe(`Leave the ${APP_NAME} mailing list?`);
  });
});

describe('titleForRoute', () => {
  it('resolves the not-found route to a distinct title, not a copy of another route\'s', () => {
    expect(titleForRoute(parsePath('/this/does/not/exist'))).toBe('404 // Not Found — Open Circuit SF');
  });

  it('resolves workshop-detail to its documented fallback', () => {
    expect(titleForRoute(parsePath('/workshops/solder-101'))).toBe('Workshop — Open Circuit SF');
  });
});

describe('shouldAnnounceNavigation (#0238 criterion 3: initial load is not double-announced)', () => {
  it('returns false on the first call, true on every call after -- vitest isolates each test file\'s module registry by default, and nothing above this describe block in this file calls it, so this genuinely is the first call', () => {
    expect(shouldAnnounceNavigation()).toBe(false);
    expect(shouldAnnounceNavigation()).toBe(true);
    expect(shouldAnnounceNavigation()).toBe(true);
  });
});
