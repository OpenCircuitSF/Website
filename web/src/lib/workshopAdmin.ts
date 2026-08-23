// Pure, framework-free helpers backing the admin workshops screen (#0052,
// PRD §5.2): status vocabulary, the publish/unpublish/cancel offer + confirm
// copy, the create/edit form validation gate, datetime-local <-> RFC 3339
// conversion, interest-tag toggling, and date-sortable list ordering.
//
// Per CLAUDE.md ("SPA logic goes in plain TypeScript modules ... Svelte
// components stay thin") and #0094 (nothing in a .svelte file is covered by
// a test), every decision Workshops.svelte/WorkshopEditor.svelte make about
// validity, status offers, or sort order is a call into this module -- never
// a comparison, `.trim()`, or status literal written inline in a template.
//
// # The slug discrepancy, for this issue's reviewer
//
// #0052's acceptance criteria say the slug should be "auto-generated from
// the title, editable before first publish and locked after." But #0051's
// API (internal/handlers/admin_workshops.go, already committed and
// resolved) never accepts a client-supplied slug at all: Create's doc
// comment says any "slug" field in the POST body is "simply ignored", and
// patchWorkshopRequest has NO slug field whatsoever -- "slug is immutable
// through this route" per that struct's own doc comment. There is no admin
// route that can ever change a workshop's slug, before or after first
// publish. This module therefore treats the slug as always read-only
// (isSlugEditable below always returns false) and the editor displays it as
// plain text, never an input. That satisfies "auto-generated" and "locked"
// but not "editable before first publish" -- flagging this the same way
// lib/workshops.ts (#0053) flagged its own #0051 discrepancy, for the
// reviewer to decide whether #0052's criterion or #0051's committed API
// contract is the one that needs to change.
//
// # Cover image: path/URL entry only, no upload
// #0051's API has no upload endpoint (no multipart route, no storage
// integration) and adding one is a backend change outside this issue's
// admin-UI-only scope. This module and the editor support "cover image ...
// path entry" (a site-relative path or an http(s) URL saved to
// cover_image), not "upload".

import type { AdminWorkshop } from './types';

// ── Status vocabulary (migration 000020's workshops_status_check) ──────────

/** The three workshops.Status* values, in no particular order. */
export const WORKSHOP_STATUSES: readonly string[] = ['draft', 'published', 'canceled'] as const;

/** Human-readable label for a workshop status value. An unknown value is returned as-is. */
export function workshopStatusLabel(status: string): string {
  switch (status) {
    case 'draft':
      return 'Draft';
    case 'published':
      return 'Published';
    case 'canceled':
      return 'Canceled';
    default:
      return status;
  }
}

/** Badge CSS class for a workshop status, mirroring campaignStatusBadgeClass's shape. */
export function workshopStatusBadgeClass(status: string): string {
  switch (status) {
    case 'published':
      return 'badge-success';
    case 'canceled':
      return 'badge-danger';
    default:
      return 'badge-muted';
  }
}

// ── Publish / unpublish / cancel offers ─────────────────────────────────────
//
// internal/workshops/store.go's Update accepts ANY status -> any other
// status (only ErrUnknownStatus guards the value itself); there is no
// server-side state machine restricting which transition is legal. These
// three functions are purely about which action makes sense to OFFER an
// admin, not enforcement -- the server remains the sole authority.

/** Whether "Publish" should be offered (workshop is not already published). */
export function canPublish(status: string): boolean {
  return status !== 'published';
}

/** Whether "Unpublish" should be offered (workshop is currently published). */
export function canUnpublish(status: string): boolean {
  return status === 'published';
}

/** Whether "Cancel" should be offered (workshop is not already canceled). */
export function canCancel(status: string): boolean {
  return status !== 'canceled';
}

/** Confirmation copy for publishing `title`. */
export function publishConfirmMessage(title: string): string {
  return `Publish "${title}"? It will appear on the public workshops page immediately.`;
}

/** Confirmation copy for unpublishing `title`. */
export function unpublishConfirmMessage(title: string): string {
  return `Unpublish "${title}"? It will be removed from the public workshops page and reverts to draft.`;
}

/**
 * Confirmation copy for canceling `title`. Mirrors store.go's own note that
 * cancel is not unpublish: a canceled workshop stays visible on the public
 * site (with a canceled badge -- see lib/workshops.ts's workshopBadgeLabel)
 * rather than disappearing.
 */
export function cancelConfirmMessage(title: string): string {
  return `Cancel "${title}"? It stays visible on the public site, marked canceled, rather than disappearing.`;
}

