import { describe, expect, it } from 'vitest';
import catalog from '../content/guides/guides.json';
import { TOPIC_GUIDES, WORKSHOP_GUIDES, guidePath, guideThumbPath, topicGuideBySlug, workshopGuideForEventSlug } from './guides';
import { TOPIC_GUIDE_SLUGS, WORKSHOP_GUIDE_SLUGS, parsePath } from './router';
import { formatTitle, titleForRoute } from './pageTitle';

describe('guide catalog (#0656)', () => {
  it('router slug sets are exactly the catalog slugs', () => {
    expect([...WORKSHOP_GUIDE_SLUGS].sort()).toEqual(catalog.workshopGuides.map((g) => g.slug).sort());
    expect([...TOPIC_GUIDE_SLUGS].sort()).toEqual(catalog.topics.map((g) => g.slug).sort());
  });

  it('slugs are unique across both kinds and URL-safe', () => {
    const all = [...WORKSHOP_GUIDES, ...TOPIC_GUIDES].map((g) => g.slug);
    expect(new Set(all).size).toBe(all.length);
    for (const slug of all) expect(slug).toMatch(/^[a-z0-9]+(-[a-z0-9]+)*$/);
  });

  it('matches an event to its workshop guide by slug or numbered repeat', () => {
    expect(workshopGuideForEventSlug('programming-leds')?.slug).toBe('programming-leds');
    expect(workshopGuideForEventSlug('programming-leds-2')?.slug).toBe('programming-leds');
    expect(workshopGuideForEventSlug('programming-leds-advanced')).toBeUndefined();
    expect(workshopGuideForEventSlug('programming-ledsx')).toBeUndefined();
    expect(workshopGuideForEventSlug('programming-leds-')).toBeUndefined();
    expect(workshopGuideForEventSlug('solder-101')).toBeUndefined();
  });

  it('workshop guide title matches the page heading', () => {
    expect(titleForRoute(parsePath('/workshops/programming-leds'))).toBe(formatTitle(WORKSHOP_GUIDES[0].title));
  });

  it('topic guide title is the topic title', () => {
    expect(titleForRoute(parsePath('/guides/baud-rate'))).toBe(formatTitle(topicGuideBySlug('baud-rate')!.title));
    expect(titleForRoute(parsePath('/guides'))).toBe(formatTitle('Guides'));
  });

  it('builds paths per kind', () => {
    expect(guidePath('workshop', 'programming-leds')).toBe('/workshops/programming-leds');
    expect(guidePath('topic', 'baud-rate')).toBe('/guides/baud-rate');
  });

  it('meta descriptions are plain text', () => {
    for (const g of [...WORKSHOP_GUIDES, ...TOPIC_GUIDES]) {
      // eslint-disable-next-line no-control-regex
      expect(g.description ?? g.hook).not.toMatch(/[\u0000-\u001f\u007f-\u009f­]/);
    }
  });

  it('every topic carries post copy', () => {
    for (const t of TOPIC_GUIDES) expect(t.post.trim()).not.toBe('');
    for (const w of WORKSHOP_GUIDES) expect('post' in w).toBe(false);
  });

  it('guideThumbPath points at the Go thumbnail route', () => {
    expect(guideThumbPath('baud-rate')).toBe('/guides/baud-rate/thumb.jpg');
  });
});
