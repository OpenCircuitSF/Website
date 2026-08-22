// Unit tests for the campaign compose screen's audience panel logic:
// staleness keying, count/sample presentation, and error message shaping.

import { describe, it, expect } from 'vitest';
// Vite's `?raw` suffix imports the file's source as a plain string, so this
// grep-style assertion needs no Node `fs`/`url` types — this SPA's
// tsconfig deliberately excludes @types/node (browser-only code).
import audienceSource from './audience.ts?raw';
import { ApiError } from './api';
import type { AudiencePreviewResponse } from './types';
import {
  audienceRequestKey,
  isStaleAudience,
  describeAudience,
  sampleIsTruncated,
  sampleCaption,
  audienceErrorMessage,
} from './audience';

function preview(fields: Partial<AudiencePreviewResponse>): AudiencePreviewResponse {
  return { mode: 'all', interest_ids: [], count: 0, sample: [], warnings: [], ...fields };
}

describe('audienceRequestKey', () => {
  it('is order-independent over interest ids', () => {
    expect(audienceRequestKey('any_of', [3, 1, 2])).toBe(audienceRequestKey('any_of', [1, 2, 3]));
  });

  it('distinguishes the four modes', () => {
    const keys = new Set([
      audienceRequestKey('all', []),
      audienceRequestKey('any_of', []),
      audienceRequestKey('all_of', []),
      audienceRequestKey('none_selected', []),
    ]);
    expect(keys.size).toBe(4);
  });
});

describe('isStaleAudience', () => {
  it('drops a superseded response', () => {
    const older = audienceRequestKey('any_of', [1]);
    const current = audienceRequestKey('any_of', [1, 2]);
    expect(isStaleAudience(older, current)).toBe(true);
  });

  it('accepts a current response', () => {
    const key = audienceRequestKey('any_of', [1, 2]);
    expect(isStaleAudience(key, key)).toBe(false);
  });
});

describe('describeAudience', () => {
  it('0 recipients', () => {
    expect(describeAudience(preview({ count: 0 }))).toBe('No recipients match this audience.');
  });
  it('1 recipient (singular)', () => {
    expect(describeAudience(preview({ count: 1 }))).toBe('1 recipient');
  });
  it('many recipients', () => {
    expect(describeAudience(preview({ count: 482 }))).toBe('482 recipients');
  });
});

describe('sampleIsTruncated / sampleCaption', () => {
  it('truncated when sample.length < count', () => {
    const p = preview({ count: 482, sample: Array.from({ length: 20 }, (_, i) => `s${i}@example.com`) });
    expect(sampleIsTruncated(p)).toBe(true);
    expect(sampleCaption(p)).toBe('Showing 20 of 482');
  });

  it('not truncated when sample.length equals count', () => {
    const p = preview({ count: 3, sample: ['a@example.com', 'b@example.com', 'c@example.com'] });
    expect(sampleIsTruncated(p)).toBe(false);
    expect(sampleCaption(p)).toBe('Showing 3 of 3');
  });

  it('the module source contains no literal 20', () => {
    expect(/\b20\b/.test(audienceSource)).toBe(false);
  });
});

describe('audienceErrorMessage', () => {
  it("returns an ApiError's message verbatim", () => {
    const err = new ApiError(409, 'this audience mode requires at least one interest to be selected', {
      error: 'this audience mode requires at least one interest to be selected',
    });
    expect(audienceErrorMessage(err)).toBe('this audience mode requires at least one interest to be selected');
  });

  it('returns a generic string for a non-ApiError', () => {
    expect(audienceErrorMessage(new TypeError('boom'))).toMatch(/could not load/i);
  });
});
