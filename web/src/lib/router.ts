// A minimal History API path router (PRD §3.4, §7.2; #0014). Replaces the
// `currentView` store as the SPA's navigation mechanism -- ShortLinks wrote to
// a `currentView` store with no URL, which is fine for a signed-in dashboard
// and wrong here: these pages get shared and indexed, so real URLs are a hard
// requirement. `currentUser`/`pendingVerifyToken` in stores.ts stay for
// transient UI state; navigation itself now lives here.
//
// Design notes:
//  - parsePath() and shouldIntercept() are pure functions with no DOM
//    dependency, so they are unit-testable without jsdom (project
//    convention -- see theme.ts, events.ts: DOM-injectable rather than
//    reaching for `document`/`window` directly inside testable logic).
//  - initRouter() is the only function that touches the live DOM
//    (document/window/history) to wire up click interception and popstate;
//    it is exercised by rendering (dev server / build), not by unit test.
//  - No routing library, per PRD §7.2 -- this file is deliberately small.

import { tick } from 'svelte';
import { writable } from 'svelte/store';

/** Every view the SPA can route to. Includes the Phase 1 auth views
 * (login/account/admin/register-verify/recover-verify) alongside the public
 * marketing routes from PRD §5.1, since both now flow through the same
 * History API mechanism instead of the old currentView store. */
export type RouteName =
  | 'home'
  | 'workshops'
  | 'workshop-detail'
  | 'about'
  | 'privacy'
  | 'subscribe'
  | 'subscribe-thanks'
  | 'confirm'
  | 'preferences'
  | 'unsubscribe'
  | 'login'
  | 'register-verify'
  | 'recover-verify'
  | 'account'
  | 'admin'
  | 'not-found';

export interface Route {
  name: RouteName;
  /** The normalized pathname this route was parsed from (no trailing slash,
   * except "/" itself). */
  path: string;
  /** Path parameters extracted from a dynamic segment, e.g. { slug: '...' }
   * for workshop-detail. Empty object for every static route. */
  params: Record<string, string>;
  /** The parsed query string (e.g. ?token=...) -- needed by /confirm and
   * /preferences per the acceptance criteria. Always present, even when
   * empty, so callers never need to null-check it. */
  query: URLSearchParams;
}

/** Static path -> route name. Mirrors PRD §5.1's public routes table plus the
 * Phase 1 auth routes carried over from the old currentView store. */
const STATIC_ROUTES: Readonly<Record<string, RouteName>> = {
  '/': 'home',
  '/workshops': 'workshops',
  '/about': 'about',
  '/privacy': 'privacy',
  '/subscribe': 'subscribe',
  '/subscribe/thanks': 'subscribe-thanks',
  '/confirm': 'confirm',
  '/preferences': 'preferences',
  '/unsubscribe': 'unsubscribe',
  '/login': 'login',
  '/register/verify': 'register-verify',
  '/recover/verify': 'recover-verify',
  '/account': 'account',
  '/admin': 'admin',
};

/** The one dynamic path parameter the router supports (PRD §7.2). */
const WORKSHOP_DETAIL = /^\/workshops\/([^/]+)$/;

/**
 * Parse a pathname + query string into a typed Route. Pure function, no DOM
 * access -- safe to call from a unit test or from the live router alike.
 */
export function parsePath(pathname: string, search = ''): Route {
  const query = new URLSearchParams(search);

  // Normalize a trailing slash on multi-segment paths ("/about/" -> "/about")
  // so both forms route identically. "/" itself is left alone.
  const path =
    pathname.length > 1 && pathname.endsWith('/') ? pathname.slice(0, -1) : pathname || '/';

  const staticName = STATIC_ROUTES[path];
  if (staticName) {
    return { name: staticName, path, params: {}, query };
  }

  const match = WORKSHOP_DETAIL.exec(path);
  if (match) {
    return { name: 'workshop-detail', path, params: { slug: decodeURIComponent(match[1]) }, query };
  }

  return { name: 'not-found', path, params: {}, query };
}

