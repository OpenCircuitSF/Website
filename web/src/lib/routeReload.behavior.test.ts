// @vitest-environment jsdom
// #0717: a reload must return the visitor to the same page. With route-level
// code splitting, each URL below is loaded fresh (new modules, as a reload
// would) and must render its own route -- never home, never not-found -- with
// the address bar unchanged.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { TOPIC_GUIDES, WORKSHOP_GUIDES } from './guides';
import { ADMIN_NAV_SECTIONS, adminSectionHash } from './adminRouter';
import { LAZY_ROUTE_LOADERS } from './routeViews';
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

const URLS = [
  '/',
  '/about',
  '/guides',
  `/guides/${TOPIC_GUIDES[0].slug}`,
  `/guides/${TOPIC_GUIDES[TOPIC_GUIDES.length - 1].slug}`,
  `/workshops/${WORKSHOP_GUIDES[0].slug}`,
  '/login',
  '/privacy',
  '/admin' + adminSectionHash(ADMIN_NAV_SECTIONS[1]),
];

describe('reload returns to the same page (#0717)', () => {
  it.each(URLS)('reload on %s stays on that route', async (url) => {
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
    render(App);
    await waitFor(
      () => {
        expect(get(router.currentRoute).name).toBe(expected);
        expect(screen.queryAllByText('Loading…').filter((el) => el.closest('.next-up') === null && el.parentElement?.classList.contains('app-shell'))).toHaveLength(0);
        expect(document.querySelector('h1')).not.toBeNull();
      },
      { timeout: 5000 },
    );
    expect(location.pathname + location.search + location.hash).toBe(url);
    expect(expected).not.toBe('not-found');
    if (url !== '/') expect(expected).not.toBe('home');
  });

  it('every RouteName is rendered by App eagerly or has a lazy loader', () => {
    for (const name of Object.keys(LAZY_ROUTE_LOADERS)) expect(appSource, name).toContain(`key="${name}"`);
  });
});
