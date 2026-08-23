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
// # The slug discrepancy -- decided by #0137, not open anymore
//
// #0052's acceptance criteria originally asked for a slug "auto-generated
// from the title, editable before first publish and locked after." #0051's
// API (internal/handlers/admin_workshops.go) never accepted a
// client-supplied slug at all -- createWorkshopRequest has no "slug" field
// and patchWorkshopRequest has none either ("slug is immutable through this
// route" per that struct's own doc comment) -- so #0052's reviewer left the
// criterion unticked rather than build something the API could not support.
// #0137 resolved the disagreement: the API is right. A stable,
// server-generated slug is one less thing an admin can get wrong, the
// collision-suffix retry already guarantees uniqueness, and an editable slug
// would reopen the "unvalidated value reaches a URL" class #0138/#0144/#0145
// closed. #0052's criterion was struck with a note pointing here. The
// accepted cost: a title typo caught after publish leaves a permanently
// wrong slug, since editing the title does not regenerate it (proved by
// execution in #0137 -- PATCHing only `title` leaves `slug` byte-identical).
// This module therefore treats the slug as always read-only
// (isSlugEditable below always returns false, and stays that way by design,
// not by gap) and the editor displays it as plain text, never an input.
//
// # Cover image: path entry only, no upload, same-origin only (#0138)
// #0051's API has no upload endpoint (no multipart route, no storage
// integration) and adding one is a backend change outside this issue's
// admin-UI-only scope -- #0138 struck #0052's "upload or path entry"
// criterion for exactly this reason: no storage location, size/type limit,
// or serving path has ever been designed, PRD §5.2 does not ask for an
// upload route, and nothing else in the product uploads a file, so an
// upload endpoint deserves its own issue rather than riding in on this one.
// Path entry is the intended v1 behavior.
//
// #0138 also closed a hole in what path entry accepted: isSafeCoverImage
// used to also accept a full http(s) URL, including one to another host --
// entirely reasonable-looking, since an admin might paste a photo's URL --
// but CLAUDE.md §9 ("no external CDNs ... self-contained by design") and
// PRD §6.2's own schema comment ("cover_image TEXT, -- path under /assets")
// both say a cover image belongs on THIS site. An absolute URL to another
// host loads that host's asset on every page view (a referer leak, and a
// dependency on a server this project doesn't control) -- exactly the thing
// §9 rules out. So cover_image now accepts ONLY a site-relative path: a
// leading "/" and nothing else that could resolve off-site. This is a
// narrower rule than #0138 applied to Markdown *link* destinations
// (isSafeLinkHref in ./linkSafety, also touched by #0138 -- that module was
// markdown.ts until #0157 renamed it; its own Markdown renderer was removed
// by #0136, which moved body-preview rendering server-side; isSafeLinkHref
// itself is unaffected): a link is a navigation the reader chooses to
// follow, so an absolute https:// URL to any host stays allowed there (a workshop's
// signup_url is routinely an external ticketing site) -- an image is a load
// the page performs on the reader's behalf without asking, so it gets the
// tighter, same-origin-only rule.

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
 * cancel is not unpublish: a canceled workshop that WAS published stays
 * visible on the public site (with a canceled badge -- see
 * lib/workshops.ts's workshopBadgeLabel) rather than disappearing.
 *
 * `wasPublished` must be `!!workshop.published_at` at the moment the admin
 * opens this confirmation -- the same signal the public read path
 * (internal/handlers/public_workshops.go, #0171) uses to decide visibility.
 * A workshop that has never been published (a draft, or one that was
 * unpublished before this cancel) has `published_at` NULL, and canceling it
 * does NOT make it public -- the pre-#0171 defect this function's old,
 * unconditional "stays visible" copy was describing wrong for that case.
 */
export function cancelConfirmMessage(title: string, wasPublished: boolean): string {
  if (wasPublished) {
    return `Cancel "${title}"? It stays visible on the public site, marked canceled, rather than disappearing.`;
  }
  return `Cancel "${title}"? It was never published, so it stays private -- canceling it will not make it visible.`;
}

/** Confirmation copy for deleting `title` (the non-conflict path). */
export function deleteConfirmMessage(title: string): string {
  return `Delete "${title}"? This permanently removes it and cannot be undone.`;
}

// ── Announce panel copy (#0143) ─────────────────────────────────────────────

/**
 * Describes what the Announce shortcut's draft campaign will actually
 * target, given the workshop's currently *saved* interest ids.
 *
 * `POST /admin/workshops/{id}/announce` (#0056) takes no body -- it reads
 * the persisted row, not this screen's local checkbox buffer -- so callers
 * must pass the last-loaded/saved `AdminWorkshop.interest_ids`, not
 * `WorkshopFormFields.interestIds`, or the copy can describe unsaved edits
 * that Announce will not actually see.
 *
 * `internal/mailing` has no "empty audience" mode: with zero interest ids,
 * `any_of` targeting is refused store-side and
 * `internal/handlers/admin_workshop_announce.go` falls back to audience mode
 * `all` -- every confirmed subscriber. #0056's reviewer accepted that
 * fallback (`all` or refuse, and refusing breaks the one-click contract for
 * what is usually a data-entry omission); the defect this fixes is that the
 * panel said "targeted at its interests" even in the fallback case, which is
 * simply untrue.
 */
export function announceTargetingDescription(interestIds: number[]): string {
  return interestIds.length === 0
    ? 'This workshop has no interests set, so the draft will target everyone on the list.'
    : // #0170 (carried in from #0162/#0167's review): this used to read "It
      // will be targeted..." -- but by the time this sentence renders it
      // opens its own paragraph (#0162 split the panel into three separate
      // elements; see the <!-- #0162 --> comment above this panel in
      // WorkshopEditor.svelte), so "It" pointed at an antecedent ("the
      // draft") two sentences back in a different paragraph. Naming the
      // subject again reads better than a pronoun with no local antecedent.
      "The draft will be targeted at this workshop's interests.";
}

/**
 * CSS class for the Announce panel's targeting sentence (#0162).
 *
 * `#0151`'s unsaved-interests hint and this sentence both used to inherit
 * `.text-muted` from the surrounding explanatory paragraph, so the one
 * sentence most likely to change an admin's next action read as
 * de-emphasised filler. This one is the higher-stakes of the two (it says
 * the draft will reach *everyone*, not a segment) -- warn-styled exactly
 * when that's what it's saying, `.text-muted` in the ordinary
 * targeted-at-interests case. Uses `.text-warn`, the warning treatment
 * already established for a conditionally-rendered admin-console notice
 * (`Admin.svelte`'s slug-format hint and its interest-deactivation notice) --
 * see this module's and `WorkshopEditor.svelte`'s header comments for why
 * this doesn't also carry `role="status"`: unlike those two, and unlike
 * `announceUnsavedInterestsHint` below, this sentence describes the
 * workshop's already-loaded, saved state -- it is present the moment the
 * panel first renders, not something that newly appears in response to an
 * in-page action, so there is nothing for `role="status"` to announce.
 * `#0063` (accessibility audit) owns the console-wide version of this
 * question; this stays deliberately minimal rather than inventing a second
 * pattern for it to reconcile.
 */
export function announceTargetingClass(interestIds: number[]): string {
  return interestIds.length === 0 ? 'text-warn' : 'text-muted';
}

/**
 * Whether two interest-id arrays represent different SETS of ids --
 * order-insensitive, and tolerant of `undefined`/empty inputs (both treated
 * as the empty set). Two arrays holding the same ids in a different order
 * are NOT a divergence; a genuine addition, removal, or swap is.
 */
export function interestIdsDiverge(
  a: number[] | undefined,
  b: number[] | undefined,
): boolean {
  const as = new Set(a ?? []);
  const bs = new Set(b ?? []);
  if (as.size !== bs.size) return true;
  for (const id of as) {
    if (!bs.has(id)) return true;
  }
  return false;
}

/**
 * A trailing warning for the Announce panel (#0151) when the editor's local,
 * unsaved checkbox buffer (`WorkshopFormFields.interestIds`) diverges from
 * the workshop's last-*saved* `interest_ids` -- the value
 * `announceTargetingDescription` describes and the only one
 * `POST /admin/workshops/{id}/announce` actually reads (see that function's
 * doc comment). Deliberately does NOT change what
 * `announceTargetingDescription` itself says: sourcing the sentence from the
 * unsaved buffer would reintroduce #0143's original defect, pointing the
 * other way (promising interest targeting Announce will not perform). This
 * only adds a heads-up that the two disagree.
 *
 * Returns '' (render nothing) when the two already match, so the two
 * existing #0143 copy paths are visually unchanged in the common case.
 *
 * #0162: this used to return a *leading-space* fragment
 * (`' Unsaved interest changes...'`) meant to be concatenated onto the end
 * of the Announce panel's muted explanatory paragraph, which is how it
 * inherited that paragraph's `.text-muted` styling and read as filler
 * rather than as the warning it is. `WorkshopEditor.svelte` now renders this
 * as its own `.text-warn` paragraph with `role="status"` (it appears and
 * disappears purely in response to the admin checking/unchecking interest
 * boxes on this screen, so unlike `announceTargetingClass` above there IS
 * something for `role="status"` to announce) -- so the returned string is a
 * plain, non-prefixed sentence now; no caller relies on the old leading
 * space (only `WorkshopEditor.svelte` consumed this, and it's updated
 * alongside).
 */
export function announceUnsavedInterestsHint(
  formInterestIds: number[] | undefined,
  savedInterestIds: number[] | undefined,
): string {
  return interestIdsDiverge(formInterestIds, savedInterestIds)
    ? 'Unsaved interest changes are not included — save first.'
    : '';
}

// ── Body preview staleness (#0167) ──────────────────────────────────────────

/**
 * Whether the editor's live Markdown buffer has diverged from the
 * workshop's last-SAVED body -- i.e. whether "Show preview" (which always
 * renders the STORED row server-side, `#0136`, so preview output is
 * byte-identical to what publishes) would currently show something other
 * than what the admin is looking at in the textarea.
 *
 * `#0136` shipped `hasPreviewContent`, derived from `previewHtml` being
 * non-empty -- that's the right gate for "is there anything to render at
 * all" (true only once *something* has ever been saved), but it says
 * nothing about whether what IS rendered still matches the buffer. That
 * left the "Save the body first" cue visible only for a brand-new,
 * never-saved workshop: editing one that already has a body, typing, and
 * clicking "Show preview" showed the *previous* text with no indication it
 * was stale. This predicate is the other, orthogonal check --
 * `WorkshopEditor.svelte` uses both: `hasPreviewContent` (previewHtml
 * non-empty) gates whether there's a rendered preview to show at all, and
 * this gates whether a "this is the last saved version" cue should sit
 * alongside it.
 *
 * `savedBodyMd` is `undefined` for a workshop whose `body_md` was never
 * set (`AdminWorkshop.body_md` is optional) -- treated the same as `''`, the
 * same convention `workshopToFormFields` already uses (`w.body_md ?? ''`).
 * Deliberately NOT used to re-render the preview client-side -- that's the
 * defect `#0136` exists to remove; this only decides whether to show a cue
 * next to the server-rendered, last-saved HTML.
 *
 * #0170 (carried in from #0162/#0167's review): compares TRIMMED bodies.
 * `validateWorkshopForm` below saves `fields.bodyMd.trim()`, so a
 * whitespace-only edit (leading/trailing space added or removed, nothing
 * else) previously flagged the preview as stale even though clicking Save
 * would store a byte-identical `body_md` -- the cue would then immediately
 * clear on save with nothing having actually changed server-side. That's a
 * pure over-warn, never an under-warn (anything that survives a `.trim()`
 * diff also differs untrimmed), so it wasn't a defect, just noise. This
 * departs from #0167's original literal criterion ("previewStale is true
 * whenever fields.bodyMd !== workshop.body_md") in favor of matching what
 * the buffer will actually persist as; leaving it untrimmed and documenting
 * why was an equally defensible choice, but there's no reason to warn an
 * admin about a save that will change nothing.
 */
export function isPreviewStale(bodyMd: string, savedBodyMd: string | undefined): boolean {
  return bodyMd.trim() !== (savedBodyMd ?? '').trim();
}

// ── Slug (server-owned -- see this module's header note) ───────────────────

/** Always false, by design (#0137) -- see this module's header note. */
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

/**
 * The validated, server-shaped fields a successful validateWorkshopForm call
 * produces.
 *
 * #0139: every optional field is always present here, as `''` when blank --
 * never `undefined`. `JSON.stringify` drops an `undefined`-valued key
 * entirely, which is indistinguishable on the wire from a field the admin
 * never touched, so a blanked field silently failed to clear
 * (internal/handlers/admin_workshops.go's patchWorkshopRequest reads an
 * absent key as "leave alone"). Sending the key with an explicit `''`
 * instead lets the server's own `normalizeOptionalCampaignField`/
 * `parseOptionalTime` "empty string means clear" path run -- the same
 * convention CampaignEditor.svelte's preheader now uses (see api.ts's
 * CampaignDraftFields). Always including the key is harmless on Create too:
 * an unset field there normalizes to the same nil/NULL either way.
 */
export interface ValidatedWorkshopFields {
  title: string;
  summary: string;
  body_md: string;
  starts_at: string;
  ends_at: string;
  location_name: string;
  location_address: string;
  location_note: string;
  // #0146: always present, `null` (not an omitted key) is the clear
  // sentinel -- see validateWorkshopForm's doc comment.
  capacity: number | null;
  signup_url: string;
  cover_image: string;
  interest_ids: number[];
}

/** Whether a URL uses http:// or https:// -- used for signup_url. */
function isHttpUrl(url: string): boolean {
  return /^https?:\/\//i.test(url);
}

// #0168: the client-side upper bound on capacity, kept as one named
// constant rather than a literal at the call site so it's found by whoever
// next changes either side of this Go<->TypeScript-duplicated rule.
// Mirrors internal/handlers/admin_workshops.go's isValidCapacity AND
// migrations/000020_create_workshops.up.sql's workshops_capacity_check
// CHECK constraint (`capacity IS NULL OR (capacity >= 1 AND capacity <=
// 2147483647)`) -- capacity is `workshops.capacity INT`, and 2147483647 is
// INT's max value in Postgres. Without a client-side check an admin typing
// something like 3000000000 got a round trip and a 400 (previously a raw
// 500, before #0155 added the CHECK) instead of an immediate local message;
// see #0168's Notes for why this single-value comment is the right amount
// of ceremony rather than a full parity fixture like #0157's URL
// validators. If `migrations/000020` ever widens the column (e.g. to
// BIGINT), this constant, isValidCapacity, and the CHECK all need to move
// together.
const MAX_WORKSHOP_CAPACITY = 2147483647;

// C0 controls (0x00-0x1f) plus DEL (0x7f). Same rule as linkSafety.ts's
// HAS_CONTROL_CHAR (#0138 bounce, finding 1): a browser's URL parser
// deletes every ASCII tab/LF/CR anywhere in the string before resolving it,
// so e.g. "/\t/evil.host/x.jpg" doesn't *look* like it starts with "//" as
// written, but reassembles into exactly that protocol-relative form once
// the browser strips the tab back out. Rejecting the whole C0+DEL range
// (not just tab/LF/CR) matches linkSafety.ts's existing rule rather than
// adding a third, narrower definition of "safe URL" to the codebase --
// \x00, \x0b, \x0c and \x7f don't actually resolve off-site (a browser
// percent-encodes them into the path), so rejecting those too is defence
// in depth, not strictly required.
const HAS_CONTROL_CHAR = /[\x00-\x1f\x7f]/;

/**
 * Whether a cover image value is a site-relative path -- SAME-ORIGIN ONLY
 * (#0138; see this module's header note for why an http(s) URL to another
 * host, previously accepted here, no longer is).
 *
 * A single leading "/" is required. That alone isn't enough: a value
 * starting with "//" is a protocol-relative URL (`//evil.host/x` resolves
 * against the page's own scheme but a DIFFERENT host -- an off-site load,
 * not a path), and a browser's URL parser treats a leading backslash the
 * same as a forward slash when resolving a relative reference against a
 * special (http/https) base, so `\evil.host/x` and `/\evil.host/x` both
 * normalize to that same off-site `//evil.host/x` form. Normalizing
 * backslashes to slashes before the "//" check catches both spellings with
 * one rule instead of a per-string denylist.
 *
 * A control character (tab/LF/CR, or any other C0/DEL) is rejected before
 * any of that: a browser's URL parser deletes every ASCII tab, LF and CR
 * from the input before parsing it, so e.g. "/\t/evil.host/x.jpg" doesn't
 * start with "//" as written but reassembles into that exact off-site form
 * once the browser normalizes it away. See linkSafety.ts's isSafeLinkHref /
 * HAS_CONTROL_CHAR, which already closed this same class for link
 * destinations -- this reuses the identical rule rather than a third
 * variant.
 *
 * Exported (#0157) so the checked-in Go<->TypeScript parity fixture
 * (testdata/url_validators.json, asserted by both this module's
 * urlValidatorFixture.test.ts and internal/handlers's
 * url_validator_fixture_test.go) can call the real function directly rather
 * than a throwaway export-added copy, as the three prior ad-hoc sweeps
 * (#0138 x2, #0152) all had to. No behavior change -- validateWorkshopForm's
 * call site below is unaffected.
 */
export function isSafeCoverImage(value: string): boolean {
  if (HAS_CONTROL_CHAR.test(value)) return false;
  if (!value.startsWith('/')) return false;
  const normalized = value.replace(/\\/g, '/');
  return !normalized.startsWith('//');
}

/**
 * Validate the create/edit form before submitting. Mirrors what the server
 * itself checks (title required; starts_at/ends_at must be RFC 3339 once
 * converted) plus client-only conveniences the server doesn't police
 * (capacity as a positive integer, a plausible signup URL, a safe cover
 * image reference) so an obviously-bad submit never round-trips. The server
 * remains the source of truth for anything not checked here.
 *
 * #0146: capacity is `number | null`, always present, the same "always send
 * the key" convention #0139 uses for the string fields -- but `null`, not
 * `''`, is capacity's clear sentinel, because the server's `capacity` field
 * is `handlers.Optional[int]` (internal/handlers/optional.go), which reads
 * an explicit JSON `null` as "clear it" and an absent key as "leave it
 * alone". Sending `null` here relies on JSON.stringify keeping an
 * explicit-null key (it does -- only `undefined`-valued keys get dropped).
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
  let capacity: number | null = null;
  if (capacityTrimmed !== '') {
    // #0168: the message matches capacityErrorMessage in
    // internal/handlers/admin_workshops.go -- same rule, same wording, in
    // both places. `Number(capacityTrimmed) <= 0` guards the lower bound
    // (0 is rejected, not treated as "unlimited" -- #0155's decision;
    // NULL/blank is the only "no limit" spelling, see blankWorkshopFormFields
    // and ValidatedWorkshopFields.capacity's doc comment above), and the new
    // upper-bound check below is what #0168 adds: without it, a value like
    // 3000000000 passed the regex/positivity check here and only got caught
    // by the server's 400, a round trip for something checkable locally.
    const capacityValue = Number(capacityTrimmed);
    if (
      !/^\d+$/.test(capacityTrimmed) ||
      capacityValue <= 0 ||
      capacityValue > MAX_WORKSHOP_CAPACITY
    ) {
      return { error: `Capacity must be a whole number between 1 and ${MAX_WORKSHOP_CAPACITY}.` };
    }
    capacity = capacityValue;
  }

  const signupUrl = fields.signupUrl.trim();
  if (signupUrl !== '' && !isHttpUrl(signupUrl)) {
    return { error: 'Signup URL must start with http:// or https://.' };
  }

  const coverImage = fields.coverImage.trim();
  if (coverImage !== '' && !isSafeCoverImage(coverImage)) {
    return {
      error:
        'Cover image must be a site-relative path starting with "/" (e.g. "/assets/workshops/soldering.jpg") -- an external URL is not accepted.',
    };
  }

  return {
    title,
    // #0139: always the trimmed value, `''` when blank -- never `undefined`.
    // See ValidatedWorkshopFields's doc comment for why.
    summary: fields.summary.trim(),
    body_md: fields.bodyMd.trim(),
    starts_at: startsAt ?? '',
    ends_at: endsAt ?? '',
    location_name: fields.locationName.trim(),
    location_address: fields.locationAddress.trim(),
    location_note: fields.locationNote.trim(),
    // #0146: `null` when blank (never `undefined`) -- see this function's
    // doc comment and ValidatedWorkshopFields.capacity's comment for why
    // capacity's clear sentinel is `null` rather than #0139's `''`.
    capacity,
    signup_url: signupUrl,
    cover_image: coverImage,
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
