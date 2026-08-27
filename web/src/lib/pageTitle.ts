// Per-route document.title for client-side navigation (#0238). The SPA
// changed views on navigate()/popstate without ever touching
// document.title, so every route past the first showed whatever the FIRST
// route's title happened to be -- wrong for the tab, for history, for
// bookmarks, and for a screen-reader user who has no other way to be told
// which page they landed on (App.svelte pairs this with a focus move; see
// its route-change effect).
//
// Deliberately NOT a second copy of internal/seo/seo.go's SEO title table.
// That Go table governs the SERVER-rendered <title> for the page's FIRST
// paint -- the one a crawler, a social-card unfurl, or a browser tab sees
// before any JS runs -- and is tuned for that audience (keyword-bearing,
// includes "San Francisco", etc; see seo.go's defaultStaticRouteMeta). This
// module governs ONLY subsequent client-side navigations, where there is no
// fresh server render to defer to (the SPA never re-fetches HTML), and its
// job is different: the tab title must match what's actually on screen
// RIGHT NOW, i.e. the current view's own <h1>. App.svelte's route-change
// effect enforces the "don't touch it on the very first render" half of
// that split -- see its own comment for why overwriting the server's title
// on load would be a regression, not a fix.
//
// STATIC_HEADING's values are the LITERAL text each route's view renders as
// its own <h1> (verified against the source at the file:line cited on each
// entry) -- so document.title and the on-screen heading are drawn from the
// same words and cannot silently drift apart, per this issue's criterion 1.
// pageTitle.guard.test.ts proves this mechanically for the entries marked
// "guarded": it parses each cited view with svelte/compiler and asserts the
// <h1>'s own compiled text equals the value here. Three entries
// (workshop-detail, confirm, unsubscribe) are marked "fallback" instead --
// their real <h1> is runtime state (a fetched workshop's title; which of
// several confirm/unsubscribe outcomes rendered) that this route-level table
// cannot see. Each fallback is worded to match that route's FIRST rendered
// state (the state a fresh navigation always lands on), which is accurate
// at the moment the title is set; a later async transition inside that
// view (e.g. ConfirmSubscription's confirming -> success/error) does not
// additionally update document.title today. That's a real, disclosed
// boundary, not a silent gap -- see the implementation subagent's report
// for issues/0238.md's `## Verification`.
//
// STATIC_HEADING's type is `Record<RouteName, string>`, not
// `Partial<...>` -- this is the guard issues/0238.md's criterion 6 asks
// for ("a route added without a title"). A `Record` object literal is
// checked for completeness by TypeScript itself: adding a member to
// RouteName (router.ts) without adding a matching key here is a compile
// error (`npm run check`, which `scripts/check.sh web` runs), not a
// runtime scan that could silently return undefined and paper over the gap
// -- the same "reported loudly, not skipped" shape #0242/#0243's guard
// uses for the live-region classification problem.
import { APP_NAME } from './branding';
import type { Route, RouteName } from './router';

const SUFFIX = ` — ${APP_NAME}`;

/**
 * Appends " — Open Circuit SF" to `heading`, unless `heading` already
 * CONTAINS the brand name somewhere in it -- not just an exact match.
 * login/account/admin/register-verify/recover-verify's shared
 * `<h1>{APP_NAME}</h1>` is an exact match ("Open Circuit SF" alone); About's
 * `<h1>About {APP_NAME}</h1>` ("About Open Circuit SF") and Unsubscribe's
 * `<h1>Leave the {APP_NAME} mailing list?</h1>` are not exact matches but
 * still already say the brand name mid-sentence -- appending the suffix to
 * either produced a real, real-browser-caught bug ("About Open Circuit SF —
 * Open Circuit SF") before this was changed from `===` to `.includes(...)`.
 * Exported so a view with a genuinely dynamic heading (a future addition)
 * can compose its own document.title the same way, rather than re-deriving
 * the suffix rule.
 */
export function formatTitle(heading: string): string {
  return heading.includes(APP_NAME) ? heading : `${heading}${SUFFIX}`;
}

const STATIC_HEADING: Record<RouteName, string> = {
  // guarded — web/src/views/Home.svelte
  home: 'Hands-on electronics workshops',
  // guarded — web/src/views/WorkshopsIndex.svelte
  workshops: 'Workshops',
  // fallback — web/src/views/WorkshopDetail.svelte's <h1> only exists once
  // the workshop has loaded (status === 'loaded'); this is what a fresh
  // navigation shows in the meantime.
  'workshop-detail': 'Workshop',
  // guarded — web/src/views/About.svelte
  about: `About ${APP_NAME}`,
  // guarded — web/src/views/PrivacyPolicy.svelte
  privacy: 'Privacy Policy',
  // guarded — web/src/views/Subscribe.svelte
  subscribe: 'Subscribe',
  // guarded — web/src/views/SubscribeThanks.svelte
  'subscribe-thanks': 'Check your email',
  // fallback — web/src/views/ConfirmSubscription.svelte's <h1> text is
  // 'Confirming…' | "This link didn't work" | "You're confirmed" depending
  // on async state; 'Confirming…' is the state every fresh navigation here
  // starts in.
  confirm: 'Confirming…',
  // guarded — web/src/views/PreferenceCenter.svelte (every loadState branch
  // that renders a heading at all uses this same literal text).
  preferences: 'Manage your preferences',
  // fallback — web/src/views/Unsubscribe.svelte's <h1> text depends on
  // `status`; this is the 'confirm' (pre-click) state every fresh
  // navigation here starts in.
  unsubscribe: `Leave the ${APP_NAME} mailing list?`,
  // guarded — web/src/views/Login.svelte
  login: APP_NAME,
  // guarded — web/src/views/RegisterVerify.svelte
  'register-verify': APP_NAME,
  // guarded — web/src/views/RecoverVerify.svelte
  'recover-verify': APP_NAME,
  // guarded — web/src/views/Account.svelte
  account: APP_NAME,
  // guarded — web/src/views/Admin.svelte
  admin: APP_NAME,
  // guarded — web/src/views/NotFound.svelte
  'not-found': '404 // Not Found',
};

/** The full document.title for `route`, per STATIC_HEADING above. Pure --
 * no DOM access -- so it's unit-testable without jsdom, matching this
 * project's DOM-avoidance convention for testable logic. */
export function titleForRoute(route: Route): string {
  return formatTitle(STATIC_HEADING[route.name]);
}

let announced = false;

/**
 * True from the SECOND call onward; false (and flips the flag) on the
 * first. App.svelte's route-change effect calls this once per route
 * change, including the very first one the page loads on, to decide
 * whether to touch document.title / move focus for that change:
 *
 *   - The FIRST call corresponds to the route the document was actually
 *     loaded on. That load already got the browser's own full navigation
 *     (and, for a route internal/seo/seo.go has a real entry for, a
 *     server-rendered <title> already sitting in the DOM) -- so this
 *     module must not re-announce it. Returns false.
 *   - Every call after that is a genuine client-side navigation (a link
 *     click, a programmatic navigate(), or a popstate) that gets no native
 *     browser-navigation signal of its own, so it needs both. Returns
 *     true.
 *
 * A plain module-level flag, not $state: this is a one-shot fact about the
 * page's lifetime, not reactive UI state, and Svelte's reactivity would add
 * nothing but overhead to something that changes exactly once.
 */
export function shouldAnnounceNavigation(): boolean {
  if (!announced) {
    announced = true;
    return false;
  }
  return true;
}
