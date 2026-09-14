// @vitest-environment jsdom
// #0521: App.svelte used to render its "Loading…" placeholder on EVERY route
// until GET /api/me settled, so a slow page load showed the server's #0519
// fallback content, then a Loading flash, then the real view. After #0521
// only the session-gated routes (router.ts's SESSION_GATED_ROUTES) wait for
// /api/me; every public route renders its real view on mount. These tests
// mount the real App and prove both halves.
//
// jsdom caveats handled below: Home.svelte's CRT hero needs matchMedia,
// IntersectionObserver, and requestAnimationFrame (all absent in jsdom), and
// every view's API call must not hit a real network -- global fetch is
// stubbed to reject, which every caller already treats as its error state.
import { render, screen, waitFor, cleanup } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import * as api from './api';
import { currentRoute, parsePath } from './router';
import type { User } from './types';
import App from '../App.svelte';

vi.stubGlobal(
  'fetch',
  vi.fn(() => Promise.reject(new Error('offline -- network stubbed for this test'))),
);

beforeEach(() => {
  if (!window.matchMedia) {
    window.matchMedia = ((query: string) =>
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
  }
  if (!('IntersectionObserver' in window)) {
    class IO {
      observe() {}
      unobserve() {}
      disconnect() {}
      takeRecords() {
        return [];
      }
    }
    (window as unknown as { IntersectionObserver: unknown }).IntersectionObserver = IO;
  }
  if (!('requestAnimationFrame' in window)) {
    (window as unknown as { requestAnimationFrame: (cb: (t: number) => void) => number }).requestAnimationFrame = (cb: FrameRequestCallback) =>
      setTimeout(() => cb(Date.now()), 16);
  }
  if (!('EventSource' in window)) {
    // Admin's dashboard subscribes to the SSE stream; jsdom has no
    // EventSource. A never-connecting stub is enough for the gate tests.
    class ES {
      addEventListener() {}
      removeEventListener() {}
      close() {}
    }
    (window as unknown as { EventSource: unknown }).EventSource = ES;
  }
  // jsdom cannot inspect the URL from a replaceState alone: the route store
  // is what App.svelte reads, so each test sets it through the store.
  currentRoute.set(parsePath('/'));
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

// getMe never resolves: the strictest version of "the API is slow". The
// public view must still render.
const neverSettlingMe = (): Promise<never> => new Promise(() => {});

/** The App-level Loading screen (#0521's subject) is a bare .app-shell
 * paragraph at the top level. Home.svelte's own "Next up" section has a
 * legitimate, separately-scoped Loading… state for its workshops fetch --
 * that one is NOT the flash this issue removes, so assert only that no
 * App-level placeholder exists. */
function appLevelLoading(): HTMLElement[] {
  return screen.queryAllByText('Loading…').filter((el) => el.closest('.next-up') === null);
}

describe('App.svelte #0521: no Loading flash between server content and the mounted page', () => {
  it('renders the home view immediately while /api/me is still pending', async () => {
    vi.spyOn(api, 'getMe').mockImplementation(neverSettlingMe);
    render(App);
    await waitFor(() => expect(screen.getByRole('heading', { level: 1 })).toBeTruthy());
    expect(appLevelLoading()).toHaveLength(0);
  });

  it('renders the About view immediately while /api/me is still pending', async () => {
    vi.spyOn(api, 'getMe').mockImplementation(neverSettlingMe);
    currentRoute.set(parsePath('/about'));
    render(App);
    await waitFor(() => expect(screen.getByRole('heading', { level: 1, name: 'About Open Circuit SF' })).toBeTruthy());
    expect(appLevelLoading()).toHaveLength(0);
  });

  it('keeps the Loading gate on the admin route until /api/me answers', async () => {
    let resolveMe: (u: User) => void = () => {};
    vi.spyOn(api, 'getMe').mockImplementation(
      () =>
        new Promise<User>((resolve) => {
          resolveMe = resolve;
        }),
    );
    currentRoute.set(parsePath('/admin'));
    render(App);
    expect(appLevelLoading()).toHaveLength(1);

    resolveMe({ id: 1, email: 'a@b.c', is_admin: true });
    await waitFor(() => expect(appLevelLoading()).toHaveLength(0));
  });

  it('keeps exactly one <h1> at every stage on a public route', async () => {
    vi.spyOn(api, 'getMe').mockImplementation(neverSettlingMe);
    currentRoute.set(parsePath('/about'));
    render(App);
    await waitFor(() => expect(screen.getByRole('heading', { level: 1, name: 'About Open Circuit SF' })).toBeTruthy());
    // After mount: the server fallback was replaced (prepareMountTarget), so
    // the document carries exactly one h1 -- the view's own.
    expect(document.querySelectorAll('h1').length).toBe(1);
  });
});
