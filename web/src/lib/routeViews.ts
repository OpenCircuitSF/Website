// #0717: route-level code splitting. Every dynamic import() of a view or a
// topic demo goes through this module, so there is one cache, one recovery
// path, and one place to test.
//
// Recovery is a full reload of the same URL (#0715's reloadToSameURL), not a
// retry: a browser caches a failed module fetch for the life of the document,
// so re-importing the same URL fails again. The typical failure is a stale tab
// after a deploy, asking for a chunk hash that no longer exists (the server
// 404s it -- internal/handlers/static.go). A sessionStorage key per URL makes
// the reload happen at most once, so a chunk that is genuinely broken cannot
// loop.
//
// No vite:preloadError listener: in Vite 6's __vitePreload, a failed
// dependency preload or a failed chunk import dispatches that event and then
// rethrows unless preventDefault() is called. Every import() here runs
// through importOnce, so the rejection always reaches loadComponent's
// handler; a listener would run the same recovery twice for one failure.
import type { Component } from 'svelte';
import type { Route, RouteName } from './router';
import { TOPIC_DEMOS } from './topics/demoLoaders';
import { reloadToSameURL } from './versionCheck';

export type ViewModule = { default: Component<any> };
export type Loader = () => Promise<ViewModule>;

export const LAZY_ROUTE_LOADERS: Readonly<Partial<Record<RouteName, Loader>>> = {
  login: () => import('../views/Login.svelte'),
  account: () => import('../views/Account.svelte'),
  admin: () => import('../views/Admin.svelte'),
  'register-verify': () => import('../views/RegisterVerify.svelte'),
  'recover-verify': () => import('../views/RecoverVerify.svelte'),
  'workshop-guide': () => import('../views/GuideProgrammingLeds.svelte'),
  'topic-guide': () => import('../views/TopicGuide.svelte'),
  confirm: () => import('../views/ConfirmSubscription.svelte'),
  preferences: () => import('../views/PreferenceCenter.svelte'),
  unsubscribe: () => import('../views/Unsubscribe.svelte'),
};

const DEMO_PREFIX = 'demo:';
export const demoKey = (slug: string): string => `${DEMO_PREFIX}${slug}`;
export const CHUNK_RELOAD_KEY_PREFIX = 'oc.chunkReload:';

export interface RecoveryDeps {
  href(): string;
  getSession(): Storage | null;
  reload(): void;
}

export const defaultDeps: RecoveryDeps = {
  href: () => location.href,
  getSession: () => sessionStorage,
  reload: () => reloadToSameURL(),
};

const cache = new Map<string, Component<any>>();
const inflight = new Map<string, Promise<ViewModule>>();

function loaderFor(key: string): Loader | undefined {
  if (key.startsWith(DEMO_PREFIX)) return TOPIC_DEMOS[key.slice(DEMO_PREFIX.length)];
  return LAZY_ROUTE_LOADERS[key as RouteName];
}

function importOnce(key: string, loader?: Loader): Promise<ViewModule> {
  const existing = inflight.get(key);
  if (existing) return existing;
  const load = loader ?? loaderFor(key);
  if (!load) return Promise.reject(new Error(`no lazy loader for ${key}`));
  const p = load().catch((err) => {
    inflight.delete(key);
    throw err;
  });
  inflight.set(key, p);
  return p;
}

export function cachedComponent(key: string): Component<any> | null {
  return cache.get(key) ?? null;
}

/** Loads a view; on failure starts the once-per-URL reload and resolves null. */
export function loadComponent(
  key: string,
  deps: RecoveryDeps = defaultDeps,
  loader?: Loader,
): Promise<Component<any> | null> {
  const hit = cache.get(key);
  if (hit) return Promise.resolve(hit);
  return importOnce(key, loader).then(
    (mod) => {
      cache.set(key, mod.default);
      clearChunkRecovery(deps);
      return mod.default;
    },
    () => {
      recoverFromChunkError(deps);
      return null;
    },
  );
}

/** Warms the cache; a failure is swallowed, with no reload and nothing cached. */
export function prefetchComponent(key: string, loader?: Loader): Promise<void> {
  if (cache.has(key)) return Promise.resolve();
  return importOnce(key, loader).then(
    (mod) => {
      cache.set(key, mod.default);
    },
    () => undefined,
  );
}

function recoveryKey(deps: RecoveryDeps): string {
  return CHUNK_RELOAD_KEY_PREFIX + deps.href();
}

/** Reloads the same URL once; false when already tried or storage is unavailable. */
export function recoverFromChunkError(deps: RecoveryDeps = defaultDeps): boolean {
  let store: Storage | null;
  try {
    store = deps.getSession();
    if (!store) return false;
    const k = recoveryKey(deps);
    if (store.getItem(k) !== null) return false;
    store.setItem(k, String(Date.now()));
  } catch {
    return false;
  }
  deps.reload();
  return true;
}

export function clearChunkRecovery(deps: RecoveryDeps = defaultDeps): void {
  try {
    deps.getSession()?.removeItem(recoveryKey(deps));
  } catch {
    // storage unavailable: nothing to clear
  }
}

export function lazyKeyForRoute(route: Route): string | null {
  return LAZY_ROUTE_LOADERS[route.name] ? route.name : null;
}

function topicDemoKey(route: Route): string | null {
  const slug = route.params?.slug;
  return route.name === 'topic-guide' && slug && TOPIC_DEMOS[slug] ? demoKey(slug) : null;
}

/** Loads the route's view and (for a topic guide) its demo; never rejects. */
export function preloadForRoute(route: Route): Promise<void> {
  const keys = [lazyKeyForRoute(route), topicDemoKey(route)].filter((k): k is string => k !== null);
  return Promise.all(keys.map((k) => loadComponent(k))).then(() => undefined);
}

export function prefetchForRoute(route: Route): void {
  for (const k of [lazyKeyForRoute(route), topicDemoKey(route)]) {
    if (k) void prefetchComponent(k);
  }
}

/** Resolves once the route's own view (and so its <h1>) can render. */
export function routeViewReady(route: Route): Promise<void> {
  const key = lazyKeyForRoute(route);
  return key ? loadComponent(key).then(() => undefined) : Promise.resolve();
}

/** Test-only: forget every cached and in-flight import. */
export function __resetForTest(): void {
  cache.clear();
  inflight.clear();
}
