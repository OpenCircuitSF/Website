import { describe, expect, it } from 'vitest';
import {
  canScheduleAll,
  handLinkReportMessage,
  linkFailureReasonLabel,
  linkReportMessage,
  scheduleAllMessage,
  scheduleSkipReasonLabel,
} from './outreachLinks';
import type { WorkshopOutreach } from './types';

describe('linkFailureReasonLabel', () => {
  it('words every reason and defaults unknown ones to failed', () => {
    expect(linkFailureReasonLabel('rate_limited')).toBe('ShortLinks is rate limiting');
    expect(linkFailureReasonLabel('timeout')).toBe('timed out');
    expect(linkFailureReasonLabel('not_configured')).toBe('ShortLinks is not configured');
    expect(linkFailureReasonLabel('rejected')).toBe('ShortLinks rejected the token');
    expect(linkFailureReasonLabel('code_changed')).toBe('the location code changed');
    expect(linkFailureReasonLabel('failed')).toBe('failed');
    expect(linkFailureReasonLabel('mystery')).toBe('failed');
  });
});

describe('handLinkReportMessage', () => {
  it('is null when nothing failed', () => {
    expect(handLinkReportMessage(null)).toBeNull();
    expect(handLinkReportMessage({ created: 6, existing: 3, failed: [], stopped: false })).toBeNull();
  });
  it('words one failure in the singular', () => {
    expect(
      handLinkReportMessage({ created: 5, existing: 3, stopped: false, failed: [{ kind: 'post', label: 'TikTok · Reminder', reason: 'failed' }] }),
    ).toBe('1 ShortLink could not be created: TikTok · Reminder (failed). Everything else here still works, and opening this page again tries again.');
  });
  it('words several in the plural and never names a button', () => {
    const msg = handLinkReportMessage({
      created: 4, existing: 3, stopped: true,
      failed: [
        { kind: 'post', label: 'TikTok · Reminder', reason: 'rate_limited' },
        { kind: 'post', label: 'YouTube Shorts · Final', reason: 'timeout' },
      ],
    });
    expect(msg).toBe(
      '2 ShortLinks could not be created: TikTok · Reminder (ShortLinks is rate limiting), YouTube Shorts · Final (timed out). Everything else here still works, and opening this page again tries again.',
    );
    expect(msg).not.toMatch(/Create missing links/);
  });
});

describe('linkReportMessage', () => {
  it('is null when nothing failed', () => {
    expect(linkReportMessage(null)).toBeNull();
    expect(linkReportMessage(undefined)).toBeNull();
    expect(linkReportMessage({ created: 3, existing: 0, failed: [], stopped: false })).toBeNull();
  });
  it('names each failed link and how to recover', () => {
    expect(
      linkReportMessage({
        created: 13, existing: 0, stopped: false,
        failed: [
          { kind: 'post', label: 'Bluesky · Reminder', reason: 'rate_limited' },
          { kind: 'flyer', label: 'Flyer · Mission Library', reason: 'failed' },
        ],
      }),
    ).toBe(
      '2 links could not be created: Bluesky · Reminder (ShortLinks is rate limiting), Flyer · Mission Library (failed). Everything else is saved. A missing link is made when it is needed: press Create link on a post, or Print on a flyer.',
    );
    expect(linkReportMessage({ created: 0, existing: 0, stopped: false, failed: [{ kind: 'post', label: 'X', reason: 'timeout' }] })).toMatch(/^1 link could not/);
  });
  it('names only controls that exist (#0629)', () => {
    const msg = linkReportMessage({ created: 0, existing: 0, stopped: false, failed: [{ kind: 'post', label: 'X', reason: 'failed' }] });
    expect(msg).not.toMatch(/Create missing links|Create links\b/);
    expect(msg).toContain('Create link on a post');
  });
});

describe('scheduleSkipReasonLabel', () => {
  it('words every reason', () => {
    expect(scheduleSkipReasonLabel('time_passed')).toBe('time has passed');
    expect(scheduleSkipReasonLabel('no_suggested_time')).toBe('no suggested time — schedule Announce by hand');
    expect(scheduleSkipReasonLabel('not_connected')).toBe('not connected');
    expect(scheduleSkipReasonLabel('not_drafted')).toBe('not drafted');
    expect(scheduleSkipReasonLabel('no_link')).toBe('no link yet');
    expect(scheduleSkipReasonLabel('invalid')).toMatch(/cannot be sent/);
    expect(scheduleSkipReasonLabel('too_far')).toMatch(/future/);
    expect(scheduleSkipReasonLabel('changed')).toBe('changed since the preview');
    expect(scheduleSkipReasonLabel('other')).toBe('other');
  });
});

describe('scheduleAllMessage', () => {
  it('reports scheduled and changed posts', () => {
    const item = { post_id: 1, channel: 'bluesky', channel_label: 'Bluesky', wave: 'last-call', wave_label: 'Final', scheduled_at: 'x' };
    const skip = { post_id: 2, channel: 'bluesky', channel_label: 'Bluesky', wave: 'reminder', wave_label: 'Reminder', reason: 'changed', suggested_at: null };
    expect(scheduleAllMessage({ dry_run: false, to_schedule: [item], skipped: [] })).toBe('Scheduled 1 post.');
    expect(scheduleAllMessage({ dry_run: false, to_schedule: [], skipped: [skip] })).toBe('Nothing was scheduled. 1 post changed since the preview and was not scheduled.');
  });
});

describe('canScheduleAll', () => {
  const ch = (name: string, automatic: boolean, configured: boolean) => ({
    name, label: name, utm_source: name, utm_medium: 'social', char_limit: 0, automatic, configured, account: '',
  });
  function wo(status: string, channels: ReturnType<typeof ch>[]): WorkshopOutreach {
    return {
      available: true, unavailable_reason: '',
      workshop: { id: 1, slug: 'w', title: 'W', status, starts_at: null },
      outreach: { id: 1, workshop_id: 1, campaign_slug: 'w', created_at: '' }, channels, waves: [], posts: [],
    };
  }
  it('needs a published workshop and a connected automatic channel', () => {
    expect(canScheduleAll(wo('published', [ch('bluesky', true, true)]))).toEqual({ ok: true, reason: null });
    expect(canScheduleAll(wo('draft', [ch('bluesky', true, true)])).ok).toBe(false);
    const none = canScheduleAll(wo('published', [ch('bluesky', true, false), ch('instagram', false, false)]));
    expect(none.ok).toBe(false);
    expect(none.reason).toMatch(/Settings/);
  });
  it('does not let an off channel with a post open Schedule all, and does not call it not connected (#0628)', () => {
    const r = canScheduleAll({ ...wo('published', [ch('bluesky', true, true)]), off_channels: [{ name: 'bluesky', label: 'Bluesky', automatic: true }] });
    expect(r.ok).toBe(false);
    expect(r.reason).toMatch(/Settings/);
    expect(r.reason).not.toMatch(/not connected/);
  });
});
