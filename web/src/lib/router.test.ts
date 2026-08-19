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
import { parsePath, shouldIntercept, type InterceptCandidate } from './router';

describe('parsePath', () => {
  it('parses every static PRD §5.1 / auth route to its route name', () => {
    const cases: [string, string][] = [
      ['/', 'home'],
      ['/workshops', 'workshops'],
      ['/about', 'about'],
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
    for (const [path, name] of cases) {
      expect(parsePath(path).name).toBe(name);
    }
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
