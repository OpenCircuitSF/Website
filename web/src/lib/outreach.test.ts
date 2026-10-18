import { describe, expect, it } from 'vitest';
import {
  LINK_ESTIMATE,
  channelStatusNote,
  composerActions,
  countCharacters,
  defaultScheduleAt,
  scheduleHint,
  fromLocalInputValue,
  isOverLimit,
  shouldPoll,
  toLocalInputValue,
  unavailableMessage,
  utmChips,
  validateRemoteUrl,
} from './outreach';
import type { ComposerActions } from './outreach';
import type { OutreachChannel, OutreachPost, OutreachWave, WorkshopOutreach } from './types';

const bluesky: OutreachChannel = { name: 'bluesky', label: 'Bluesky', utm_source: 'bluesky', utm_medium: 'social', char_limit: 300, automatic: true, configured: true, account: '' };
const mastodon: OutreachChannel = { name: 'mastodon', label: 'Mastodon', utm_source: 'mastodon', utm_medium: 'social', char_limit: 500, automatic: true, configured: false, account: '' };
const instagram: OutreachChannel = { name: 'instagram', label: 'Instagram', utm_source: 'instagram', utm_medium: 'social', char_limit: 0, automatic: false, configured: false, account: '' };

function post(over: Partial<OutreachPost> = {}): OutreachPost {
  return {
    id: 1, outreach_id: 1, channel: 'bluesky', wave: 'announce', body: 'x', status: 'draft', prepared: false, stale: false, customized: false, held: false,
    short_link_key: null, short_url: null, destination_url: 'https://x/workshops/s?utm_source=bluesky',
    utm: { source: 'bluesky', medium: 'social', campaign: 's', content: 'announce' },
    scheduled_at: null, posted_at: null, remote_url: null, last_error: null, created_at: '2026-01-01T00:00:00Z', updated_at: null,
    ...over,
  };
}

describe('countCharacters', () => {
  it('counts a ZWJ emoji and a combining sequence as one grapheme each on bluesky', () => {
    expect(countCharacters(bluesky, '\u{1F468}‍\u{1F469}‍\u{1F467}', true)).toEqual({ count: 1, exact: true });
    expect(countCharacters(bluesky, 'é', true)).toEqual({ count: 1, exact: true });
  });
  it('estimates an unprepared link and marks the count inexact', () => {
    expect(countCharacters(bluesky, 'Sign up: {link}', false)).toEqual({ count: 'Sign up: '.length + LINK_ESTIMATE, exact: false });
  });
  it('is exact when prepared or when there is no token', () => {
    expect(countCharacters(bluesky, 'abc', false).exact).toBe(true);
    expect(countCharacters(bluesky, 'abc https://go.example/k', true)).toEqual({ count: 'abc https://go.example/k'.length, exact: true });
  });
  it('counts every mastodon URL as 23 regardless of length', () => {
    const long = 'https://go.opencircuitsf.com/' + 'a'.repeat(80);
    expect(countCharacters(mastodon, `Hi ${long}`, true)).toEqual({ count: 3 + 23, exact: true });
  });
  it('counts a mastodon unprepared token as 23 and inexact', () => {
    expect(countCharacters(mastodon, 'Hi {link}', false)).toEqual({ count: 3 + 23, exact: false });
  });
});

describe('isOverLimit', () => {
  it('compares against the limit', () => {
    expect(isOverLimit(bluesky, 300)).toBe(false);
    expect(isOverLimit(bluesky, 301)).toBe(true);
  });
  it('never fires for a limit of 0', () => {
    expect(isOverLimit(instagram, 100000)).toBe(false);
  });
});

