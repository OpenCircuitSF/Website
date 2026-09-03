// #0393: the admin CRT-session CRUD screen's logic, kept in a plain
// TypeScript module so it is unit-testable without a DOM (CLAUDE.md §1) —
// CrtCommands.svelte stays markup and wiring only, mirroring lib/admin.ts's
// interests taxonomy section (sortedInterests/reorderSwap/isValidInterestSlug)
// this file is deliberately modelled on.

import type { CrtCommand } from './types';
import { CRT_LINE_CHARS } from './crtScreen';

export { CRT_LINE_CHARS };

/** #0403: shown after a successful create/update/delete/reorder/active-toggle
 *  in the CRT session admin section, via CrtCommands.svelte's `saveNote`
 *  role="status" region. Two independent 60s cache windows sit between a
 *  save and the change actually reaching a home-page visitor -- the
 *  `GET /api/crt-session` server-side in-process cache (#0393) and that
 *  response's own `Cache-Control: max-age=60` in the browser -- and they do
 *  not align, so measured worst case is closer to two minutes than to one.
 *  "Up to a minute" is therefore the honest ceiling to name; a precise
 *  number like "in 60 seconds" would be wrong often enough to be its own
 *  annoyance. Naming the hard reload costs nothing and needs no change to
 *  either cache. Kept here, not inlined in the component, so the wording is
 *  one unit-tested string (CLAUDE.md §1). */
export const CRT_SAVE_NOTE =
  "Saved. It can take up to a minute to appear on the home page — if it still looks out of date, a hard reload bypasses your browser's own cached copy.";

// Mirrors internal/crt.ValidSlug / the crt_commands_slug_format CHECK
// constraint (internal/crt/store.go): lowercase alphanumerics separated by
// single hyphens, no leading, trailing, or doubled hyphens. Identical
// pattern to lib/admin.ts's INTEREST_SLUG_PATTERN.
const CRT_SLUG_PATTERN = /^[a-z0-9]+(-[a-z0-9]+)*$/;

/** Whether a string is a valid crt_commands slug per internal/crt.ValidSlug's rule. */
export function isValidCrtSlug(slug: string): boolean {
  return CRT_SLUG_PATTERN.test(slug);
}

/** The four values internal/crt.ValidSource accepts, labelled for the admin
 *  source `<select>` (#0393's Design §4: "the live sources labelled so it
 *  is clear the stored lines are a fallback"). Order matches the Design §1
 *  table. */
export const CRT_SOURCES: ReadonlyArray<{ value: string; label: string }> = [
  { value: 'static', label: 'Static (stored text only)' },
  { value: 'workshops', label: 'Live: upcoming workshops (fallback below)' },
  { value: 'list_stats', label: 'Live: subscriber counts (fallback below)' },
  { value: 'interests', label: 'Live: per-topic subscriber counts (fallback below)' },
];

/** Whether source is one of the four values CRT_SOURCES lists. */
export function isValidCrtSource(source: string): boolean {
  return CRT_SOURCES.some((s) => s.value === source);
}

/**
 * Validate a new-command form (slug + command + output) before submitting.
 * Trims all three; an invalid slug or an empty command/output yields an
 * `error` message, otherwise the trimmed values to submit. The server
 * independently enforces slug uniqueness (409 on a duplicate) — this only
 * catches what can be checked client-side, mirroring validateNewInterest.
 */
export function validateNewCrtCommand(
  slug: string,
  command: string,
  output: string,
): { slug: string; command: string; output: string } | { error: string } {
  const trimmedSlug = slug.trim();
  const trimmedCommand = command.trim();
  const trimmedOutput = output.trim();
  if (!isValidCrtSlug(trimmedSlug)) {
    return {
      error: 'Slug must be lowercase letters, numbers, and single hyphens (e.g. "workshops-next").',
    };
  }
  if (trimmedCommand === '') {
    return { error: 'Command is required.' };
  }
  if (trimmedOutput === '') {
    return { error: 'Output is required.' };
  }
  return { slug: trimmedSlug, command: trimmedCommand, output: trimmedOutput };
}