/**
 * The fields shouldIntercept needs off a click MouseEvent and the matched
 * anchor. Kept as a plain interface (rather than lib.dom's MouseEvent /
 * HTMLAnchorElement) so unit tests exercise the decision with plain objects
 * and no DOM at all.
 */
export interface InterceptCandidate {
  /** MouseEvent.button -- 0 is the primary (left) button. */
  button: number;
  metaKey: boolean;
  ctrlKey: boolean;
  shiftKey: boolean;
  altKey: boolean;
  /** Whether something upstream already called preventDefault(). */
  defaultPrevented: boolean;
  /** The anchor's `target` attribute, or null if absent. */
  target: string | null;
  /** The anchor's `download` attribute, or null if absent. Presence alone
   * (regardless of value) means "let the browser download it". */
  download: string | null;
  /** The anchor's resolved absolute href (HTMLAnchorElement.href, not
   * getAttribute('href') -- already resolved against the document's base). */
  href: string;
  /** window.location.origin, passed in rather than read directly so this
   * stays a pure function. */
  currentOrigin: string;
}

/**
 * True when a click on a same-origin `<a>` should be intercepted for
 * client-side routing; false when it must fall through to normal browser
 * navigation -- a modified click (cmd/ctrl/shift/alt, to open in a new tab or
 * window), a new-tab/new-window target, a download link, a non-http(s)
 * scheme (mailto:, tel:), a cross-origin host, or a click some other handler
 * already handled.
 */
export function shouldIntercept(c: InterceptCandidate): boolean {
  if (c.defaultPrevented) return false;
  if (c.button !== 0) return false;
  if (c.metaKey || c.ctrlKey || c.shiftKey || c.altKey) return false;
  if (c.target && c.target !== '_self') return false;
  if (c.download !== null) return false;

  let url: URL;
  try {
    url = new URL(c.href, c.currentOrigin);
  } catch {
    return false;
  }
  if (url.origin !== c.currentOrigin) return false;
  if (url.protocol !== 'http:' && url.protocol !== 'https:') return false;

  return true;
}

/** The current parsed route. initRouter()'s popstate handler and navigate()
 * both write to this; every view reads it reactively as `$currentRoute`. */
export const currentRoute = writable<Route>(
  typeof window !== 'undefined'
    ? parsePath(window.location.pathname, window.location.search)
    : parsePath('/', ''),
);

interface NavigateOptions {
  /** Use history.replaceState instead of pushState -- for redirects that
   * should not add a back-button stop (e.g. stripping a one-time token from
   * a magic-link landing URL). */
  replace?: boolean;
}

/**
 * Navigate to `path` client-side: push (or replace) history state, update
 * currentRoute, and reset scroll to top. Not used for back/forward -- see
 * initRouter()'s popstate handler, which restores the scroll offset *we*
 * saved for that entry (not the browser's own restoration, which is turned
 * off in initRouter() -- see the comment there for why).
 *
 * Before leaving the current entry, its scroll offset is saved into its own
 * history state via replaceState, so that a later Back lands exactly where
 * the user left it. That is belt-and-braces on top of the throttled scroll
 * listener in initRouter() (which has usually already stamped it): the
 * listener is what covers an entry left by a history *traversal*, where
 * navigate() never runs at all.
 */
export function navigate(path: string, { replace = false }: NavigateOptions = {}): void {
  const url = new URL(path, window.location.origin);
  const route = parsePath(url.pathname, url.search);
  const target = url.pathname + url.search;

  // A restore still re-applying would scroll the page we are about to push:
  // its loop keeps calling scrollTo(0, previousOffset) for up to
  // RESTORE_DEADLINE_MS, and the scrollTo(0, 0) below does not stop it (both
  // land on 0 while the new view is still short, so the loop's "the user
  // scrolled" bail-out never trips). See cancelRestore().
  cancelRestore();
  saveScrollToCurrentEntry();

  if (replace) {
    window.history.replaceState({ scrollY: 0 }, '', target);
  } else {
    window.history.pushState({ scrollY: 0 }, '', target);
  }

  currentRoute.set(route);
  window.scrollTo(0, 0);
}

