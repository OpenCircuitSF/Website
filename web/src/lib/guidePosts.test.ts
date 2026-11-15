import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { countCharacters } from './outreach';
import {
  GUIDE_LINK_SAMPLE,
  bodyAfterGuideChange,
  allCreated,
  campaignTotals,
  canScheduleAgainTogether,
  createAcross,
  createButtonLabel,
  defaultTickedChannels,
  clicksErrorMessage,
  countWithLink,
  counterText,
  defaultScheduleAt,
  formatSignups,
  groupPosts,
  groupShares,
  outcomeSummary,
  shareChannelLabels,
  isValidCampaign,
  linkErrorMessage,
  overLimit,
  postActions,
  signupsErrorMessage,
  sortShares,
} from './guidePosts';
import type { GuidePostChannel, GuidePostCreated, GuidePostRow, GuideShare } from './types';

const bluesky: GuidePostChannel = { name: 'bluesky', label: 'Bluesky', char_limit: 300, configured: true };
const mastodon: GuidePostChannel = { name: 'mastodon', label: 'Mastodon', char_limit: 500, configured: true };

function post(over: Partial<GuidePostRow> = {}): GuidePostRow {
  return {
    id: 1,
    guide_slug: 'blink-led',
    channel: 'bluesky',
    body: 'Blink an LED {link}',
    destination_url: 'https://www.opencircuitsf.com/guides/blink-led',
    campaign_slug: 'guides-2026-11',
    short_link_key: 'k1',
    short_url: 'https://go.opencircuitsf.com/u/k1',
    status: 'draft',
    scheduled_at: null,
    posted_at: null,
    remote_id: null,
    remote_url: null,
    last_error: null,
    attempt_generation: 0,
    created_at: '2026-11-01T00:00:00Z',
    updated_at: null,
    clicks: 0,
    signups: { requested: 0, confirmed: 0 },
    ...over,
  };
}

function share(over: Partial<GuideShare> = {}): GuideShare {
  return {
    slug: 'blink-led',
    title: 'Blink an LED',
    url: 'https://www.opencircuitsf.com/guides/blink-led',
    last_posted_at: null,
    last_campaign: '',
    channels: [],
    campaigns: [],
    posted_count: 0,
    next_scheduled_at: null,
    clicks: null,
    signups: null,
    ...over,
  };
}