describe('composerActions', () => {
  const none = { canPostNow: false, canRetry: false, canSchedule: false, canUnschedule: false };
  it('draft on a manual channel, available', () => {
    expect(composerActions(post(), instagram, true)).toEqual({ canSave: true, canRegenerate: false, canPrepare: true, canCopy: false, canMarkPosted: false, canDelete: true, ...none });
  });
  it('draft, unavailable cannot prepare', () => {
    expect(composerActions(post(), instagram, false).canPrepare).toBe(false);
  });
  it('stale offers regenerate', () => {
    expect(composerActions(post({ stale: true }), instagram, true).canRegenerate).toBe(true);
  });
  it('prepared manual can copy and mark posted but not prepare or send', () => {
    expect(composerActions(post({ prepared: true, status: 'manual' }), instagram, true)).toEqual({ canSave: true, canRegenerate: false, canPrepare: false, canCopy: true, canMarkPosted: true, canDelete: true, ...none });
  });
  it('posted is locked', () => {
    expect(composerActions(post({ prepared: true, status: 'posted', stale: true }), bluesky, true)).toEqual({ canSave: false, canRegenerate: false, canPrepare: false, canCopy: false, canMarkPosted: false, canDelete: false, ...none });
  });

  // #0545: the matrix for an automatic channel, by status, configuration and availability.
  type Row = [status: OutreachPost['status'], configured: boolean, available: boolean, expected: Partial<ComposerActions>];
  const rows: Row[] = [
    ['draft', true, true, { canPostNow: true, canSchedule: true, canMarkPosted: true, canSave: true, canDelete: true }],
    ['draft', true, false, { canMarkPosted: true, canSave: true, canDelete: true }],
    ['draft', false, true, { canMarkPosted: true, canSave: true, canDelete: true }],
    ['scheduled', true, true, { canPostNow: true, canSchedule: true, canUnschedule: true, canSave: true, canDelete: true }],
    ['scheduled', false, true, { canUnschedule: true, canMarkPosted: true, canSave: true, canDelete: true }],
    ['queued', true, true, { canCopy: true }],
    ['queued', false, true, { canCopy: true }],
    ['failed', true, true, { canRetry: true, canSchedule: true, canMarkPosted: true, canSave: true, canDelete: true }],
    ['failed', true, false, { canMarkPosted: true, canSave: true, canDelete: true }],
    ['failed', false, true, { canMarkPosted: true, canSave: true, canDelete: true }],
    ['posted', true, true, {}],
  ];
  for (const [status, configured, available, expected] of rows) {
    it(`prepared bluesky post, ${status}, configured=${configured}, available=${available}`, () => {
      const ch = { ...bluesky, configured };
      const got = composerActions(post({ prepared: true, status }), ch, available);
      const want: ComposerActions = {
        canSave: false, canRegenerate: false, canPrepare: false, canCopy: status !== 'posted', canMarkPosted: false, canDelete: false,
        canPostNow: false, canRetry: false, canSchedule: false, canUnschedule: false,
        ...expected,
      };
      expect(got).toEqual(want);
    });
  }
  it('a queued post locks every mutating action', () => {
    const a = composerActions(post({ prepared: true, status: 'queued', stale: true }), bluesky, true);
    expect([a.canSave, a.canRegenerate, a.canMarkPosted, a.canDelete, a.canPostNow, a.canRetry, a.canSchedule, a.canUnschedule]).toEqual(Array(8).fill(false));
  });
  it('an unprepared automatic post cannot be sent or scheduled', () => {
    const a = composerActions(post({ prepared: false }), bluesky, true);
    expect(a.canPostNow || a.canSchedule).toBe(false);
    expect(a.canPrepare).toBe(true);
  });
});

describe('defaultScheduleAt', () => {
  const now = new Date('2026-11-01T00:00:00Z');
  const waves: OutreachWave[] = [
    { name: 'announce', label: 'Announce', suggested_at: null },
    { name: 'reminder', label: 'Reminder', suggested_at: '2026-11-28T18:00:00Z' },
    { name: 'last-call', label: 'Last call', suggested_at: '2026-12-04T18:00:00Z' },
  ];
  it('announce has no default', () => {
    expect(defaultScheduleAt('announce', waves, now)).toBeNull();
    expect(defaultScheduleAt('announce', [{ ...waves[0], suggested_at: '2026-12-01T00:00:00Z' }], now)).toBeNull();
  });
  it('reminder and last-call use the suggestion', () => {
    expect(defaultScheduleAt('reminder', waves, now)).toBe('2026-11-28T18:00:00Z');
    expect(defaultScheduleAt('last-call', waves, now)).toBe('2026-12-04T18:00:00Z');
  });
  it('a suggestion in the past, a null one, an unknown wave and a bad date are all null', () => {
    expect(defaultScheduleAt('reminder', waves, new Date('2026-11-29T00:00:00Z'))).toBeNull();
    expect(defaultScheduleAt('reminder', waves, new Date('2026-11-28T18:00:00Z'))).toBeNull();
    expect(defaultScheduleAt('reminder', [{ ...waves[1], suggested_at: null }], now)).toBeNull();
    expect(defaultScheduleAt('nope', waves, now)).toBeNull();
    expect(defaultScheduleAt('reminder', [{ ...waves[1], suggested_at: 'garbage' }], now)).toBeNull();
  });
});