/** Stamp the *current* history entry's state with the current scroll offset,
 * without navigating anywhere (same URL, replaceState). Called just before
 * every push/replace in navigate(), from the throttled scroll listener, and on
 * pagehide, so whichever entry the user eventually Backs (or Forwards) into
 * has an accurate offset to restore. */
function saveScrollToCurrentEntry(): void {
  // Any explicit save supersedes a pending throttled one, and re-arms the
  // throttle window -- otherwise the pending trailing timer would fire *after*
  // a pushState and stamp the freshly-pushed entry with the outgoing page's
  // offset.
  cancelPendingScrollSave();
  lastScrollSaveAt = Date.now();
  try {
    window.history.replaceState(
      { scrollY: window.scrollY },
      '',
      window.location.pathname + window.location.search,
    );
  } catch {
    // Safari rate-limits history.replaceState (historically ~100 calls per 30
    // seconds) and throws a SecurityError past the limit. The throttle below
    // is sized to stay under it, but a page that also replaceState'd from
    // somewhere else could still trip it; losing one scroll stamp is a far
    // better outcome than an uncaught exception out of a scroll handler.
  }
}

// --- Scroll stamping -------------------------------------------------------
//
// navigate() and pagehide are not enough on their own: neither fires when the
// user leaves an entry by a *history traversal*. An entry entered via
// pushState keeps the `{ scrollY: 0 }` it was stamped with at push time for as
// long as the user only ever leaves it with Back/Forward, so scrolling it and
// then coming back Forward replays that stale 0. Stamping from inside the
// popstate handler cannot fix that -- by the time popstate fires the traversal
// has already committed, and replaceState would write the *incoming* entry.
//
// So the current entry is stamped continuously while the user scrolls, which
// means whatever the user is looking at is already recorded before any
// traversal begins.
//
// Throttling strategy: a wall-clock leading+trailing throttle, NOT
// requestAnimationFrame coalescing. rAF coalescing still yields one
// replaceState per animation frame -- ~60 per second -- which blows straight
// through Safari's historical limit of ~100 replaceState calls per 30 seconds
// (a SecurityError, thrown out of a scroll handler) and needlessly hammers the
// history API in every browser. At 400 ms the ceiling is 2.5 writes/second,
// i.e. 75 per 30 seconds of continuous scrolling -- measured at exactly that
// in a real Chromium, comfortably under the limit. The leading edge stamps
// immediately when a scroll starts after an idle period; the trailing edge
// guarantees the final resting offset is recorded once scrolling stops, which
// is the value that actually matters. The cost of the interval is that an
// offset can be up to 400 ms stale if the user scrolls and hits Back in the
// same breath -- a traversal cannot stamp the entry it is leaving (see above),
// so that window is inherent to the approach, not to its tuning.
const SCROLL_SAVE_INTERVAL_MS = 400;

let scrollSaveTimer: ReturnType<typeof setTimeout> | null = null;
let lastScrollSaveAt = 0;
/** Set while a restore is in flight, so the programmatic window.scrollTo()
 * that performs the restore does not immediately re-stamp the entry (and, more
 * importantly, so a mid-traversal clamp of the outgoing document's height
 * cannot stamp the incoming entry with a bogus offset). */
let restoringScroll = false;

function cancelPendingScrollSave(): void {
  if (scrollSaveTimer !== null) {
    clearTimeout(scrollSaveTimer);
    scrollSaveTimer = null;
  }
}

