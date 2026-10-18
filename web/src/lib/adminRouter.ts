// Hash routing inside the admin section (#0527). The public site keeps its
// History API path router (router.ts, #0014); admin screens are
// authenticated, never indexed, purely client state — so their URLs are
// fragments on /admin: `/admin#/campaigns/42`. A fragment never reaches the
// server, so this needs no change to the Go route table, the Go/TypeScript
// route parity, robots.txt, or the sitemap.
//
// Design notes:
//  - parseAdminHash() and the *Hash builders are pure functions with no DOM
//    dependency, so they are unit-testable without jsdom (project
//    convention, same split router.ts draws: pure logic here, the live
//    `hashchange` wiring stays in Admin.svelte).
//  - One routing idiom, deliberately (issue hazard 5): router.ts publishes
//    the OUTER route as a svelte/store and that stays untouched; the admin
//    hash state is LOCAL to Admin.svelte as a `$state` derived from
//    parseAdminHash() plus a hashchange listener. Promoting it to a second
//    global store would duplicate router.ts's role for a state with exactly
//    one consumer; keeping the parser pure keeps the component thin.
//  - Only hashes beginning `#/` are admin routes (issue hazard 3): the skip
//    link's `#main-content` and any other same-page anchor are same-document
//    targets, not screens. parseAdminHash returns null for them and
//    Admin.svelte's hashchange listener ignores null.
//  - No create-new hash state: campaign and workshop creation are modals on
//    the list screens (Campaigns.svelte/Workshops.svelte), not editor
//    screens — the route table's "a create-new state, if one exists" row
//    resolves to "none exists".

/** Every admin screen the hash can address, plus not-found for an unknown or
 * malformed hash (#0527 criterion 5). Mirrors Admin.svelte's screen set —
 * confirmed against the code, not the issue's route table, per that table's
 * own instruction. */
export type AdminSection =
  | 'overview'
  | 'settings'
  | 'users'
  | 'audit'
  | 'interests'
  | 'subscribers'
  | 'pending'
  | 'suppressions'
  | 'deliverability'
  | 'campaigns'
  | 'workshops'
  | 'outreach'
  | 'crt'
  | 'not-found';

/** The nav as grouped on screen (#0586): Overview stands alone, then Mailing
 * list / Workshops / System. ADMIN_NAV_SECTIONS below is derived from this so
 * the two cannot disagree about which screens exist. */
export const ADMIN_NAV_GROUPS: readonly {
  label: string;
  sections: readonly Exclude<AdminSection, 'not-found'>[];
}[] = [
  { label: 'Mailing list', sections: ['subscribers', 'pending', 'suppressions', 'deliverability', 'campaigns', 'interests'] },
  { label: 'Workshops', sections: ['workshops', 'outreach'] },
  { label: 'System', sections: ['settings', 'users', 'audit', 'crt'] },
];

/** The sections that are real screens (everything but not-found), in nav
 * order: Overview, then each group's sections. */
export const ADMIN_NAV_SECTIONS: readonly Exclude<AdminSection, 'not-found'>[] = [
  'overview',
  ...ADMIN_NAV_GROUPS.flatMap((g) => g.sections),
];

/** The subtab bar's label for each section. Kept beside ADMIN_NAV_SECTIONS so
 * a new section forces both to be filled in together. */
export const ADMIN_SECTION_LABELS: Readonly<Record<AdminSection, string>> = {
  overview: 'Overview',
  settings: 'Settings',
  users: 'Users',
  audit: 'Audit log',
  interests: 'Interests',
  subscribers: 'Subscribers',
  pending: 'Pending',
  suppressions: 'Suppressions',
  deliverability: 'Deliverability',
  campaigns: 'Campaigns',
  workshops: 'Workshops',
  outreach: 'Outreach',
  crt: 'CRT session',
  'not-found': 'Not found',
};

/** Which screen — and which record within it — the current hash addresses.
 * campaign is non-null only for `#/campaigns/:id[...]` (mode distinguishes
 * the editor from its stats view); workshopId is non-null only for
 * `#/workshops/:id`. query carries the hash's own query part (parsed with
 * URLSearchParams — never window.location.search, which belongs to the
 * outer page). */
export interface AdminRoute {
  section: AdminSection;
  campaign: { id: number; mode: 'editor' | 'stats' } | null;
  workshopId: number | null;
  query: URLSearchParams;
}

