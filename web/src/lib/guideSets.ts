// /guides grouping and filter (user request, 2026-11-14): the guides are
// shown in their sets (LEDs, Multimeter, Soldering), and a control at the top
// narrows the page to one set. The choice lives in the query string
// (?set=multimeter) so a filtered view can be linked to.
import { GUIDE_SETS, type GuideMeta, type GuideSet } from './guides';

export const ALL = 'all';

/** The set id from a query string value; anything unknown means all. */
export function setFromQuery(value: string | null | undefined): string {
  return value && GUIDE_SETS.some((s) => s.id === value) ? value : ALL;
}

/** The URL for a filter choice: /guides, or /guides?set=<id>. */
export function hrefForSet(id: string): string {
  return id === ALL ? '/guides' : `/guides?set=${encodeURIComponent(id)}`;
}

export interface SetGroup<T extends GuideMeta> {
  set: GuideSet;
  guides: T[];
}

/** The sets to show, in catalog order, each with its guides; empty sets are left out. */
export function groupBySet<T extends GuideMeta>(guides: readonly T[], filter: string): SetGroup<T>[] {
  return GUIDE_SETS.filter((s) => filter === ALL || s.id === filter)
    .map((set) => ({ set, guides: guides.filter((g) => g.set === set.id) }))
    .filter((g) => g.guides.length > 0);
}

/** How many guides each filter choice shows, for the control's labels. */
export function countsBySet(guides: readonly GuideMeta[]): Record<string, number> {
  const counts: Record<string, number> = { [ALL]: guides.length };
  for (const s of GUIDE_SETS) counts[s.id] = guides.filter((g) => g.set === s.id).length;
  return counts;
}
