// @vitest-environment jsdom
//
// #0614: an old /workshops path on the client. jsdom is not a browser; the
// real-browser evidence is scripts/browser_nav_guard_test.sh.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';

async function freshRouter() {
  vi.resetModules();
  return import('./router');
}

describe('old workshop paths on the client', () => {
  beforeEach(() => {
    window.scrollTo = vi.fn() as unknown as typeof window.scrollTo;
    window.history.replaceState(null, '', '/');
  });
  afterEach(() => window.history.replaceState(null, '', '/'));

  it('navigate to an old path lands on the new one with its query', async () => {
    const { navigate, currentRoute } = await freshRouter();
    navigate('/workshops/solder?utm_source=x');
    expect(window.location.pathname + window.location.search).toBe('/events/solder?utm_source=x');
    expect(get(currentRoute).name).toBe('workshop-detail');
    expect(get(currentRoute).params.slug).toBe('solder');

    navigate('/workshops?utm_source=x');
    expect(window.location.pathname + window.location.search).toBe('/events?type=workshop&utm_source=x');
    expect(get(currentRoute).query.get('type')).toBe('workshop');
  });

  it('initRouter rewrites an old location without adding a history entry', async () => {
    window.history.replaceState(null, '', '/workshops/solder?utm_source=x');
    const { initRouter, currentRoute } = await freshRouter();
    expect(get(currentRoute).name).toBe('workshop-detail');
    const before = window.history.length;
    initRouter();
    expect(window.location.pathname + window.location.search).toBe('/events/solder?utm_source=x');
    expect(window.history.length).toBe(before);
  });
});
