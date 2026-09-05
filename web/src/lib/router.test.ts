// Unit tests for the pure parts of the History API router: path parsing and
// the click-interception predicate. Neither touches the DOM, matching this
// project's DOM-avoidance test convention (see theme.test.ts, events.test.ts)
// -- there is no jsdom/happy-dom environment configured for vitest here, so a
// test that reached for `document`/`window` would simply fail to run at all.
//
// initRouter() itself (the part that wires up real click/popstate listeners)
// is NOT exercised here -- it has no pure-logic surface left once
// shouldIntercept/parsePath/navigate are factored out, and is verified by
// rendering (dev server) instead. See the implementation subagent's report
// for exactly what "verified by rendering" vs "not verified at all" covers.

import { describe, expect, it } from 'vitest';
import { parsePath, shouldIntercept, STATIC_ROUTES, type InterceptCandidate } from './router';

// Every static PRD §5.1 / auth route, paired with the route name parsePath()
// resolves it to. Hoisted out of the test body (rather than a local const
// inside `it`) so the completeness-equality test below can check it against
// STATIC_ROUTES without duplicating it a second time.
//
// #0440: this table is the ninth independent account of the static route set
// to drift out of agreement with production since #0123 added `/archive` --
// #0425 corrected seven prose/doc-comment accounts and #0437 corrected four
// Go test fixtures, one of which (TestIsKnownRoute_StaticRoutes) is already
// cross-checked against router.ts's STATIC_ROUTES by
// TestRouteTableParity_StaticRoutes (internal/handlers/routes_parity_test.go,
// #0071) -- but that check only catches drift between the Go and TypeScript
// route *tables*, not between this array and router.ts's own table. The
// second test below closes that remaining gap: it fails if this array is
// ever missing an entry STATIC_ROUTES has, holds an entry STATIC_ROUTES
// doesn't, or pairs a path with the wrong name -- so the "every static
// route" claim in the first test's description is enforced, not just
// written.
const staticRouteCases: [string, string][] = [
  ['/', 'home'],
  ['/workshops', 'workshops'],
  ['/archive', 'archive'],
  ['/about', 'about'],
  ['/privacy', 'privacy'],
  ['/subscribe', 'subscribe'],
  ['/subscribe/thanks', 'subscribe-thanks'],
  ['/confirm', 'confirm'],
  ['/preferences', 'preferences'],
  ['/unsubscribe', 'unsubscribe'],
  ['/login', 'login'],
  ['/register/verify', 'register-verify'],
  ['/recover/verify', 'recover-verify'],
  ['/account', 'account'],
  ['/admin', 'admin'],
];

describe('parsePath', () => {
  it('parses every static PRD §5.1 / auth route to its route name', () => {
    for (const [path, name] of staticRouteCases) {
      expect(parsePath(path).name).toBe(name);
    }
  });

  it("covers exactly router.ts's own STATIC_ROUTES table -- no more, no less", () => {
    // Set equality on [path, name] pairs: catches a route added to
    // STATIC_ROUTES and not mirrored here, a route removed from
    // STATIC_ROUTES that this array still asserts, and a path paired with
    // the wrong name on either side. Sorted so the comparison doesn't
    // depend on declaration order.
    const expected = Object.entries(STATIC_ROUTES).sort();
    const actual = [...staticRouteCases].sort();
    expect(actual).toEqual(expected);
  });

  it('extracts the :slug path parameter for /workshops/:slug', () => {
    const route = parsePath('/workshops/solder-101');
    expect(route.name).toBe('workshop-detail');
    expect(route.params).toEqual({ slug: 'solder-101' });
  });

  it('decodes a URL-encoded slug', () => {
    const route = parsePath('/workshops/kicad%20night');
    expect(route.params.slug).toBe('kicad night');
  });

  it('does not treat a two-segment workshops path as the index route', () => {
    // Proves the dynamic pattern actually bites: without it, /workshops/x
    // would fall through to not-found rather than resolving a slug.
    expect(parsePath('/workshops/x').name).toBe('workshop-detail');
  });

  it('falls through to not-found for an unmatched path', () => {
    const route = parsePath('/this/does/not/exist');
    expect(route.name).toBe('not-found');
    expect(route.path).toBe('/this/does/not/exist');
  });

  it('normalizes a trailing slash on a multi-segment path', () => {
    expect(parsePath('/about/').name).toBe('about');
    expect(parsePath('/about/').path).toBe('/about');
  });

  it('leaves the root path "/" alone rather than stripping it to empty', () => {
    expect(parsePath('/').path).toBe('/');
  });

  it('parses and exposes query parameters', () => {
    const route = parsePath('/confirm', '?token=abc123');
    expect(route.query.get('token')).toBe('abc123');
  });

  it('exposes multiple query parameters', () => {
    const route = parsePath('/preferences', '?token=xyz&source=email');
    expect(route.query.get('token')).toBe('xyz');
    expect(route.query.get('source')).toBe('email');
  });

  it('returns an empty (not null) query when there is no search string', () => {
    const route = parsePath('/');
    expect(route.query.get('token')).toBeNull();
    expect([...route.query.keys()]).toHaveLength(0);
  });
});

