// Unit tests for the campaign compose screen's pre-send-checks parser.
// Every case here is chosen to prove the module holds NO code→message
// table: an entry's message is always rendered verbatim, an unknown code
// survives with its own message, and a blank message falls back to the raw
// code rather than being invented or dropped.

import { describe, it, expect } from 'vitest';
import { ApiError } from './api';
import { parseUnmet, parseUnmetFromError, isPreflightBlocked, fixLocation } from './preflight';

describe('parseUnmet', () => {
  it('parses a 200 advisory body', () => {
    const body = { ok: false, unmet: [{ code: 'subject_empty', message: 'Subject is empty.' }] };
    expect(parseUnmet(body)).toEqual([{ code: 'subject_empty', message: 'Subject is empty.' }]);
  });

  it('parses an ApiError.body from a 409', () => {
    const err = new ApiError(409, 'unmet', { unmet: [{ code: 'no_test_send', message: 'No test send.' }] });
    expect(parseUnmet(err.body)).toEqual([{ code: 'no_test_send', message: 'No test send.' }]);
  });

  it('{unmet:[]} parses to an empty array, not null', () => {
    expect(parseUnmet({ unmet: [] })).toEqual([]);
  });

  it('a malformed body parses to null', () => {
    expect(parseUnmet({ error: 'something else' })).toBeNull();
    expect(parseUnmet('a string')).toBeNull();
    expect(parseUnmet(null)).toBeNull();
    expect(parseUnmet(undefined)).toBeNull();
  });

  it('renders an entry verbatim — a client table would fail this', () => {
    const body = {
      unmet: [{ code: 'physical_address_missing', message: 'ENTIRELY DIFFERENT SERVER TEXT' }],
    };
    expect(parseUnmet(body)).toEqual([
      { code: 'physical_address_missing', message: 'ENTIRELY DIFFERENT SERVER TEXT' },
    ]);
  });

  it('an unknown code survives with its message', () => {
    const body = { unmet: [{ code: 'future_code', message: 'A brand new requirement.' }] };
    expect(parseUnmet(body)).toEqual([{ code: 'future_code', message: 'A brand new requirement.' }]);
    expect(fixLocation('future_code')).toBeNull();
  });

  it('a blank/absent message falls back to the raw code — never dropped, never invented', () => {
    const body = {
      unmet: [
        { code: 'blank_message_code', message: '' },
        { code: 'absent_message_code' },
      ],
    };
    expect(parseUnmet(body)).toEqual([
      { code: 'blank_message_code', message: 'blank_message_code' },
      { code: 'absent_message_code', message: 'absent_message_code' },
    ]);
  });

  it('preserves order exactly as received, including a deliberately out-of-canonical-order fixture', () => {
    const body = {
      unmet: [
        { code: 'mailer_unavailable', message: 'm' },
        { code: 'subject_empty', message: 's' },
        { code: 'reply_to_missing', message: 'r' },
      ],
    };
    expect(parseUnmet(body)?.map((f) => f.code)).toEqual([
      'mailer_unavailable',
      'subject_empty',
      'reply_to_missing',
    ]);
  });
});

describe('parseUnmetFromError', () => {
  it('extracts the unmet list from an ApiError', () => {
    const err = new ApiError(409, 'unmet', { unmet: [{ code: 'no_test_send', message: 'No test send.' }] });
    expect(parseUnmetFromError(err)).toEqual([{ code: 'no_test_send', message: 'No test send.' }]);
  });

  it('returns null for a non-ApiError', () => {
    expect(parseUnmetFromError(new Error('network down'))).toBeNull();
  });
});

describe('isPreflightBlocked', () => {
  it('false for an empty list', () => {
    expect(isPreflightBlocked([])).toBe(false);
  });
  it('true for a non-empty list', () => {
    expect(isPreflightBlocked([{ code: 'subject_empty', message: 'x' }])).toBe(true);
  });
});

describe('fixLocation', () => {
  it('maps physical_address_missing to the Settings subtab', () => {
    expect(fixLocation('physical_address_missing')).toEqual({ section: 'settings', label: 'Go to Settings' });
  });

  it('returns null for a code it does not know', () => {
    expect(fixLocation('future_code')).toBeNull();
  });
});