function onScroll(): void {
  if (restoringScroll) return;

  const elapsed = Date.now() - lastScrollSaveAt;
  if (elapsed >= SCROLL_SAVE_INTERVAL_MS) {
    saveScrollToCurrentEntry();
    return;
  }
  if (scrollSaveTimer !== null) return;
  scrollSaveTimer = setTimeout(() => {
    scrollSaveTimer = null;
    if (!restoringScroll) saveScrollToCurrentEntry();
  }, SCROLL_SAVE_INTERVAL_MS - elapsed);
}

/** How long restoreScroll() keeps re-applying an offset while it waits for the
 * document to grow tall enough to accept it. One `await tick()` is enough for a
 * view that renders synchronously, but not for one gated on a fetch -- notably
 * App.svelte, which renders a zero-height "Loading…" placeholder until
 * `GET /api/me` settles, so the very first restore after a document load would
 * otherwise clamp to 0 against a page with no height yet. */
const RESTORE_DEADLINE_MS = 1000;

/** Bumped by every restoreScroll() call so a superseded one stops re-applying. */
let restoreToken = 0;

/**
 * Apply a saved scroll offset to the document, after giving Svelte a chance to
 * render the view that offset belongs to. Shared by the popstate handler
 * (same-document Back/Forward) and by initRouter() (a *cross-document*
 * traversal or reload, where popstate never fires -- see initRouter()).
 */
function restoreScroll(saved: number): void {
  const token = ++restoreToken;
  restoringScroll = true;
  cancelPendingScrollSave();

  const finish = (): void => {
    if (token !== restoreToken) return;
    restoringScroll = false;
    lastScrollSaveAt = Date.now();
  };

  // Wait for Svelte to re-render the restored view before scrolling to it --
  // scrolling against the outgoing view's (possibly shorter) DOM is exactly
  // the bug this machinery exists to fix. See the initRouter() doc comment.
  void tick().then(() => {
    if (token !== restoreToken) return;

    const deadline = Date.now() + RESTORE_DEADLINE_MS;
    // The offset we last wrote, so a change we did not cause can be told apart
    // from a clamp. -1 means "nothing applied yet".
    let applied = -1;

    const attempt = (): void => {
      if (token !== restoreToken) return;

      // The scroll moved and it was not us: the user is scrolling. Stop
      // fighting them.
      if (applied >= 0 && window.scrollY !== applied) {
        finish();
        return;
      }

      window.scrollTo(0, saved);
      applied = window.scrollY;

      // Reached it, or the document is genuinely too short to ever reach it.
      // Either way stop; the deadline bounds the "too short" case, since the
      // page may still be growing.
      if (applied >= saved || Date.now() > deadline) {
        finish();
        return;
      }
      window.requestAnimationFrame(attempt);
    };

    attempt();
  });
}

/**
 * Abandon any in-flight restoreScroll() loop. Called at the top of navigate():
 * a restore left running across a navigation re-applies the *previous* entry's
 * offset to the page that was just pushed, and the throttled scroll listener
 * then stamps that bogus offset into the new entry's state, so the damage
 * outlives the moment. Bumping restoreToken makes the live loop return on its
 * next frame; restoringScroll must be cleared here rather than left to that
 * loop's own finish(), which is token-guarded and will refuse to clear it once
 * it has been superseded (leaving scroll stamping suppressed for good).
 */
function cancelRestore(): void {
  restoreToken++;
  restoringScroll = false;
}

let initialized = false;

