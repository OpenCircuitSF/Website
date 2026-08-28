// Pure, framework-free helpers backing the public campaign archive
// (#0123, PRD §6.8): ArchiveIndex.svelte's index-row date and
// ArchiveEntry.svelte's detail-page date. Same "nothing in a .svelte file
// is covered by a test" reasoning as lib/workshops.ts (#0094) — see that
// module's own doc comment.

import type { ArchiveEntry } from './types';

const DATE_FMT: Intl.DateTimeFormatOptions = { year: 'numeric', month: 'short', day: 'numeric' };

/**
 * Format an archived_at RFC 3339 timestamp as a human date (e.g.
 * "Aug 27, 2026") in the viewer's local timezone — same
 * `toLocaleDateString` convention lib/workshops.ts's formatWorkshopDate
 * uses. Missing or unparseable input returns "" (never a placeholder like
 * workshops' "Date TBA" — an archived campaign without an archived_at is
 * not an expected state worth its own copy, unlike a not-yet-scheduled
 * workshop).
 */
export function formatArchivedDate(archivedAt: string | null | undefined): string {
  if (!archivedAt) return '';
  const d = new Date(archivedAt);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleDateString(undefined, DATE_FMT);
}

/** Whether the archive index should render its empty state — no campaign has ever been sent. */
export function hasNoArchiveEntries(list: Pick<{ archive: ArchiveEntry[] }, 'archive'>): boolean {
  return list.archive.length === 0;
}
