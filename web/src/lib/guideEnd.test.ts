import { describe, expect, it } from 'vitest';
import { GUIDE_EVENTS_LIMIT, guidePreselectInterests, guideUpcomingEvents, isRelatedGuideEvent } from './guideEnd';
import type { PublicWorkshop, WorkshopsListResponse } from './types';

function ev(slug: string, interests: string[] = [], status = 'published'): PublicWorkshop {
  return {
    slug,
    title: slug,
    status,
    interests: interests.map((s) => ({ slug: s, name: s })),
    event_type: { slug: 'workshop', name: 'Workshop', sort_order: 10 },
  } as PublicWorkshop;
}

function slugs(list: PublicWorkshop[]): string[] {
  return list.map((e) => e.slug);
}

describe('isRelatedGuideEvent', () => {
  it('matches programming-leds and programming-leds-2 but not programming-leds-extra', () => {
    expect(isRelatedGuideEvent(ev('programming-leds'), 'workshop', 'programming-leds')).toBe(true);
    expect(isRelatedGuideEvent(ev('programming-leds-2'), 'workshop', 'programming-leds')).toBe(true);
    expect(isRelatedGuideEvent(ev('programming-leds-extra'), 'workshop', 'programming-leds')).toBe(false);
    expect(isRelatedGuideEvent(ev('hack-night'), 'workshop', 'programming-leds')).toBe(false);
  });

  it('matches any guided workshop run on a topic guide', () => {
    expect(isRelatedGuideEvent(ev('programming-leds-3'), 'topic', 'delay-vs-millis')).toBe(true);
    expect(isRelatedGuideEvent(ev('hack-night'), 'topic', 'delay-vs-millis')).toBe(false);
  });
});

describe('guideUpcomingEvents', () => {
  it('puts runs of the workshop first on its own guide page', () => {
    const list = { upcoming: [ev('hack-night'), ev('programming-leds-2')] };
    expect(slugs(guideUpcomingEvents(list, 'workshop', 'programming-leds'))).toEqual(['programming-leds-2', 'hack-night']);
  });

  it('treats any guided workshop run as related on a topic guide', () => {
    const list = { upcoming: [ev('hack-night'), ev('programming-leds')] };
    expect(slugs(guideUpcomingEvents(list, 'topic', 'delay-vs-millis'))).toEqual(['programming-leds', 'hack-night']);
  });

  it('fills with other upcoming events after the related ones, keeping server order', () => {
    const list = { upcoming: [ev('a'), ev('programming-leds-2'), ev('b'), ev('programming-leds-3')] };
    expect(slugs(guideUpcomingEvents(list, 'workshop', 'programming-leds', 10))).toEqual([
      'programming-leds-2',
      'programming-leds-3',
      'a',
      'b',
    ]);
  });

  it('drops canceled events', () => {
    const list = { upcoming: [ev('programming-leds-2', [], 'canceled'), ev('a')] };
    expect(slugs(guideUpcomingEvents(list, 'workshop', 'programming-leds'))).toEqual(['a']);
  });

  it('caps at GUIDE_EVENTS_LIMIT', () => {
    const list = { upcoming: [ev('a'), ev('b'), ev('c'), ev('d'), ev('e')] };
    expect(guideUpcomingEvents(list, 'topic', 'delay-vs-millis')).toHaveLength(GUIDE_EVENTS_LIMIT);
  });

  it('returns [] for an empty upcoming list', () => {
    expect(guideUpcomingEvents({ upcoming: [] }, 'topic', 'delay-vs-millis')).toEqual([]);
  });
});

describe('guidePreselectInterests', () => {
  const list = (upcoming: PublicWorkshop[], past: PublicWorkshop[] = []): WorkshopsListResponse => ({ upcoming, past });

  it('unions the interests of related upcoming events in first-seen order, deduped', () => {
    const l = list([ev('programming-leds-2', ['microcontrollers', 'beginner']), ev('programming-leds-3', ['beginner', 'leds'])]);
    expect(guidePreselectInterests(l, 'workshop', 'programming-leds')).toEqual(['microcontrollers', 'beginner', 'leds']);
  });

  it('ignores the interests of unrelated events', () => {
    const l = list([ev('hack-night', ['homelab']), ev('programming-leds-2', ['microcontrollers'])]);
    expect(guidePreselectInterests(l, 'workshop', 'programming-leds')).toEqual(['microcontrollers']);
  });

  it('uses guided workshop runs on a topic guide', () => {
    const l = list([ev('hack-night', ['homelab']), ev('programming-leds', ['microcontrollers'])]);
    expect(guidePreselectInterests(l, 'topic', 'delay-vs-millis')).toEqual(['microcontrollers']);
  });

  it('includes a canceled related run', () => {
    const l = list([ev('programming-leds-2', ['microcontrollers'], 'canceled')]);
    expect(guidePreselectInterests(l, 'workshop', 'programming-leds')).toEqual(['microcontrollers']);
  });

  it('falls back to the most recent related past event when none is upcoming', () => {
    const l = list([ev('hack-night', ['homelab'])], [ev('programming-leds-3', ['leds']), ev('programming-leds-2', ['old'])]);
    expect(guidePreselectInterests(l, 'workshop', 'programming-leds')).toEqual(['leds']);
  });

  it('returns [] when no related event exists', () => {
    expect(guidePreselectInterests(list([ev('hack-night', ['homelab'])]), 'topic', 'delay-vs-millis')).toEqual([]);
  });
});
