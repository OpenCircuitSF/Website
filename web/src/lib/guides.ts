// The guide catalog: every student workshop guide (/workshops/{slug}) and
// topic guide (/guides/{slug}), read from web/src/content/guides/guides.json.
// The Go server embeds the same file (internal/guides), so the route lists,
// SEO meta and sitemap on both sides come from one copy.
import catalog from '../content/guides/guides.json';

export interface GuideMeta {
  slug: string;
  title: string;
  /** One line: the hook on cards and in social posts. */
  hook: string;
  /** Plain-text meta description when the hook is not suitable for one. */
  description?: string;
  tags: string[];
  /** Which guide set it belongs to (GUIDE_SETS), for /guides' grouping. */
  set: string;
}

/** A set of related guides, shown as one group on /guides. */
export interface GuideSet {
  id: string;
  title: string;
  blurb: string;
}

export interface TopicMeta extends GuideMeta {
  minutes: number;
  /** Suggested text for a social post (the link is appended). */
  post: string;
}

export type GuideKind = 'workshop' | 'topic';

export const GUIDE_SETS: readonly GuideSet[] = catalog.sets;
export const WORKSHOP_GUIDES: readonly GuideMeta[] = catalog.workshopGuides;
export const TOPIC_GUIDES: readonly TopicMeta[] = catalog.topics;

export function topicGuideBySlug(slug: string): TopicMeta | undefined {
  return TOPIC_GUIDES.find((g) => g.slug === slug);
}

export function guidePath(kind: GuideKind, slug: string): string {
  return kind === 'workshop' ? `/workshops/${slug}` : `/guides/${slug}`;
}

/**
 * The workshop guide an event links to as its "Student guide" (#0656): the
 * event's slug is the guide's slug, or the guide's slug plus a numbered
 * repeat suffix (`programming-leds-2`), which is how the workshop store's
 * slugify names a second run of the same title.
 */
export function workshopGuideForEventSlug(eventSlug: string): GuideMeta | undefined {
  return WORKSHOP_GUIDES.find((g) => {
    if (eventSlug === g.slug) return true;
    if (!eventSlug.startsWith(g.slug + '-')) return false;
    return /^\d+$/.test(eventSlug.slice(g.slug.length + 1));
  });
}

// #0710: the /guides card image, served by Go (internal/seo/guidethumb.go,
// whose guideThumbW/guideThumbH constants these dimensions mirror).
export const GUIDE_THUMB = { width: 640, height: 336 } as const;

export function guideThumbPath(slug: string): string {
  return `/guides/${slug}/thumb.jpg`;
}
