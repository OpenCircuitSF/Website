// versionCheck — notice that a newer build was deployed while this tab sat
// open, and move onto it (#0715).
//
// The server stamps its build id into <meta name="oc-build"> and answers
// GET /api/version with the same id. When the page becomes visible (or is
// restored from the back/forward cache), at most once an hour, the two are
// compared; on a mismatch the tab reloads in place, or — when work is
// unsaved — the caller is asked to show a "Reload" notice instead.
//
// No service worker, no polling interval, no external request (PRD §7.6).

import { hasUnsavedWork as registryHasUnsaved, isEditingField } from './unsavedWork';

export const CHECK_INTERVAL_MS = 3_600_000;
export const RETRY_AFTER_FAILURE_MS = 300_000;
export const LAST_CHECK_KEY = 'oc.versionCheck.lastAt';
export const RELOAD_SCROLL_KEY = 'oc.reload.scroll';

export function readRunningBuild(doc: Pick<Document, 'querySelector'> = document): string | null {
  const meta = doc.querySelector('meta[name="oc-build"]');
  const content = meta?.getAttribute('content') ?? '';
  // An unsubstituted token (Vite dev) means "unknown": the feature is off.
  if (content === '' || content.startsWith('%%')) return null;
  return content;
}

export interface ThrottleStore {
  get(): number | null;
  set(ms: number): void;
}

export function createThrottleStore(
  getStorage: () => Storage | null = () => window.localStorage,
): ThrottleStore {
  let memory: number | null = null;
  return {
    get() {
      try {
        const raw = getStorage()?.getItem(LAST_CHECK_KEY);
        if (raw != null) {
          const n = Number(raw);
          if (Number.isFinite(n)) return n;
        }
      } catch {
        // storage blocked: fall through to memory
      }
      return memory;
    },
    set(ms: number) {
      memory = ms;
      try {
        getStorage()?.setItem(LAST_CHECK_KEY, String(ms));
      } catch {
        // storage blocked: memory still holds it
      }
    },
  };
}

export function shouldCheck(now: number, last: number | null): boolean {
  if (last === null) return true;
  if (last > now) return true; // clock moved backward
  return now - last >= CHECK_INTERVAL_MS;
}

export async function fetchServerBuild(fetchFn: typeof fetch = fetch): Promise<string | null> {
  try {
    const res = await fetchFn('/api/version', {
      cache: 'no-store',
      credentials: 'same-origin',
      headers: { Accept: 'application/json' },
    });
    if (!res.ok) return null;
    const body = (await res.json()) as { build?: unknown };
    return typeof body?.build === 'string' && body.build !== '' ? body.build : null;
  } catch {
    return null;
  }
}

export type Decision = 'skip' | 'same' | 'reload' | 'notify' | 'failed';

export interface CheckDeps {
  now(): number;
  store: ThrottleStore;
  runningBuild: string | null;
  fetchBuild(): Promise<string | null>;
  hasUnsavedWork(): boolean;
}

export async function checkForNewVersion(deps: CheckDeps): Promise<Decision> {
  if (deps.runningBuild === null) return 'skip';
  const now = deps.now();
  if (!shouldCheck(now, deps.store.get())) return 'skip';
  deps.store.set(now); // before the fetch, so two near-simultaneous events cannot both fetch
  const server = await deps.fetchBuild();
  if (server === null) {
    // Allow the next try in five minutes rather than a full hour.
    deps.store.set(now - CHECK_INTERVAL_MS + RETRY_AFTER_FAILURE_MS);
    return 'failed';
  }
  if (server === deps.runningBuild) return 'same';
  return deps.hasUnsavedWork() ? 'notify' : 'reload';
}

/**
 * Reload this tab onto the newest build, on the same page.
 *
 * Calls location.reload() — never assign/replace/href — so path, query and
 * hash (admin `#/…` routes included) are kept; the scroll offset is saved for
 * restoreScrollAfterReload. This is the reload #0717 reuses for stale-chunk
 * recovery.
 */
export function reloadToSameURL(
  loc: Pick<Location, 'href' | 'reload'> = window.location,
  getSession: () => Storage | null = () => window.sessionStorage,
  scrollY: number = window.scrollY,
): void {
  try {
    getSession()?.setItem(RELOAD_SCROLL_KEY, JSON.stringify({ href: loc.href, y: scrollY }));
  } catch {
    // best effort
  }
  loc.reload();
}

export function restoreScrollAfterReload(
  loc: Pick<Location, 'href'> = window.location,
  getSession: () => Storage | null = () => window.sessionStorage,
  scrollTo: (y: number) => void = (y) => window.scrollTo(0, y),
  raf: (cb: () => void) => unknown = (cb) => requestAnimationFrame(cb),
): void {
  let saved: { href?: unknown; y?: unknown } | null = null;
  try {
    const s = getSession();
    const raw = s?.getItem(RELOAD_SCROLL_KEY);
    if (raw == null) return;
    s?.removeItem(RELOAD_SCROLL_KEY);
    saved = JSON.parse(raw);
  } catch {
    return;
  }
  const y = saved?.y;
  if (saved?.href === loc.href && typeof y === 'number' && y > 0) raf(() => scrollTo(y));
}

export interface WatchOptions {
  onNotify(): void;
  win?: EventTarget;
  doc?: EventTarget & { visibilityState: string; activeElement?: Element | null };
  deps?: Partial<CheckDeps>;
  reload?: () => void;
}

/** Wire the check to visibility/pageshow; returns a cleanup function. */
export function startVersionWatch(opts: WatchOptions): () => void {
  const win = opts.win ?? window;
  const doc = opts.doc ?? document;
  const reload = opts.reload ?? (() => reloadToSameURL());
  const deps: CheckDeps = {
    now: () => Date.now(),
    store: opts.deps?.store ?? createThrottleStore(),
    runningBuild: opts.deps?.runningBuild !== undefined ? opts.deps.runningBuild : readRunningBuild(),
    fetchBuild: () => fetchServerBuild(),
    hasUnsavedWork: () => registryHasUnsaved() || isEditingField(doc.activeElement ?? null),
    ...opts.deps,
  };
  let inFlight = false;
  const run = async () => {
    if (inFlight) return;
    inFlight = true;
    try {
      const d = await checkForNewVersion(deps);
      if (d === 'reload') reload();
      else if (d === 'notify') opts.onNotify();
    } finally {
      inFlight = false;
    }
  };
  const onVisibility = () => {
    if (doc.visibilityState === 'visible') void run();
  };
  const onPageShow = (e: Event) => {
    if ((e as PageTransitionEvent).persisted) void run();
  };
  doc.addEventListener('visibilitychange', onVisibility);
  win.addEventListener('pageshow', onPageShow);
  return () => {
    doc.removeEventListener('visibilitychange', onVisibility);
    win.removeEventListener('pageshow', onPageShow);
  };
}
