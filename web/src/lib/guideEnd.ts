// Pure helpers backing GuideEnd.svelte (#0662): the upcoming-events block and
// the pre-checked newsletter topics at the end of every guide page. Kept out
// of the component so the ordering and the pre-check rule are unit-testable
// without a DOM (AGENTS.md §1).
import { workshopGuideForEventSlug, type GuideKind } from './guides';
import { isCanceled } from './workshops';
import type { PublicWorkshop, WorkshopsListResponse } from './types';

/** How many events a guide page lists. */
export const GUIDE_EVENTS_LIMIT = 3;

export const GUIDE_EVENTS_HEADING = 'Upcoming events';
export const GUIDE_EVENTS_EMPTY =
  "Nothing scheduled right now. Join the newsletter below and we'll tell you when the next one is announced.";
export const GUIDE_EVENTS_LINK = 'See all events →';
export const GUIDE_SIGNUP_HEADING = 'Get the newsletter';
export const GUIDE_SIGNUP_INTRO =
  'New guides and upcoming workshops by email. Pick the topics you want, and unsubscribe any time.';

/**
 * Whether an event is "related" to a guide page. On a workshop guide it is a
 * run of that workshop (`programming-leds`, `programming-leds-2`, ...); on a
 * topic guide it is a run of any workshop that has a guide. The single rule
 * both the events list and the interest pre-check use, so they cannot drift.
 */
export function isRelatedGuideEvent(e: Pick<PublicWorkshop, 'slug'>, kind: GuideKind, slug: string): boolean {
  const g = workshopGuideForEventSlug(e.slug);
  if (kind === 'workshop') return g?.slug === slug;
  return g !== undefined;
}

/** Up to `limit` non-canceled upcoming events: related ones first, then the
 * others, each group in the server's (chronological) order. */
export function guideUpcomingEvents(
  list: Pick<WorkshopsListResponse, 'upcoming'>,
  kind: GuideKind,
  slug: string,
  limit: number = GUIDE_EVENTS_LIMIT,
): PublicWorkshop[] {
  const live = list.upcoming.filter((e) => !isCanceled(e));
  const related = live.filter((e) => isRelatedGuideEvent(e, kind, slug));
  const others = live.filter((e) => !isRelatedGuideEvent(e, kind, slug));
  return [...related, ...others].slice(0, limit);
}

/**
 * The interest slugs a guide's signup form pre-checks (#0662 decision 7): the
 * union of the related upcoming events' interests (canceled runs included),
 * deduped in first-seen order; failing that, the most recent related past
 * event's; failing that, none.
 */
export function guidePreselectInterests(list: WorkshopsListResponse, kind: GuideKind, slug: string): string[] {
  const related = list.upcoming.filter((e) => isRelatedGuideEvent(e, kind, slug));
  let sources: PublicWorkshop[] = related;
  if (sources.length === 0) {
    const past = list.past.find((e) => isRelatedGuideEvent(e, kind, slug));
    sources = past ? [past] : [];
  }
  const out: string[] = [];
  for (const e of sources) {
    for (const it of e.interests ?? []) {
      if (!out.includes(it.slug)) out.push(it.slug);
    }
  }
  return out;
}
