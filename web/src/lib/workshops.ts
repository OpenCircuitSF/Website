// Pure, framework-free helpers backing the public workshops index (#0053,
// PRD §5.1/§7.3) and Home.svelte's "next up" section: date formatting, the
// canceled-badge decision, the upcoming-empty-state decision, and the
// next-N-upcoming slice both views share.
//
// #0094 (restated by #0047's own lib/ modules, and #0048's
// campaignProgress.ts) means nothing in a .svelte file is covered by a
// test, so every decision WorkshopsIndex.svelte and Home.svelte's "next up"
// section make about a workshop's date/location/canceled state routes
// through here, never a comparison or ternary written directly in the
// component.
//
// # A discrepancy with #0051, for that issue's reviewer
//
// This issue's own acceptance criteria say "canceled workshops shown with a
// clear canceled badge" for the /workshops index. But
// internal/handlers/public_workshops.go's List handler (#0051, still in
// review as this was written) calls workshops.Store.ListPublished, whose own
// doc comment says the index returns ONLY status='published' rows — a
// canceled workshop never reaches GET /api/workshops at all, only the detail
// route (GET /api/workshops/{slug}). So under the API as committed, a
// canceled workshop simply never appears on this index for the badge logic
// below to apply to. `isCanceled`/`workshopBadgeLabel` are implemented and
// tested anyway (a `publicWorkshopView` always carries `status`, so nothing
// stops a canceled row from showing up here if that handler behavior
// changes), but as things stand they are effectively dead code on the index
// path. Not fixed here — #0051 is someone else's in-flight change; flagging
// for its reviewer to decide whether the index criterion or the handler's
// ListPublished contract is the one that's wrong.

import type { PublicWorkshop, WorkshopsListResponse } from './types';

// ── Date/time formatting (site's local timezone, PRD §5.1) ──────────────────

const DATE_FMT: Intl.DateTimeFormatOptions = { year: 'numeric', month: 'short', day: 'numeric' };
const TIME_FMT: Intl.DateTimeFormatOptions = { hour: 'numeric', minute: '2-digit' };

/**
 * Format a workshop's starts_at/ends_at (RFC 3339, UTC — see
 * internal/handlers/admin_subscribers.go's formatTimePtr, which
 * PublicWorkshopsHandler reuses) as a human date/time in the VIEWER's local
 * timezone — `toLocaleString`/`toLocaleDateString`/`toLocaleTimeString` with
 * no explicit timeZone option read the browser's own, which is what "the
 * site's local timezone" means for a static SPA with no server-rendered
 * request context (matches lib/admin.ts's formatDateTime doing the same for
 * the admin console).
 *
 * `startsAt` absent (workshop date not yet set) → 'Date TBA'. `endsAt`
 * absent or unparseable → the start alone. Both present → a single date with
 * a start–end time range, since PRD's location/date block never spans
 * multiple calendar days for one workshop.
 */
export function formatWorkshopDate(
  startsAt: string | null | undefined,
  endsAt?: string | null | undefined,
): string {
  if (!startsAt) return 'Date TBA';
  const start = new Date(startsAt);
  if (Number.isNaN(start.getTime())) return 'Date TBA';

  const datePart = start.toLocaleDateString(undefined, DATE_FMT);
  const startTime = start.toLocaleTimeString(undefined, TIME_FMT);

  if (!endsAt) return `${datePart}, ${startTime}`;
  const end = new Date(endsAt);
  if (Number.isNaN(end.getTime())) return `${datePart}, ${startTime}`;

  const endTime = end.toLocaleTimeString(undefined, TIME_FMT);
  return `${datePart}, ${startTime}–${endTime}`;
}

// ── Location ──────────────────────────────────────────────────────────────

/** A card's location line: the workshop's location_name, or a plain fallback when it isn't set yet. */
export function workshopLocationLabel(w: Pick<PublicWorkshop, 'location_name'>): string {
  const name = w.location_name?.trim();
  return name ? name : 'Location TBA';
}

// ── Canceled badge (PRD §5.1; see this module's header comment re: #0051) ──

/** Whether a workshop is canceled — the one status value the badge cares about. */
export function isCanceled(w: Pick<PublicWorkshop, 'status'>): boolean {
  return w.status === 'canceled';
}

/** The card badge text for a workshop, or null when no badge should show. */
export function workshopBadgeLabel(w: Pick<PublicWorkshop, 'status'>): string | null {
  return isCanceled(w) ? 'Canceled' : null;
}

// ── Empty state (PRD §5.1: "empty state when nothing is scheduled") ────────

/**
 * Whether the index should render its "nothing scheduled" empty state with
 * a subscribe prompt. Keyed on `upcoming` alone: an empty `past` list is
 * normal for a brand-new group and is its own (unremarkable) section state,
 * not the "nothing is scheduled" case PRD's Notes describe.
 */
export function hasNoUpcoming(list: Pick<WorkshopsListResponse, 'upcoming'>): boolean {
  return list.upcoming.length === 0;
}

// ── Home page "next up" (#0015's placeholder → real data) ──────────────────

/** Default cap for Home.svelte's "next up" section, per #0015's acceptance criteria. */
export const NEXT_UP_LIMIT = 3;

/**
 * The next N upcoming workshops for Home.svelte's "next up" section. The
 * API already returns `upcoming` in chronological order (ListPublished), so
 * this is a plain slice — no re-sorting here, which would risk drifting from
 * the server's own ordering rule.
 */
export function nextWorkshops(
  list: Pick<WorkshopsListResponse, 'upcoming'>,
  limit: number = NEXT_UP_LIMIT,
): PublicWorkshop[] {
  return list.upcoming.slice(0, Math.max(0, limit));
}
