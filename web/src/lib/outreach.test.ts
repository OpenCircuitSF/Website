import { describe, expect, it } from 'vitest';
import {
  LINK_ESTIMATE,
  buildGrid,
  cellIsStale,
  cellLabel,
  composerActions,
  countCharacters,
  isOverLimit,
  unavailableMessage,
  utmChips,
  validateRemoteUrl,
} from './outreach';
import type { OutreachChannel, OutreachPost, WorkshopOutreach } from './types';

const bluesky: OutreachChannel = { name: 'bluesky', label: 'Bluesky', utm_source: 'bluesky', utm_medium: 'social', char_limit: 300, automatic: true };
const mastodon: OutreachChannel = { name: 'mastodon', label: 'Mastodon', utm_source: 'mastodon', utm_medium: 'social', char_limit: 500, automatic: true };
const instagram: OutreachChannel = { name: 'instagram', label: 'Instagram', utm_source: 'instagram', utm_medium: 'social', char_limit: 0, automatic: false };

function post(over: Partial<OutreachPost> = {}): OutreachPost {
  return {
    id: 1, outreach_id: 1, channel: 'bluesky', wave: 'announce', body: 'x', status: 'draft', prepared: false, stale: false,
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

describe('buildGrid and cells', () => {
  const view: Pick<WorkshopOutreach, 'channels' | 'waves' | 'posts'> = {
    channels: [bluesky, instagram],
    waves: [
      { name: 'announce', label: 'Announce', suggested_at: null },
      { name: 'reminder', label: 'Reminder', suggested_at: null },
    ],
    posts: [post({ id: 7, channel: 'instagram', wave: 'reminder' })],
  };
  it('lays out channels by waves in server order with nulls for gaps', () => {
    const g = buildGrid(view);
    expect(g.map((r) => r.channel.name)).toEqual(['bluesky', 'instagram']);
    expect(g[0].cells.map((c) => c.post)).toEqual([null, null]);
    expect(g[1].cells[1].post?.id).toBe(7);
    expect(g[1].cells[0].post).toBeNull();
  });
  it('labels each state', () => {
    expect(cellLabel(null)).toBe('Not drafted');
    expect(cellLabel(post())).toBe('Draft');
    expect(cellLabel(post({ prepared: true }))).toBe('Link ready');
    expect(cellLabel(post({ prepared: true, status: 'manual' }))).toBe('Ready to post');
    expect(cellLabel(post({ prepared: true, status: 'posted' }))).toBe('Posted');
  });
  it('reports staleness', () => {
    expect(cellIsStale(null)).toBe(false);
    expect(cellIsStale(post({ stale: true }))).toBe(true);
  });
});

describe('composerActions', () => {
  it('draft, available', () => {
    expect(composerActions(post(), true)).toEqual({ canSave: true, canRegenerate: false, canPrepare: true, canCopy: false, canMarkPosted: false, canDelete: true });
  });
  it('draft, unavailable cannot prepare', () => {
    expect(composerActions(post(), false).canPrepare).toBe(false);
  });
  it('stale offers regenerate', () => {
    expect(composerActions(post({ stale: true }), true).canRegenerate).toBe(true);
  });
  it('prepared manual can copy and mark posted but not prepare', () => {
    expect(composerActions(post({ prepared: true, status: 'manual' }), true)).toEqual({ canSave: true, canRegenerate: false, canPrepare: false, canCopy: true, canMarkPosted: true, canDelete: true });
  });
  it('prepared automatic draft can also copy and mark posted in this phase', () => {
    const a = composerActions(post({ prepared: true }), true);
    expect(a.canCopy && a.canMarkPosted).toBe(true);
  });
  it('posted is locked', () => {
    expect(composerActions(post({ prepared: true, status: 'posted', stale: true }), true)).toEqual({ canSave: false, canRegenerate: false, canPrepare: false, canCopy: false, canMarkPosted: false, canDelete: false });
  });
});

describe('utmChips, messages, url validation', () => {
  it('orders chips source, medium, campaign, content', () => {
    expect(utmChips(post()).map((c) => c.label)).toEqual(['utm_source', 'utm_medium', 'utm_campaign', 'utm_content']);
    expect(utmChips(post())[3].value).toBe('announce');
  });
  it('explains unavailability', () => {
    expect(unavailableMessage('shortlinks_not_configured')).toContain('SHORTLINKS_BASE_URL');
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
