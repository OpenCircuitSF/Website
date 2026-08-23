import { describe, it, expect } from 'vitest';
import {
  workshopLocationLines,
  hasAnyLocationInfo,
  workshopPreselectSlugs,
  hasExternalSignup,
  COVER_IMAGE_WIDTH,
  COVER_IMAGE_HEIGHT,
} from './workshopDetail';
import type { PublicWorkshop } from './types';

function workshop(fields: Partial<PublicWorkshop> = {}): PublicWorkshop {
  return {
    slug: 'soldering-101',
    title: 'Soldering fundamentals',
    status: 'published',
    interests: [],
    ...fields,
  };
}

describe('workshopLocationLines', () => {
  it('returns [] when nothing is set', () => {
    expect(workshopLocationLines(workshop())).toEqual([]);
  });

  it('includes only location_name when it is the only field set', () => {
    expect(workshopLocationLines(workshop({ location_name: 'The Workshop' }))).toEqual([
      'The Workshop',
    ]);
  });

  it('orders name, then address, then note', () => {
    expect(
      workshopLocationLines(
        workshop({
          location_name: 'The Workshop',
          location_address: '123 Main St, San Francisco, CA',
          location_note: 'Enter through the loading dock.',
        }),
      ),
    ).toEqual(['The Workshop', '123 Main St, San Francisco, CA', 'Enter through the loading dock.']);
  });

  it('omits address but keeps name and note when address is unset', () => {
    expect(
      workshopLocationLines(
        workshop({ location_name: 'The Workshop', location_note: 'Ring the bell.' }),
      ),
    ).toEqual(['The Workshop', 'Ring the bell.']);
  });

  it('treats a whitespace-only field as unset', () => {
    expect(
      workshopLocationLines(
        workshop({ location_name: '   ', location_address: '123 Main St' }),
      ),
    ).toEqual(['123 Main St']);
  });
});

describe('hasAnyLocationInfo', () => {
  it('is false when no location fields are set', () => {
    expect(hasAnyLocationInfo(workshop())).toBe(false);
  });

  it('is true when only location_note is set', () => {
    expect(hasAnyLocationInfo(workshop({ location_note: 'Parking in the back.' }))).toBe(true);
  });
});

describe('workshopPreselectSlugs', () => {
  it('returns [] for a workshop with no interests', () => {
    expect(workshopPreselectSlugs(workshop())).toEqual([]);
  });

  it('returns every interest slug, in order', () => {
    const w = workshop({
      interests: [
        { slug: 'soldering', name: 'Soldering & Assembly', sort_order: 0 },
        { slug: 'pcb-design', name: 'PCB Design', sort_order: 1 },
      ],
    });
    expect(workshopPreselectSlugs(w)).toEqual(['soldering', 'pcb-design']);
  });
});

describe('hasExternalSignup', () => {
  it('is false when signup_url is absent', () => {
    expect(hasExternalSignup(workshop())).toBe(false);
  });

  it('is false when signup_url is an empty or whitespace-only string', () => {
    expect(hasExternalSignup(workshop({ signup_url: '' }))).toBe(false);
    expect(hasExternalSignup(workshop({ signup_url: '   ' }))).toBe(false);
  });

  it('is true when signup_url is a non-empty string', () => {
    expect(hasExternalSignup(workshop({ signup_url: 'https://example.com/rsvp' }))).toBe(true);
  });

  // #0054's phase-3 review: signup_url reached `href` with no scheme
  // allowlist. hasExternalSignup now reuses linkSafety.ts's isSafeLinkHref
  // (#0052) so an unsafe scheme never renders as the external action.
  it('is false when signup_url is a javascript: URL', () => {
    expect(hasExternalSignup(workshop({ signup_url: 'javascript:alert(1)' }))).toBe(false);
  });

  it('is false when signup_url is a data: URL', () => {
    expect(
      hasExternalSignup(workshop({ signup_url: 'data:text/html,<script>alert(1)</script>' })),
    ).toBe(false);
  });

  it('is true when signup_url is a root-relative path', () => {
    expect(hasExternalSignup(workshop({ signup_url: '/rsvp' }))).toBe(true);
  });
});

describe('cover image dimensions', () => {
  it('are fixed, non-zero constants', () => {
    expect(COVER_IMAGE_WIDTH).toBeGreaterThan(0);
    expect(COVER_IMAGE_HEIGHT).toBeGreaterThan(0);
  });
});