const SECTION_SET: ReadonlySet<string> = new Set(ADMIN_NAV_SECTIONS);
const DIGITS = /^\d+$/;

/**
 * Parse `window.location.hash` into an AdminRoute. Pure, no DOM access.
 *
 * Returns null when the hash is not an admin route at all — anything not
 * beginning `#/` (and not empty: an empty hash is the admin landing state,
 * Overview). `#main-content` and friends must be left for the browser's own
 * same-page anchor handling (#0527 hazard 3).
 *
 * An empty hash, `#`, and `#/` all normalize to Overview. A trailing slash
 * (`#/settings/`) is ignored. An unknown path (`#/nope`), a malformed record
 * id (`#/campaigns/abc`), and a stray extra segment are the not-found
 * section — no crash, no redirect loop (#0527 criterion 5); a well-formed id
 * that names a deleted record is the editor's own empty/error state.
 */
export function parseAdminHash(hash: string): AdminRoute | null {
  if (hash === '' || hash === '#') return overviewRoute();

  if (!hash.startsWith('#/')) return null;

  let rest = hash.slice(2);
  const qIndex = rest.indexOf('?');
  const query = new URLSearchParams(qIndex >= 0 ? rest.slice(qIndex + 1) : '');
  if (qIndex >= 0) rest = rest.slice(0, qIndex);
  if (rest.endsWith('/')) rest = rest.slice(0, -1);

  if (rest === '') return overviewRoute();

  const segs = rest.split('/');
  if (segs.length === 1) {
    const section = segs[0];
    if (SECTION_SET.has(section)) {
      return { section: section as AdminSection, campaign: null, workshopId: null, query };
    }
    return notFoundRoute(query);
  }

  if (segs[0] === 'campaigns') {
    if (segs.length === 2 && DIGITS.test(segs[1])) {
      return { section: 'campaigns', campaign: { id: Number(segs[1]), mode: 'editor' }, workshopId: null, query };
    }
    if (segs.length === 3 && DIGITS.test(segs[1]) && segs[2] === 'stats') {
      return { section: 'campaigns', campaign: { id: Number(segs[1]), mode: 'stats' }, workshopId: null, query };
    }
    return notFoundRoute(query);
  }

  if (segs[0] === 'workshops' && segs.length === 2 && DIGITS.test(segs[1])) {
    return { section: 'workshops', campaign: null, workshopId: Number(segs[1]), query };
  }

  return notFoundRoute(query);
}

function overviewRoute(): AdminRoute {
  return { section: 'overview', campaign: null, workshopId: null, query: new URLSearchParams() };
}

/** A guaranteed Overview route for callers that must present some screen even
 * when parseAdminHash returned null (Admin.svelte's initial state, a loaded
 * hash that is a same-page anchor). Fresh URLSearchParams per call — routes
 * are values, never shared mutable state. */
export function adminOverviewRoute(): AdminRoute {
  return overviewRoute();
}

function notFoundRoute(query: URLSearchParams): AdminRoute {
  return { section: 'not-found', campaign: null, workshopId: null, query };
}

// ── Hash builders ────────────────────────────────────────────────────────────
//
// Every admin navigation — subtab links' hrefs, cross-screen jumps — builds
// its URL through one of these, so the URL shapes live in exactly one place.

/** The hash for a plain section screen. Overview is `#/` (the empty hash
 * means the same thing, but `#/` is the canonical spelling). */
export function adminSectionHash(section: Exclude<AdminSection, 'not-found'>): string {
  return section === 'overview' ? '#/' : `#/${section}`;
}

/** The addressable parts of the Settings screen (#0584), carried in the hash
 * query (`#/settings?section=outreach`). */
export type SettingsSection = 'general' | 'server' | 'outreach';
export const SETTINGS_SECTIONS: readonly { id: SettingsSection; label: string }[] = [
  { id: 'general', label: 'General' },
  { id: 'server', label: 'Server settings' },
  { id: 'outreach', label: 'Outreach' },
];

/** The Settings section a hash query names, else null. */
export function settingsSectionFromQuery(query: URLSearchParams): SettingsSection | null {
  const v = query.get('section');
  return SETTINGS_SECTIONS.find((s) => s.id === v)?.id ?? null;
}

/** The hash for Settings, optionally landing on one section. */
export function adminSettingsHash(section?: SettingsSection): string {
  return section ? `#/settings?section=${section}` : '#/settings';
}