/**
 * Wire the router up against the live DOM: intercepts same-origin `<a>`
 * clicks (pushState navigation), handles popstate (back/forward) by restoring
 * the route *and* the scroll offset saved for that entry, keeps the current
 * entry's offset up to date with a throttled `scroll` listener, and applies
 * any offset already on the entry at load time (the cross-document traversal
 * case, where popstate never fires). Call once, from App.svelte's onMount.
 * Idempotent: a second call is a no-op, since Svelte's onMount can in
 * principle re-run under HMR.
 *
 * Scroll restoration is deliberately hand-rolled rather than left to the
 * browser's default `history.scrollRestoration = 'auto'`: on popstate the
 * browser restores scroll against the DOM as it exists at that instant,
 * which is still the *outgoing* view -- Svelte has not yet swapped in the
 * markup for the restored route, so the restore clamps to 0 against a
 * document with no height yet and nothing moves it afterwards. Setting
 * `scrollRestoration = 'manual'` stops the browser from doing that race at
 * all; the popstate handler below does the restore itself, after `tick()`
 * has let Svelte re-render the new view, so the page actually has the height
 * to scroll to.
 */
export function initRouter(): void {
  if (initialized) return;
  initialized = true;

  if ('scrollRestoration' in window.history) {
    window.history.scrollRestoration = 'manual';
  }

  // Cross-document restore. Setting scrollRestoration = 'manual' above turns
  // off the browser's native restoration for *every* history traversal, not
  // just the same-document ones popstate covers -- including leaving the site
  // and pressing Back, and a plain reload. popstate does not fire on a fresh
  // document load, so without this the offset saved by the pagehide/scroll
  // stamps would be written and never read, and the user would land at the top
  // of a page they had scrolled halfway down. (Real Chrome and Safari usually
  // mask this via bfcache, which preserves scroll independently of
  // scrollRestoration -- but bfcache is not guaranteed, and headless CDP
  // disables it.) history.state survives both a reload and a cross-document
  // traversal, so the offset stamped before we left is still there now.
  //
  // Only a positive offset is applied: forcing scrollTo(0, 0) would fight a
  // browser that *did* restore scroll natively (bfcache) on this same load.
  const entryState = window.history.state as { scrollY?: number } | null;
  if (entryState && typeof entryState.scrollY === 'number' && entryState.scrollY > 0) {
    restoreScroll(entryState.scrollY);
  }

  document.addEventListener('click', (event) => {
    if (event.defaultPrevented) return;

    const target = event.target as Element | null;
    const anchor = target?.closest?.('a[href]') as HTMLAnchorElement | null;
    if (!anchor) return;

    const candidate: InterceptCandidate = {
      button: event.button,
      metaKey: event.metaKey,
      ctrlKey: event.ctrlKey,
      shiftKey: event.shiftKey,
      altKey: event.altKey,
      defaultPrevented: event.defaultPrevented,
      target: anchor.getAttribute('target'),
      download: anchor.getAttribute('download'),
      href: anchor.href,
      currentOrigin: window.location.origin,
    };

    if (!shouldIntercept(candidate)) return;

    event.preventDefault();
    navigate(anchor.pathname + anchor.search);
  });

  window.addEventListener('popstate', (event) => {
    // A throttled save queued against the *outgoing* entry must not land now:
    // the traversal has already committed, so replaceState would write it to
    // the incoming entry.
    cancelPendingScrollSave();

    currentRoute.set(parsePath(window.location.pathname, window.location.search));

    const state = event.state as { scrollY?: number } | null;
    const saved = state && typeof state.scrollY === 'number' ? state.scrollY : 0;
    restoreScroll(saved);
  });

  // Keeps the current entry's offset current while the user scrolls, so an
  // entry left by Back/Forward (which never runs navigate()) still carries
  // where the user actually was. Passive: this handler never calls
  // preventDefault, and marking it so keeps it off the scrolling critical
  // path.
  window.addEventListener('scroll', onScroll, { passive: true });

  // Covers leaving the SPA without going through navigate() at all (e.g. a
  // same-tab navigation to an external page and back, or the tab closing).
  // The scroll listener above covers most of this, but not the last
  // SCROLL_SAVE_INTERVAL_MS of it; pagehide closes that gap on the one path
  // where it is cheap to do so.
  window.addEventListener('pagehide', saveScrollToCurrentEntry);
}
