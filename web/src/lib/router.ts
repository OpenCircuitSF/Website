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
 * initRouter()'s popstate handler, which restores the browser's own saved
 * scroll position instead of resetting it, per the acceptance criteria.
 */
export function navigate(path: string, { replace = false }: NavigateOptions = {}): void {
  const url = new URL(path, window.location.origin);
  const route = parsePath(url.pathname, url.search);
  const target = url.pathname + url.search;

  if (replace) {
    window.history.replaceState({}, '', target);
  } else {
    window.history.pushState({}, '', target);
  }

  currentRoute.set(route);
  window.scrollTo(0, 0);
}

let initialized = false;

/**
 * Wire the router up against the live DOM: intercepts same-origin `<a>`
 * clicks (pushState navigation), and handles popstate (back/forward) by
 * restoring the route without resetting scroll -- the browser already
 * restores the scroll position it saved for that history entry. Call once,
 * from App.svelte's onMount. Idempotent: a second call is a no-op, since
 * Svelte's onMount can in principle re-run under HMR.
 */
export function initRouter(): void {
  if (initialized) return;
  initialized = true;

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

  window.addEventListener('popstate', () => {
    currentRoute.set(parsePath(window.location.pathname, window.location.search));
  });
}