describe('shouldIntercept', () => {
  function candidate(overrides: Partial<InterceptCandidate> = {}): InterceptCandidate {
    return {
      button: 0,
      metaKey: false,
      ctrlKey: false,
      shiftKey: false,
      altKey: false,
      defaultPrevented: false,
      target: null,
      download: null,
      href: 'https://opencircuitsf.com/about',
      currentOrigin: 'https://opencircuitsf.com',
      ...overrides,
    };
  }

  it('intercepts a plain same-origin left click', () => {
    expect(shouldIntercept(candidate())).toBe(true);
  });

  it('intercepts an explicit target="_self"', () => {
    expect(shouldIntercept(candidate({ target: '_self' }))).toBe(true);
  });

  it('does not intercept a middle click', () => {
    expect(shouldIntercept(candidate({ button: 1 }))).toBe(false);
  });

  it('does not intercept a right click', () => {
    expect(shouldIntercept(candidate({ button: 2 }))).toBe(false);
  });

  it('does not intercept when a modifier key is held', () => {
    expect(shouldIntercept(candidate({ metaKey: true }))).toBe(false);
    expect(shouldIntercept(candidate({ ctrlKey: true }))).toBe(false);
    expect(shouldIntercept(candidate({ shiftKey: true }))).toBe(false);
    expect(shouldIntercept(candidate({ altKey: true }))).toBe(false);
  });

  it('does not intercept target="_blank"', () => {
    expect(shouldIntercept(candidate({ target: '_blank' }))).toBe(false);
  });

  it('does not intercept a download link', () => {
    expect(shouldIntercept(candidate({ download: '' }))).toBe(false);
    expect(shouldIntercept(candidate({ download: 'report.pdf' }))).toBe(false);
  });

  it('does not intercept a cross-origin href', () => {
    expect(shouldIntercept(candidate({ href: 'https://discord.gg/Fq9ug6QXV3' }))).toBe(false);
  });

  it('does not intercept the Luma calendar link', () => {
    expect(shouldIntercept(candidate({ href: 'https://luma.com/opencircuitsf' }))).toBe(false);
  });

  it('does not intercept a mailto: link', () => {
    expect(shouldIntercept(candidate({ href: 'mailto:hello@opencircuitsf.com' }))).toBe(false);
  });

  it('does not intercept a tel: link', () => {
    expect(shouldIntercept(candidate({ href: 'tel:+14155551212' }))).toBe(false);
  });

  it('does not intercept a click some other handler already prevented', () => {
    expect(shouldIntercept(candidate({ defaultPrevented: true }))).toBe(false);
  });

  it('intercepts a relative href resolved against the current origin', () => {
    expect(
      shouldIntercept(
        candidate({ href: 'https://opencircuitsf.com/workshops/solder-101' }),
      ),
    ).toBe(true);
  });
});
