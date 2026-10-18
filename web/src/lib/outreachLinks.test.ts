import { describe, expect, it } from 'vitest';
import {
  canScheduleAll,
  draftAllMessage,
  linkClicksLabel,
  linkFailureReasonLabel,
  linkReportMessage,
  linkRowLabel,
  linksEmptyState,
  scheduleAllMessage,
  scheduleSkipReasonLabel,
} from './outreachLinks';
import type { OutreachLinkRow, OutreachLinksView, WorkshopOutreach } from './types';

function row(over: Partial<OutreachLinkRow> = {}): OutreachLinkRow {
  return {
    kind: 'post', channel: 'bluesky', channel_label: 'Bluesky', wave: 'announce', wave_label: 'Announce',
    location_id: null, location_name: null, location_code: null,
    short_link_key: 'k1', short_url: 'https://go.example/k1', destination_url: 'https://site/w?utm_source=bluesky',
    utm: { source: 'bluesky', medium: 'social', campaign: 'w', content: 'announce' }, clicks: 4, ...over,
  };
}

function links(over: Partial<OutreachLinksView> = {}): OutreachLinksView {
  return {
    available: true, unavailable_reason: '', outreach: { id: 1, campaign_slug: 'w' }, clicks_status: 'ok',
    missing: 0, links: [row()], report: null, ...over,
  };
}

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
      '2 links could not be created: Bluesky · Reminder (ShortLinks is rate limiting), Flyer · Mission Library (failed). Everything else is saved — use Create missing links to try again.',
    );
    expect(linkReportMessage({ created: 0, existing: 0, stopped: false, failed: [{ kind: 'post', label: 'X', reason: 'timeout' }] })).toMatch(/^1 link could not/);
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

describe('linksEmptyState', () => {
  it('points at Settings, the editor, and Create links', () => {
    expect(linksEmptyState(links({ available: false, unavailable_reason: 'shortlinks_not_configured' })).kind).toBe('not_connected');
    expect(linksEmptyState(links({ available: false, unavailable_reason: 'workshop_not_published' })).kind).toBe('not_published');
    const none = linksEmptyState(links({ links: [row({ short_url: null, short_link_key: null })], missing: 1 }));
    expect(none.kind).toBe('no_links');
    expect(none.message).toMatch(/Create links/);
    const some = linksEmptyState(links({ links: [row(), row({ short_url: null, short_link_key: null })], missing: 1 }));
    expect(some.kind).toBe('some_missing');
    expect(some.message).toMatch(/1 link is missing/);
    expect(linksEmptyState(links({ missing: 2, links: [row(), row()] , outreach: { id: 1, campaign_slug: 'w' } })).message).toMatch(/2 links are missing/);
  });
  it('says nothing when the set is complete or outreach has not started', () => {
    expect(linksEmptyState(links())).toEqual({ kind: null, message: '' });
    expect(linksEmptyState(links({ outreach: null, links: [], clicks_status: 'not_started' }))).toEqual({ kind: null, message: '' });
  });
});

describe('row labels', () => {
  it('labels a post row and a flyer row', () => {
    expect(linkRowLabel(row())).toBe('Bluesky');
    expect(linkRowLabel(row({ kind: 'flyer', channel_label: 'Flyer', location_name: 'Mission Library' }))).toBe('Flyer · Mission Library');
  });
  it('shows an em dash without a known count', () => {
    expect(linkClicksLabel(row({ clicks: 4 }), 'ok')).toBe('4');
    expect(linkClicksLabel(row({ clicks: 0 }), 'ok')).toBe('0');
    expect(linkClicksLabel(row({ clicks: null }), 'ok')).toBe('—');
    expect(linkClicksLabel(row({ clicks: 4 }), 'unreachable')).toBe('—');
  });
});

describe('draftAllMessage and scheduleAllMessage', () => {
  it('reports drafts, leftovers and missing links', () => {
    expect(draftAllMessage({ created: [], skipped: 15, without_link: 0 })).toBe('Nothing to draft: all 15 cells already have a post.');
    expect(
      draftAllMessage({ created: [{ post_id: 1, channel: 'a', wave: 'b', has_link: true }], skipped: 14, without_link: 0 }),
    ).toBe('Drafted 1 post; 14 existing posts were left alone.');
    expect(
      draftAllMessage({ created: [{ post_id: 1, channel: 'a', wave: 'b', has_link: false }, { post_id: 2, channel: 'a', wave: 'c', has_link: false }], skipped: 0, without_link: 2 }),
    ).toBe('Drafted 2 posts. 2 drafts have no link yet — create the missing links first.');
  });
  it('reports scheduled and changed posts', () => {
    const item = { post_id: 1, channel: 'bluesky', channel_label: 'Bluesky', wave: 'last-call', wave_label: 'Last call', scheduled_at: 'x' };
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
});