/** Commands ordered by sort_order then slug, matching the server's List
 *  ordering (internal/crt.Store.List). */
export function sortedCrtCommands(list: CrtCommand[]): CrtCommand[] {
  return [...list].sort((a, b) => a.sort_order - b.sort_order || a.slug.localeCompare(b.slug));
}

/** One command's id + the sort_order to write it to, half of a reorder
 *  swap. Mirrors lib/admin.ts's SortOrderTarget. */
export interface CrtSortOrderTarget {
  id: number;
  sortOrder: number;
}

/** A pair of sort_order writes that swaps two adjacent commands' positions. */
export interface CrtSortOrderSwap {
  moved: CrtSortOrderTarget;
  other: CrtSortOrderTarget;
}

/**
 * Compute the sort_order swap for moving the command with `id` one position
 * `direction` within `sorted` (already in display order — pass the result
 * of sortedCrtCommands). Returns null when `id` isn't found or is already
 * at that end of the list. The caller PATCHes both `moved` and `other` with
 * each other's current sort_order — the same shape lib/admin.ts's
 * reorderSwap already uses for the interests taxonomy screen, deliberately
 * not duplicated as a generic helper since the two operate on different
 * row types.
 */
export function crtReorderSwap(
  sorted: CrtCommand[],
  id: number,
  direction: 'up' | 'down',
): CrtSortOrderSwap | null {
  const idx = sorted.findIndex((c) => c.id === id);
  if (idx === -1) return null;
  const otherIdx = direction === 'up' ? idx - 1 : idx + 1;
  if (otherIdx < 0 || otherIdx >= sorted.length) return null;
  const moved = sorted[idx];
  const other = sorted[otherIdx];
  return {
    moved: { id: moved.id, sortOrder: other.sort_order },
    other: { id: other.id, sortOrder: moved.sort_order },
  };
}

/** A human label for a source value, for the read-only list view (the
 *  `<select>` itself uses CRT_SOURCES' own labels while editing). */
export function crtSourceLabel(source: string): string {
  return CRT_SOURCES.find((s) => s.value === source)?.label ?? source;
}

/** #0393 review-bounce follow-up: the 1-based line numbers within `output`
 *  (newline-separated, matching the crt_commands.output column and the admin
 *  `<textarea>`) whose length exceeds `max` (default CRT_LINE_CHARS -- the
 *  same 36-column budget crtTruncate enforces at render time on the home
 *  page). Trailing empty lines are ignored, matching Command.Lines() /
 *  crtSessionToScript's own "trim trailing empties" convention server- and
 *  client-side, so an admin isn't warned about blank padding at the end of
 *  the textarea. */
export function crtOverBudgetLines(output: string, max: number = CRT_LINE_CHARS): number[] {
  const lines = output.split('\n');
  while (lines.length && lines[lines.length - 1].trim() === '') lines.pop();
  const over: number[] = [];
  lines.forEach((line, i) => {
    if (line.length > max) over.push(i + 1);
  });
  return over;
}

/** A human warning naming which lines of `output` are over budget, or null
 *  when every line fits. Kept here rather than inlined in the component's
 *  markup so the phrasing is one unit-tested string (CLAUDE.md §1:
 *  components stay thin). Line numbers are 1-based, matching how an admin
 *  counts lines in the textarea. */
export function crtOverBudgetWarning(output: string, max: number = CRT_LINE_CHARS): string | null {
  const over = crtOverBudgetLines(output, max);
  if (!over.length) return null;
  const noun = over.length === 1 ? 'Line' : 'Lines';
  const verb = over.length === 1 ? 'is' : 'are';
  return `${noun} ${over.join(', ')} ${verb} over the ${max}-character CRT width and will be truncated on the glass.`;
}
