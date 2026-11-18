// #0717: lazy view loading and the once-per-URL stale-chunk recovery.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  CHUNK_RELOAD_KEY_PREFIX,
  LAZY_ROUTE_LOADERS,
  __resetForTest,
  cachedComponent,
  loadComponent,
  prefetchComponent,
  type Loader,
  type RecoveryDeps,
} from './routeViews';
import { TOPIC_DEMOS } from './topics/demoLoaders';
import { TOPIC_GUIDES } from './guides';

function memStorage(): Storage {
  const m = new Map<string, string>();
  return {
    get length() {
      return m.size;
    },
    clear: () => m.clear(),
    getItem: (k) => m.get(k) ?? null,
    key: (i) => [...m.keys()][i] ?? null,
    removeItem: (k) => void m.delete(k),
    setItem: (k, v) => void m.set(k, String(v)),
  };
}

const HREF = 'https://www.example.test/guides/baud-rate';
const KEY = CHUNK_RELOAD_KEY_PREFIX + HREF;
let store: Storage;
let deps: RecoveryDeps & { reload: ReturnType<typeof vi.fn<() => void>> };
const Fake = (() => {}) as unknown as never;
const ok: Loader = () => Promise.resolve({ default: Fake });
const fail: Loader = () => Promise.reject(new TypeError('Failed to fetch dynamically imported module'));

beforeEach(() => {
  __resetForTest();
  store = memStorage();
  deps = { href: () => HREF, getSession: () => store, reload: vi.fn<() => void>() };
});

describe('routeViews', () => {
  it('caches a loaded view and returns it synchronously afterwards', async () => {
    expect(cachedComponent('x')).toBeNull();
    expect(await loadComponent('x', deps, ok)).toBe(Fake);
    expect(cachedComponent('x')).toBe(Fake);
  });

  it('on a failed chunk import, reloads the same URL once and records it', async () => {
    expect(await loadComponent('x', deps, fail)).toBeNull();
    expect(deps.reload).toHaveBeenCalledTimes(1);
    expect(store.getItem(KEY)).not.toBeNull();
  });

  it('does not reload a second time for the same URL', async () => {
    store.setItem(KEY, '1');
    expect(await loadComponent('x', deps, fail)).toBeNull();
    expect(deps.reload).not.toHaveBeenCalled();
  });

  it('clears the guard after a successful load so a later deploy can recover again', async () => {
    store.setItem(KEY, '1');
    await loadComponent('x', deps, ok);
    expect(store.getItem(KEY)).toBeNull();
  });

  it('a view that loads while its sibling chunk fails keeps the guard, so the reload cannot loop', async () => {
    // A topic guide's view and demo load in parallel (preloadForRoute). After
    // the one recovery reload the guard is set; the view succeeding must not
    // clear it while the demo is still in flight, or a demo chunk that fails
    // every time would reload the page forever.
    store.setItem(KEY, '1');
    const late: Loader = () => new Promise((_, rej) => setTimeout(() => rej(new TypeError('404')), 5));
    await Promise.all([loadComponent('view', deps, ok), loadComponent('demo', deps, late)]);
    expect(deps.reload).not.toHaveBeenCalled();
    expect(store.getItem(KEY)).not.toBeNull();
    // and in the other order: the failure settles first
    __resetForTest();
    const slowOk: Loader = () => new Promise((r) => setTimeout(() => r({ default: Fake }), 5));
    await Promise.all([loadComponent('view', deps, slowOk), loadComponent('demo', deps, fail)]);
    expect(deps.reload).not.toHaveBeenCalled();
    expect(store.getItem(KEY)).not.toBeNull();
  });

  it('does not reload when sessionStorage is unavailable', async () => {
    const throwing = { ...deps, getSession: () => { throw new Error('denied'); } };
    expect(await loadComponent('x', throwing, fail)).toBeNull();
    const absent = { ...deps, getSession: () => null };
    expect(await loadComponent('y', absent, fail)).toBeNull();
    expect(deps.reload).not.toHaveBeenCalled();
  });

  it('a failed prefetch neither reloads nor caches, and a later real load still recovers', async () => {
    await prefetchComponent('x', fail);
    expect(deps.reload).not.toHaveBeenCalled();
    expect(cachedComponent('x')).toBeNull();
    expect(await loadComponent('x', deps, fail)).toBeNull();
    expect(deps.reload).toHaveBeenCalledTimes(1);
  });

  it('every lazy route loader resolves to a component', async () => {
    for (const [name, load] of Object.entries(LAZY_ROUTE_LOADERS)) {
      expect(typeof (await load!()).default, name).toBe('function');
    }
  });

  it('every catalog topic has a demo loader and every loader a catalog topic', () => {
    expect(Object.keys(TOPIC_DEMOS).sort()).toEqual(TOPIC_GUIDES.map((t) => t.slug).sort());
  });
});