describe('guidePosts', () => {
  it('counts the {link} token as the sample short URL before a link exists', () => {
    const r = countWithLink(bluesky, 'Hi {link}', null);
    expect(r.exact).toBe(false);
    expect(r.count).toBe(countCharacters(bluesky, `Hi ${GUIDE_LINK_SAMPLE}`, true).count);
  });

  it('counts the real short URL once the link exists', () => {
    const r = countWithLink(bluesky, 'Hi {link}', 'https://go.x/k');
    expect(r).toEqual({ count: 'Hi https://go.x/k'.length, exact: true });
  });

  it('counts Mastodon URLs as 23', () => {
    expect(countWithLink(mastodon, 'Hi {link}', null).count).toBe(3 + 23);
  });

  it('writes the counter with the short link included and an approximate mark', () => {
    const n = 3 + GUIDE_LINK_SAMPLE.length;
    expect(counterText(bluesky, 'Hi {link}', null)).toBe(`≈ ${n} / 300 characters, including the short link`);
    expect(counterText(bluesky, 'Hi', null)).toBe('2 / 300 characters');
    expect(counterText({ ...bluesky, char_limit: 0 }, 'Hi', null)).toBe('2 characters');
  });

  it('flags over the Bluesky limit', () => {
    expect(overLimit(bluesky, 'x'.repeat(300), null)).toBe(false);
    expect(overLimit(bluesky, 'x'.repeat(260) + ' {link}', null)).toBe(true);
  });

  it('mirrors the Go sample short URL', () => {
    const path = fileURLToPath(new URL('../../../internal/guideposts/guideposts.go', import.meta.url));
    expect(readFileSync(path, 'utf-8')).toContain(`sampleShortURL = "${GUIDE_LINK_SAMPLE}"`);
  });

  it('accepts blank and lowercase-hyphen campaigns, rejects others', () => {
    expect(isValidCampaign('')).toBe(true);
    expect(isValidCampaign('guides-2026-11')).toBe(true);
    expect(isValidCampaign('Guides')).toBe(false);
    expect(isValidCampaign('a--b')).toBe(false);
    expect(isValidCampaign('-a')).toBe(false);
    expect(isValidCampaign('a'.repeat(101))).toBe(false);
  });

  it('keeps edited text when the guide changes', () => {
    expect(bodyAfterGuideChange('old', 'old', 'new')).toBe('new');
    expect(bodyAfterGuideChange('  ', 'old', 'new')).toBe('new');
    expect(bodyAfterGuideChange('mine', 'old', 'new')).toBe('mine');
  });

  it('defaults to tomorrow at the post hour', () => {
    expect(defaultScheduleAt(new Date('2026-11-14T20:00:00Z'), 9)).toBe('2026-11-15T09:00');
  });

  it('rolls over month end', () => {
    expect(defaultScheduleAt(new Date('2026-11-30T20:00:00Z'), 9)).toBe('2026-12-01T09:00');
    expect(defaultScheduleAt(new Date('2026-12-31T20:00:00Z'), 9)).toBe('2027-01-01T09:00');
  });

  it('uses the Pacific date, not UTC', () => {
    expect(defaultScheduleAt(new Date('2026-12-01T03:00:00Z'), 10)).toBe('2026-12-01T10:00');
  });

  it('lists actions by status', () => {
    const names = (status: GuidePostRow['status'], remote_url: string | null = null) =>
      postActions({ status, remote_url }, true).map((a) => a.action);
    expect(names('draft')).toEqual(['edit', 'schedule', 'postNow', 'delete']);
    expect(names('scheduled')).toEqual(['edit', 'unschedule', 'postNow']);
    expect(names('failed')).toEqual(['edit', 'retry', 'schedule', 'delete']);
    expect(names('queued')).toEqual([]);
    expect(names('posted')).toEqual(['again']);
    expect(names('posted', 'https://bsky.app/x')).toEqual(['open', 'again']);
  });

  it('disables schedule and post now when the channel is not connected', () => {
    const acts = postActions({ status: 'draft', remote_url: null }, false);
    expect(acts.filter((a) => !a.enabled).map((a) => a.action)).toEqual(['schedule', 'postNow']);
  });

  it('sorts oldest share first with never-posted guides on top', () => {
    const rows = [
      share({ slug: 'a', last_posted_at: '2026-11-10T00:00:00Z' }),
      share({ slug: 'b' }),
      share({ slug: 'c', last_posted_at: '2026-11-01T00:00:00Z' }),
      share({ slug: 'd' }),
    ];
    const copy = rows.slice();
    expect(sortShares(rows, 'oldest').map((r) => r.slug)).toEqual(['b', 'd', 'c', 'a']);
    expect(rows).toEqual(copy);
  });

  it('sorts newest first, by clicks, and in guide order', () => {
    const rows = [
      share({ slug: 'a', last_posted_at: '2026-11-10T00:00:00Z', clicks: 3 }),
      share({ slug: 'b' }),
      share({ slug: 'c', last_posted_at: '2026-11-01T00:00:00Z', clicks: 9 }),
    ];
    expect(sortShares(rows, 'newest').map((r) => r.slug)).toEqual(['a', 'c', 'b']);
    expect(sortShares(rows, 'clicks').map((r) => r.slug)).toEqual(['c', 'a', 'b']);
    expect(sortShares(rows, 'catalog').map((r) => r.slug)).toEqual(['a', 'b', 'c']);
  });

  it('groups posts into active and posted, filtered by guide', () => {
    const posts = [
      post({ id: 1, status: 'draft', created_at: '2026-11-01T00:00:00Z' }),
      post({ id: 2, status: 'draft', created_at: '2026-11-02T00:00:00Z' }),
      post({ id: 3, status: 'scheduled', scheduled_at: '2026-11-20T00:00:00Z' }),
      post({ id: 4, status: 'queued' }),
      post({ id: 5, status: 'failed' }),
      post({ id: 6, status: 'posted', posted_at: '2026-11-03T00:00:00Z' }),
      post({ id: 7, status: 'posted', posted_at: '2026-11-05T00:00:00Z' }),
      post({ id: 8, guide_slug: 'other-guide', status: 'draft' }),
    ];
    const g = groupPosts(posts, 'blink-led');
    expect(g.active.map((p) => p.id)).toEqual([4, 5, 3, 2, 1]);
    expect(g.posted.map((p) => p.id)).toEqual([7, 6]);
    expect(groupPosts(posts, null).active).toHaveLength(6);
  });

  it('totals clicks and signups by campaign, null when any is unknown', () => {
    const t = campaignTotals([
      post({ id: 1, campaign_slug: 'guides-2026-11', clicks: 2, signups: { requested: 1, confirmed: 1 } }),
      post({ id: 2, campaign_slug: 'guides-2026-11', clicks: 3, signups: { requested: 2, confirmed: 0 } }),
      post({ id: 3, campaign_slug: 'guides-2026-12', clicks: null, signups: null }),
      post({ id: 4, campaign_slug: 'guides-2026-12', clicks: 5 }),
      post({ id: 5, campaign_slug: 'nolink', short_url: null }),
    ]);
    expect(t).toEqual([
      { campaign: 'guides-2026-12', posts: 2, clicks: null, signups: null },
      { campaign: 'guides-2026-11', posts: 2, clicks: 5, signups: { requested: 3, confirmed: 1 } },
    ]);
  });

  it('explains link and click errors', () => {
    expect(linkErrorMessage(null)).toBeNull();
    expect(linkErrorMessage('not_configured')).toContain('ShortLinks is not configured');
    expect(clicksErrorMessage(null)).toBeNull();
    expect(clicksErrorMessage('not_configured')).toContain('not configured');
    expect(clicksErrorMessage('rejected')).toContain('rejected');
    expect(clicksErrorMessage('timeout')).toBe('Some click counts are unavailable right now (timed out).');
    expect(signupsErrorMessage('x')).toBe('Signup counts could not be loaded right now.');
    expect(signupsErrorMessage(null)).toBeNull();
  });

  it('formats signups', () => {
    expect(formatSignups(null)).toBe('—');
    expect(formatSignups({ requested: 1, confirmed: 1 })).toBe('1 signup, 1 confirmed');
    expect(formatSignups({ requested: 0, confirmed: 0 })).toBe('0 signups, 0 confirmed');
  });
});

