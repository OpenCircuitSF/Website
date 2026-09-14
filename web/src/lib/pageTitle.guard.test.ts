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

const SOURCE_FILES = import.meta.glob('../views/*.svelte', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

// StaticPage.svelte is read via a plain raw import: #0520's delegated-h1
// shape needs its template, and a static import sidesteps glob-key quirks.
import staticPageSource from './StaticPage.svelte?raw';

// #0520: About and Privacy's <h1> text now lives in the shared content JSON
// (web/src/content/*.json) the view imports, so the guard needs those files
// to resolve an expression like {content.h1}.
const CONTENT_FILES = import.meta.glob('../content/*.json', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

function contentJSONFor(ref: string): Record<string, unknown> {
  const key = `../content/${ref}`;
  const raw = CONTENT_FILES[key];
  if (raw === undefined) {
    throw new Error(`pageTitle.guard: no content file found at web/src/content/${ref} -- glob key mismatch?`);
  }
  return JSON.parse(raw) as Record<string, unknown>;
}

/** Maps a view's default-content import identifier to its parsed content
 * file, read off the view's own `import content from '../content/xxx.json'`
 * statement -- so resolving {content.h1} follows the view's own import, not
 * a guess. Views without a content import (every other guarded route)
 * return null. */
function defaultImportFor(source: string, _file: string): Record<string, unknown> | null {
  const m = /import\s+(\w+)\s+from\s+'(\.\.\/content\/[^']+\.json)'/.exec(source);
  if (!m) return null;
  return contentJSONFor(m[2].replace(/^.*\/content\//, ''));
}

function rawFor(viewFile: string): string {
  const key = `../views/${viewFile}`;
  const src = SOURCE_FILES[key];
  if (src === undefined) {
    throw new Error(`pageTitle.guard: no view file found at web/src/views/${viewFile} -- glob key mismatch?`);
  }
  return src;
}

function childText(node: SvelteNode, file: string, content: Record<string, unknown> | null): string {
  if (node.type === 'Text') return node.data as string;
  if (node.type === 'ExpressionTag') {
    const expr = node.expression as SvelteNode;
    if (expr.type === 'Identifier' && expr.name === 'APP_NAME') return APP_NAME;
    // #0520: a view rendering its shared-source heading ({content.h1} from
    // a web/src/content/*.json default import) resolves against that JSON.
    if (expr.type === 'MemberExpression') {
      const obj = expr.object as SvelteNode;
      const prop = expr.property as SvelteNode;
      if (obj.type === 'Identifier' && prop.type === 'Identifier' && content !== null) {
        const value = content[prop.name as string];
        if (typeof value !== 'string') {
          throw new Error(`pageTitle.guard: ${file}'s <h1> expression {content.${prop.name as string}} did not resolve to a string in the view's imported content JSON`);
        }
        return value;
      }
      throw new Error(
        `pageTitle.guard: ${file}'s <h1> contains a MemberExpression this guard doesn't know how to resolve -- extend childText rather than silently skipping it`,
      );
    }
    throw new Error(
      `pageTitle.guard: ${file}'s <h1> contains an ExpressionTag this guard doesn't know how to resolve (expression type "${expr.type}") -- extend childText rather than silently skipping it`,
    );
  }
  throw new Error(
    `pageTitle.guard: ${file}'s <h1> contains a child node type "${node.type as string}" this guard doesn't know how to resolve -- extend childText rather than silently skipping it`,
  );
}

function collectNamed(node: unknown, typeName: string, name: string, out: SvelteNode[] = [], seen = new Set<unknown>()): SvelteNode[] {
  if (node === null || typeof node !== 'object') return out;
  if (seen.has(node)) return out;
  seen.add(node);
  if (Array.isArray(node)) {
    for (const item of node) collectNamed(item, typeName, name, out, seen);
    return out;
  }
  const obj = node as SvelteNode;
  if (obj.type === typeName && obj.name === name) out.push(obj);
  for (const key of Object.keys(obj)) {
    if (key === 'parent') continue;
    collectNamed(obj[key], typeName, name, out, seen);
  }
  return out;
}

/** The literal, concatenated text of the guarded route's <h1>. For every
 * view that renders its own <h1>, that is the first <h1> in the file's
 * template (depth-first); every "guarded" route used to render exactly one
 * <h1> per branch, with all branches using identical text.
 *
 * #0520 added the one sanctioned exception: About and Privacy render their
 * <h1> through StaticPage.svelte from the content JSON they import. The
 * guard follows the view's own wiring rather than trusting it: the view
 * must bind its imported JSON to <StaticPage content=...>, and
 * StaticPage.svelte's own <h1> must be a member expression over that same
 * prop -- then the JSON's h1 IS the view's h1, by construction, and
 * titleForRoute (which reads the same JSON) cannot drift from either. */
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
      found = kids.map((k) => childText(k, file, defaultImportFor(source, file))).join('');
      return;
    }
    for (const key of Object.keys(obj)) {
      if (key === 'parent') continue;
      walk(obj[key]);
    }
  }

  walk(ast.fragment);
  if (found !== undefined) return found;

  // No literal <h1> in the view: the only sanctioned shape is delegation to
  // StaticPage with the imported content bound to its `content` prop.
  const staticPageUses = collectNamed(ast.fragment, 'Component', 'StaticPage');
  if (staticPageUses.length !== 1) {
    throw new Error(`pageTitle.guard: ${file} has no <h1> and no single <StaticPage> -- cited from pageTitle.ts as a "guarded" route`);
  }
  const attrs = (staticPageUses[0].attributes as SvelteNode[] | undefined) ?? [];
  const contentAttr = attrs.find((a) => a.type === 'Attribute' && a.name === 'content');
  // Shorthand ({content}) yields the ExpressionTag directly; a full
  // content={...} binding yields a one-element array of it.
  const attrValue = contentAttr?.value as SvelteNode | SvelteNode[] | undefined;
  const exprTag: SvelteNode | undefined = Array.isArray(attrValue) ? (attrValue[0] as SvelteNode | undefined) : (attrValue as SvelteNode | undefined);
  if (exprTag?.type !== 'ExpressionTag' || (exprTag.expression as SvelteNode | undefined)?.type !== 'Identifier') {
    throw new Error(`pageTitle.guard: ${file}'s <StaticPage content=...> is not bound to an identifier -- extend this guard rather than silently skipping it`);
  }
  const viewContent = defaultImportFor(source, file);
  if (!viewContent) {
    throw new Error(`pageTitle.guard: ${file} passes <StaticPage content=...> but imports no content JSON -- extend defaultImportFor rather than silently skipping it`);
  }

  const staticPageAst = parseSvelte(staticPageSource, { filename: 'StaticPage.svelte', modern: true }) as unknown as SvelteNode;
  let h1: string | undefined;
  function walkStaticPage(node: unknown): void {
    if (h1 !== undefined) return;
    if (node === null || typeof node !== 'object') return;
    if (Array.isArray(node)) {
      for (const item of node) walkStaticPage(item);
      return;
    }
    const obj = node as SvelteNode;
    if (obj.type === 'RegularElement' && obj.name === 'h1') {
      const kids = ((obj.fragment as SvelteNode | undefined)?.nodes as SvelteNode[] | undefined) ?? [];
      h1 = kids.map((k) => childText(k, 'StaticPage.svelte', viewContent)).join('');
      return;
    }
    for (const key of Object.keys(obj)) {
      if (key === 'parent') continue;
      walkStaticPage(obj[key]);
    }
  }
  walkStaticPage(staticPageAst.fragment);
  if (h1 === undefined) {
    throw new Error('pageTitle.guard: StaticPage.svelte has no <h1> -- a view delegating its heading to it no longer renders one');
  }
  return h1;
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
