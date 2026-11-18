// @vitest-environment jsdom
// #0717: a reload must return the visitor to the same page. With route-level
// code splitting, each URL below is loaded fresh (new modules, as a reload
// would) and must render its own route -- never home, never not-found -- with
// the address bar unchanged.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { TOPIC_GUIDES, WORKSHOP_GUIDES } from './guides';
import { ADMIN_NAV_SECTIONS, ADMIN_SECTION_LABELS, adminSectionHash } from './adminRouter';
import { LAZY_ROUTE_LOADERS } from './routeViews';
import { APP_NAME } from './branding';
import about from '../content/about.json';
import privacy from '../content/privacy.json';
import appSource from '../App.svelte?raw';

vi.mock('./api', async (orig) => ({
  ...(await orig<typeof import('./api')>()),
  getMe: () => Promise.resolve({ id: 1, email: 'a@b.c', is_admin: true }),
}));

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(() => new Promise(() => {})));
  window.matchMedia ??= ((query: string) =>
    ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    })) as unknown as typeof window.matchMedia;
  if (!('IntersectionObserver' in window)) {
    (window as unknown as { IntersectionObserver: unknown }).IntersectionObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
      takeRecords() {
        return [];
      }
    };
  }
  if (!('EventSource' in window)) {
    (window as unknown as { EventSource: unknown }).EventSource = class {
      addEventListener() {}
      removeEventListener() {}
      close() {}
    };
  }
});

let cleanup: () => void = () => {};
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

// Each URL with the <h1> its own view renders, so a route that comes back
// showing some other view's page fails here, not only one that changes the
// address. Login, Account and Admin share the brand <h1>, so those cases also
// name an element only their own view renders -- for admin, the subtab its
// bar marks current, since a reload that dropped the #/... hash would land on
// the overview with the address unchanged.
type Marker = { selector: string; text?: string };
const ADMIN_SECTION = ADMIN_NAV_SECTIONS[1];
const URLS: [string, string, Marker?][] = [
  ['/', 'Hands-on electronics workshops'],
  ['/about', about.h1],
  ['/guides', 'Guides'],
  [`/guides/${TOPIC_GUIDES[0].slug}`, TOPIC_GUIDES[0].title],
  [`/guides/${TOPIC_GUIDES[TOPIC_GUIDES.length - 1].slug}`, TOPIC_GUIDES[TOPIC_GUIDES.length - 1].title],
  [`/workshops/${WORKSHOP_GUIDES[0].slug}`, WORKSHOP_GUIDES[0].title],
  ['/login', APP_NAME, { selector: 'main.login-shell' }],
  ['/privacy', privacy.h1],
  [
    '/admin' + adminSectionHash(ADMIN_SECTION),
    APP_NAME,
    { selector: '[aria-current="page"]', text: ADMIN_SECTION_LABELS[ADMIN_SECTION] },
  ],
];

describe('reload returns to the same page (#0717)', () => {
  it.each(URLS)('reload on %s stays on that route', async (url, h1, marker) => {
    history.replaceState(null, '', url);
    vi.resetModules();
    // Fresh copies of everything, Svelte's runtime included, as a reload gets.
    const tl = await import('@testing-library/svelte');
    const { get } = await import('svelte/store');
    const { render, screen, waitFor } = tl;
    cleanup = tl.cleanup;
    const { default: App } = await import('../App.svelte');
    const router = await import('./router');
    const expected = router.parsePath(location.pathname, location.search).name;
    // As main.ts does: the route's chunks are in before App mounts. Awaiting
    // it here, outside waitFor, keeps the cold transform of a large view
    // (Admin) off waitFor's default deadline.
    const { preloadForRoute } = await import('./routeViews');
    await preloadForRoute(get(router.currentRoute));
    render(App);
    await waitFor(() => {
      expect(get(router.currentRoute).name).toBe(expected);
      expect(screen.queryAllByText('Loading…').filter((el) => el.closest('.next-up') === null && el.parentElement?.classList.contains('app-shell'))).toHaveLength(0);
      expect([...document.querySelectorAll('h1')].map((el) => el.textContent?.trim())).toEqual([h1]);
      if (marker) {
        const el = document.querySelector(marker.selector);
        expect(el, marker.selector).not.toBeNull();
        if (marker.text !== undefined) expect(el?.textContent?.trim()).toBe(marker.text);
      }
    });
    expect(location.pathname + location.search + location.hash).toBe(url);
    expect(expected).not.toBe('not-found');
    if (url !== '/') expect(expected).not.toBe('home');
  });

  it('every RouteName is rendered by App eagerly or has a lazy loader', () => {
    for (const name of Object.keys(LAZY_ROUTE_LOADERS)) expect(appSource, name).toContain(`key="${name}"`);
  });
});