describe('#0665 one draft, several channels', () => {
  it('ticks every connected channel by default', () => {
    expect(defaultTickedChannels([bluesky, mastodon])).toEqual(['bluesky', 'mastodon']);
    expect(defaultTickedChannels([{ ...bluesky, configured: false }, mastodon])).toEqual(['mastodon']);
  });

  it('labels the create button by count and schedule', () => {
    expect(createButtonLabel(1, '')).toBe('Create draft');
    expect(createButtonLabel(2, '  ')).toBe('Create drafts');
    expect(createButtonLabel(2, '2026-11-15T09:00')).toBe('Create and schedule');
  });

  const created = (id: number, channel: string, short: string | null, linkError: string | null = null): GuidePostCreated => ({
    post: post({ id, channel, short_url: short }),
    link_error: linkError,
  });

  it('creates on every channel with the same request and schedules each', async () => {
    const calls: string[] = [];
    const scheduled: number[] = [];
    const outcomes = await createAcross(
      [bluesky, mastodon],
      async (ch) => {
        calls.push(ch);
        return ch === 'bluesky' ? created(10, ch, 'https://go.opencircuitsf.com/u/a') : created(11, ch, 'https://go.opencircuitsf.com/u/b');
      },
      async (id) => {
        scheduled.push(id);
      },
    );
    expect(calls).toEqual(['bluesky', 'mastodon']);
    expect(scheduled).toEqual([10, 11]);
    expect(allCreated(outcomes)).toBe(true);
    const msg = outcomeSummary(outcomes, '2026-11-15T17:00:00Z');
    expect(msg).toBe(
      'Bluesky draft created with https://go.opencircuitsf.com/u/a, and scheduled for Nov 15, 2026, 9:00 AM PT. ' +
        'Mastodon draft created with https://go.opencircuitsf.com/u/b, and scheduled for Nov 15, 2026, 9:00 AM PT.',
    );
  });

  it('keeps the other channel when one fails, and does not schedule the failed one', async () => {
    const scheduled: number[] = [];
    const outcomes = await createAcross(
      [bluesky, mastodon],
      async (ch) => {
        if (ch === 'bluesky') throw new Error('Bluesky rejected the request');
        return created(11, ch, 'https://go.opencircuitsf.com/u/b');
      },
      async (id) => {
        scheduled.push(id);
      },
    );
    expect(scheduled).toEqual([11]);
    expect(allCreated(outcomes)).toBe(false);
    expect(outcomes[0]).toMatchObject({ channel: 'bluesky', post: null, error: 'Bluesky rejected the request.' });
    expect(outcomeSummary(outcomes, '2026-11-15T17:00:00Z')).toBe(
      'Bluesky: the draft was not created. Bluesky rejected the request. ' +
        'Mastodon draft created with https://go.opencircuitsf.com/u/b, and scheduled for Nov 15, 2026, 9:00 AM PT.',
    );
  });

  it('reports a short-link failure and a schedule failure per channel', async () => {
    const outcomes = await createAcross(
      [bluesky, mastodon],
      async (ch) => (ch === 'bluesky' ? created(10, ch, null, 'not_configured') : created(11, ch, 'https://go.opencircuitsf.com/u/b')),
      async (id) => {
        if (id === 11) throw new Error('scheduled time is in the past');
      },
    );
    const msg = outcomeSummary(outcomes, '2026-11-15T17:00:00Z');
    expect(msg).toContain('Bluesky draft created, but its short link could not be made');
    expect(msg).toContain('Mastodon draft created with https://go.opencircuitsf.com/u/b. It was not scheduled: scheduled time is in the past.');
    expect(outcomes[1]).toMatchObject({ scheduled: false, scheduleError: 'scheduled time is in the past.' });
  });

  it('groups posts made together into one share', () => {
    const a = post({ id: 1, channel: 'bluesky', created_at: '2026-11-10T10:00:00Z' });
    const b = post({ id: 2, channel: 'mastodon', created_at: '2026-11-10T10:00:03Z' });
    const later = post({ id: 3, channel: 'mastodon', created_at: '2026-11-11T10:00:00Z' });
    const otherBody = post({ id: 4, channel: 'mastodon', body: 'different {link}', created_at: '2026-11-10T10:00:01Z' });
    const { active, posted } = groupShares([a, b, later, otherBody], null);
    expect(posted).toEqual([]);
    expect(active.map((s) => s.map((p) => p.id))).toEqual([[3], [1, 2], [4]]);
  });

  it('keeps a share active until every member is posted', () => {
    const a = post({ id: 1, channel: 'bluesky', status: 'posted', posted_at: '2026-11-10T17:00:00Z' });
    const b = post({ id: 2, channel: 'mastodon', status: 'failed' });
    const r = groupShares([a, b], null);
    expect(r.active.map((s) => s.map((p) => p.id))).toEqual([[2, 1]]);
    expect(r.posted).toEqual([]);
    expect(canScheduleAgainTogether(r.active[0])).toBe(false);
    const both = groupShares([a, { ...b, status: 'posted', posted_at: '2026-11-10T17:00:00Z' }], null);
    expect(both.posted).toHaveLength(1);
    expect(canScheduleAgainTogether(both.posted[0])).toBe(true);
    expect(canScheduleAgainTogether([a])).toBe(false);
  });

  it('names a share\'s channels', () => {
    expect(shareChannelLabels([post({ channel: 'bluesky' }), post({ channel: 'mastodon' })], [bluesky, mastodon])).toBe('Bluesky and Mastodon');
    expect(shareChannelLabels([post({ channel: 'bluesky' })], [bluesky])).toBe('Bluesky');
  });
});