/** The hash for one campaign's editor (default) or stats view. */
export function adminCampaignHash(id: number, mode: 'editor' | 'stats' = 'editor'): string {
  return mode === 'stats' ? `#/campaigns/${id}/stats` : `#/campaigns/${id}`;
}

/** The hash for one workshop's editor. */
export function adminWorkshopHash(id: number): string {
  return `#/workshops/${id}`;
}

/** The audit log, pre-filtered to one campaign's history (#0114's deep link,
 * now a real URL: filters that are useful to share live in the hash's query
 * part). */
export function adminAuditCampaignHash(campaignId: number): string {
  return `#/audit?campaign=${campaignId}`;
}

/** The Outreach tab's sub-views: the workshops list, the saved flyer
 * locations (#0548) and click results (#0546). Carried in the hash query
 * (`#/outreach?view=locations`) so no AdminRoute shape changes. */
export type OutreachView = 'workshops' | 'workshop' | 'locations' | 'results' | 'flyers' | 'print';

/** The sub-view a hash query names; unknown or missing is `workshops`. */
export function outreachViewFromQuery(query: URLSearchParams): OutreachView {
  const v = query.get('view');
  if (v === 'workshop') return outreachWorkshopFromQuery(query) === null ? 'workshops' : 'workshop';
  return v === 'locations' || v === 'results' || v === 'flyers' || v === 'print' ? v : 'workshops';
}

/** The hash for an Outreach sub-view. */
export function adminOutreachHash(view: OutreachView = 'workshops'): string {
  return view === 'workshops' ? '#/outreach' : `#/outreach?view=${view}`;
}

/** The workshop a results hash names: a positive integer `workshop` param, else null. */
export function outreachWorkshopFromQuery(query: URLSearchParams): number | null {
  const raw = query.get('workshop');
  if (raw === null || !/^[0-9]+$/.test(raw)) return null;
  const n = Number(raw);
  return Number.isSafeInteger(n) && n > 0 ? n : null;
}

/** The results hash: the all-workshops summary, or one workshop's results. */
export function adminOutreachResultsHash(workshopId?: number): string {
  return workshopId === undefined ? '#/outreach?view=results' : `#/outreach?view=results&workshop=${workshopId}`;
}

/** One workshop's outreach page (#0568): `<id>` is the workshop id. */
export function adminOutreachWorkshopHash(workshopId: number): string {
  return `#/outreach?view=workshop&workshop=${workshopId}`;
}

/** The flyer a flyers or print hash names: a positive integer `flyer` param, else null (#0549). */
export function outreachFlyerFromQuery(query: URLSearchParams): number | null {
  const raw = query.get('flyer');
  if (raw === null || !/^[0-9]+$/.test(raw)) return null;
  const n = Number(raw);
  return Number.isSafeInteger(n) && n > 0 ? n : null;
}

/** How a flyer prints: one per sheet, or two half-size per sheet. */
export type FlyerPrintLayout = 'full' | 'half';

/** The print layout a hash names; unknown or missing is `full`. */
export function outreachLayoutFromQuery(query: URLSearchParams): FlyerPrintLayout {
  return query.get('layout') === 'half' ? 'half' : 'full';
}

/** The flyers list, or one flyer's editor. */
export function adminOutreachFlyersHash(flyerId?: number): string {
  return flyerId === undefined ? '#/outreach?view=flyers' : `#/outreach?view=flyers&flyer=${flyerId}`;
}

/** The admin-only print page for a flyer. */
export function adminOutreachPrintHash(flyerId: number, layout: FlyerPrintLayout): string {
  return `#/outreach?view=print&flyer=${flyerId}&layout=${layout}`;
}

/**
 * Where Login.svelte sends the user after a successful sign-in (criterion 6:
 * a signed-out visit to a deep admin link must come back to that exact
 * screen). `next` is the raw ?next= value; only a same-origin path is
 * honored — anything absent, protocol-relative (`//…`), or absolute is
 * rejected in favor of the default /account. A next containing a fragment
 * needs a full document load (navigate() pushes pathname+search only, and a
 * pushState never fires hashchange), so the caller gets fullLoad to act on.
 */
export function resolvePostLoginDestination(next: string | null): { path: string; fullLoad: boolean } {
  if (!next || !next.startsWith('/') || next.startsWith('//')) {
    return { path: '/account', fullLoad: false };
  }
  return { path: next, fullLoad: next.includes('#') };
}