describe('datetime-local conversion (Pacific, #0585)', () => {
  it('round-trips in Pacific daylight time (UTC-7)', () => {
    expect(toLocalInputValue('2026-10-31T18:00:00Z')).toBe('2026-10-31T11:00');
    expect(fromLocalInputValue('2026-10-31T11:00')).toBe('2026-10-31T18:00:00Z');
  });
  it('round-trips in Pacific standard time (UTC-8)', () => {
    expect(toLocalInputValue('2026-11-28T18:00:00Z')).toBe('2026-11-28T10:00');
    expect(fromLocalInputValue('2026-11-28T10:00')).toBe('2026-11-28T18:00:00Z');
  });
  it('crosses midnight and a year boundary correctly', () => {
    expect(toLocalInputValue('2027-01-01T03:00:00Z')).toBe('2026-12-31T19:00');
    expect(fromLocalInputValue('2026-12-31T19:00')).toBe('2027-01-01T03:00:00Z');
  });
  it('rejects garbage', () => {
    expect(toLocalInputValue('garbage')).toBe('');
    expect(fromLocalInputValue('')).toBeNull();
    expect(fromLocalInputValue('tomorrow')).toBeNull();
  });
});

describe('shouldPoll and channelStatusNote', () => {
  it('polls only while something is queued', () => {
    expect(shouldPoll({ posts: [] })).toBe(false);
    expect(shouldPoll({ posts: [post({ status: 'scheduled' }), post({ status: 'failed' })] })).toBe(false);
    expect(shouldPoll({ posts: [post({ status: 'draft' }), post({ status: 'queued' })] })).toBe(true);
  });
  it('flags only an automatic channel without credentials', () => {
    expect(channelStatusNote({ ...mastodon, configured: false })).toBe('Not configured — post by hand');
    expect(channelStatusNote(bluesky)).toBeNull();
    expect(channelStatusNote(instagram)).toBeNull();
  });
});

describe('utmChips, messages, url validation', () => {
  it('orders chips source, medium, campaign, content', () => {
    expect(utmChips(post()).map((c) => c.label)).toEqual(['utm_source', 'utm_medium', 'utm_campaign', 'utm_content']);
    expect(utmChips(post())[3].value).toBe('announce');
  });
  it('explains unavailability', () => {
    expect(unavailableMessage('shortlinks_not_configured')).toContain('Settings → Outreach connections');
    expect(unavailableMessage('shortlinks_not_configured')).not.toContain('SHORTLINKS_');
    expect(unavailableMessage('workshop_not_published')).toBe('Publish this workshop to start outreach.');
    expect(unavailableMessage('???')).toBe('Outreach is not available right now.');
  });
  it('validates a post URL', () => {
    expect(validateRemoteUrl('')).toBeNull();
    expect(validateRemoteUrl('  ')).toBeNull();
    expect(validateRemoteUrl('https://bsky.app/profile/x')).toBeNull();
    expect(validateRemoteUrl('javascript:alert(1)')).not.toBeNull();
    expect(validateRemoteUrl('not a url')).not.toBeNull();
    expect(validateRemoteUrl('https://x.test/' + 'a'.repeat(2100))).not.toBeNull();
  });
});

describe('scheduleHint (#0587)', () => {
  const waves: OutreachWave[] = [
    { name: 'announce', label: 'Announce', suggested_at: null },
    { name: 'reminder', label: 'Reminder', suggested_at: '2026-10-25T00:00:00Z' },
  ];
  it('announce usually goes out now', () => {
    expect(scheduleHint('announce', waves)).toBe('The announcement usually goes out now. Pick a time only if you want to wait.');
  });
  it('a passed suggestion is named with its Pacific date', () => {
    expect(scheduleHint('reminder', waves, new Date('2026-10-26T00:00:00Z'))).toBe('The suggested time (Sat, Oct 24, 5:00 PM PT) has passed. Pick one.');
  });
  it('a suggestion still ahead is not called passed', () => {
    expect(scheduleHint('reminder', waves, new Date('2026-10-20T00:00:00Z'))).toBe('The suggested time is Sat, Oct 24, 5:00 PM PT. Pick a time.');
  });
  it('a wave with no suggestion says so', () => {
    expect(scheduleHint('nope', waves)).toBe('There is no suggested time for this post. Pick one.');
  });
});