/** Confirmation copy for deleting `title` (the non-conflict path). */
export function deleteConfirmMessage(title: string): string {
  return `Delete "${title}"? This permanently removes it and cannot be undone.`;
}

// ── Slug (server-owned -- see this module's header note) ───────────────────

/** Always false today -- see this module's header note on the slug discrepancy. */
export function isSlugEditable(): boolean {
  return false;
}

// ── datetime-local <-> RFC 3339 ─────────────────────────────────────────────
//
// `<input type="datetime-local">` reads/writes "YYYY-MM-DDTHH:mm" in the
// browser's OWN local timezone, no offset -- the same "viewer's local
// timezone" convention lib/workshops.ts's formatWorkshopDate uses for
// display. The API stores/returns RFC 3339 (UTC) -- see
// internal/handlers/admin_workshops.go's formatTimePtr / parseOptionalTime.

function pad2(n: number): string {
  return String(n).padStart(2, '0');
}

/** An RFC 3339 timestamp (or absent) as a datetime-local input value, or '' when absent/unparseable. */
export function toDatetimeLocalValue(iso: string | null | undefined): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}T${pad2(d.getHours())}:${pad2(d.getMinutes())}`;
}

/** A datetime-local input value as an RFC 3339 timestamp, or undefined when empty/unparseable. */
export function fromDatetimeLocalValue(value: string): string | undefined {
  const trimmed = value.trim();
  if (trimmed === '') return undefined;
  const d = new Date(trimmed);
  if (Number.isNaN(d.getTime())) return undefined;
  return d.toISOString();
}

// ── Interest tagging ─────────────────────────────────────────────────────────

/** Toggle `id` in `ids`, returning a new array (add if absent, remove if present). */
export function toggleInterestId(ids: number[], id: number): number[] {
  return ids.includes(id) ? ids.filter((x) => x !== id) : [...ids, id];
}

// ── List sorting (acceptance criterion: "sortable by date") ────────────────

export type SortDirection = 'asc' | 'desc';

/** The other direction -- what a sort-toggle click flips to. */
export function toggleSortDirection(direction: SortDirection): SortDirection {
  return direction === 'asc' ? 'desc' : 'asc';
}

/**
 * `list` ordered by `starts_at`. Workshops with no starts_at ("Date TBA")
 * sort after every dated workshop regardless of direction -- there is no
 * date to compare, and burying them at the bottom is more useful than
 * letting an arbitrary string/undefined comparison scatter them through the
 * dated rows.
 */
export function sortWorkshopsByDate(
  list: AdminWorkshop[],
  direction: SortDirection = 'asc',
): AdminWorkshop[] {
  const dated = list.filter((w) => w.starts_at);
  const undated = list.filter((w) => !w.starts_at);
  dated.sort((a, b) => {
    const ta = new Date(a.starts_at as string).getTime();
    const tb = new Date(b.starts_at as string).getTime();
    return direction === 'asc' ? ta - tb : tb - ta;
  });
  return [...dated, ...undated];
}

// ── Create/edit form validation ─────────────────────────────────────────────

/** Raw form field values, as bound directly to WorkshopEditor.svelte's inputs. */
export interface WorkshopFormFields {
  title: string;
  summary: string;
  bodyMd: string;
  startsAtLocal: string; // datetime-local value, possibly ''
  endsAtLocal: string; // datetime-local value, possibly ''
  locationName: string;
  locationAddress: string;
  locationNote: string;
  capacity: string; // raw text input
  signupUrl: string;
  coverImage: string;
  interestIds: number[];
}

/** The validated, server-shaped fields a successful validateWorkshopForm call produces. */
export interface ValidatedWorkshopFields {
  title: string;
  summary?: string;
  body_md?: string;
  starts_at?: string;
  ends_at?: string;
  location_name?: string;
  location_address?: string;
  location_note?: string;
  capacity?: number;
  signup_url?: string;
  cover_image?: string;
  interest_ids: number[];
}

function emptyToUndefined(s: string): string | undefined {
  const trimmed = s.trim();
  return trimmed === '' ? undefined : trimmed;
}

/** Whether a URL uses http:// or https:// -- used for both signup_url and an http(s) cover image. */
function isHttpUrl(url: string): boolean {
  return /^https?:\/\//i.test(url);
}

/** Whether a cover image value is a site-relative path or an http(s) URL -- never a javascript:/data: scheme. */
function isSafeCoverImage(value: string): boolean {
  return value.startsWith('/') || isHttpUrl(value);
}

/**
 * Validate the create/edit form before submitting. Mirrors what the server
 * itself checks (title required; starts_at/ends_at must be RFC 3339 once
 * converted) plus client-only conveniences the server doesn't police
 * (capacity as a positive integer, a plausible signup URL, a safe cover
 * image reference) so an obviously-bad submit never round-trips. The server
 * remains the source of truth for anything not checked here.
 */
export function validateWorkshopForm(
  fields: WorkshopFormFields,
): ValidatedWorkshopFields | { error: string } {
  const title = fields.title.trim();
  if (title === '') {
    return { error: 'Title is required.' };
  }

  const startsAtTrimmed = fields.startsAtLocal.trim();
  const startsAt = startsAtTrimmed === '' ? undefined : fromDatetimeLocalValue(startsAtTrimmed);
  if (startsAtTrimmed !== '' && startsAt === undefined) {
    return { error: 'Start date/time is invalid.' };
  }

  const endsAtTrimmed = fields.endsAtLocal.trim();
  const endsAt = endsAtTrimmed === '' ? undefined : fromDatetimeLocalValue(endsAtTrimmed);
  if (endsAtTrimmed !== '' && endsAt === undefined) {
    return { error: 'End date/time is invalid.' };
  }

  if (startsAt && endsAt && new Date(endsAt).getTime() < new Date(startsAt).getTime()) {
    return { error: 'End must be on or after start.' };
  }

  const capacityTrimmed = fields.capacity.trim();
  let capacity: number | undefined;
  if (capacityTrimmed !== '') {
    if (!/^\d+$/.test(capacityTrimmed) || Number(capacityTrimmed) <= 0) {
      return { error: 'Capacity must be a positive whole number.' };
    }
    capacity = Number(capacityTrimmed);
  }

  const signupUrl = fields.signupUrl.trim();
  if (signupUrl !== '' && !isHttpUrl(signupUrl)) {
    return { error: 'Signup URL must start with http:// or https://.' };
  }

  const coverImage = fields.coverImage.trim();
  if (coverImage !== '' && !isSafeCoverImage(coverImage)) {
    return {
      error: 'Cover image must be a site-relative path (starting with "/") or an http(s) URL.',
    };
  }

  return {
    title,
    summary: emptyToUndefined(fields.summary),
    body_md: emptyToUndefined(fields.bodyMd),
    starts_at: startsAt,
    ends_at: endsAt,
    location_name: emptyToUndefined(fields.locationName),
    location_address: emptyToUndefined(fields.locationAddress),
    location_note: emptyToUndefined(fields.locationNote),
    capacity,
    signup_url: signupUrl === '' ? undefined : signupUrl,
    cover_image: coverImage === '' ? undefined : coverImage,
    interest_ids: fields.interestIds,
  };
}

// ── Delete: the 409 conflict path ───────────────────────────────────────────

/**
 * Whether an HTTP status is the "still referenced by a campaign" delete
 * conflict (internal/workshops/store.go's ErrHasCampaigns, surfaced as 409
 * by admin_workshops.go). Takes a bare status number rather than importing
 * ApiError from api.ts, so this module has no dependency on the fetch layer
 * -- the caller does `err instanceof ApiError && isDeleteConflict(err.status)`.
 */
export function isDeleteConflict(status: number): boolean {
  return status === 409;
}

/** Build a WorkshopFormFields from a loaded AdminWorkshop, for populating the edit form. */
export function workshopToFormFields(w: AdminWorkshop): WorkshopFormFields {
  return {
    title: w.title,
    summary: w.summary ?? '',
    bodyMd: w.body_md ?? '',
    startsAtLocal: toDatetimeLocalValue(w.starts_at),
    endsAtLocal: toDatetimeLocalValue(w.ends_at),
    locationName: w.location_name ?? '',
    locationAddress: w.location_address ?? '',
    locationNote: w.location_note ?? '',
    capacity: w.capacity != null ? String(w.capacity) : '',
    signupUrl: w.signup_url ?? '',
    coverImage: w.cover_image ?? '',
    interestIds: w.interest_ids,
  };
}

/** A blank WorkshopFormFields for the "new workshop" form. */
export function blankWorkshopFormFields(): WorkshopFormFields {
  return {
    title: '',
    summary: '',
    bodyMd: '',
    startsAtLocal: '',
    endsAtLocal: '',
    locationName: '',
    locationAddress: '',
    locationNote: '',
    capacity: '',
    signupUrl: '',
    coverImage: '',
    interestIds: [],
  };
}
