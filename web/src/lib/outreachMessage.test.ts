import { describe, expect, it } from 'vitest';
import { composeBody, isHashtagParagraph, manualRowStatus, messageTargets, planSaveMessage, sharedMessage, splitBody, waveRowStatus } from './outreachMessage';
import type { OutreachChannel, OutreachPost, OutreachWave } from './types';

const ch = (name: string, automatic: boolean, configured: boolean, limit = 0): OutreachChannel => ({
  name, label: name, utm_source: name, utm_medium: 'social', char_limit: limit, automatic, configured, account: '',
});
const channels = [ch('bluesky', true, true, 300), ch('mastodon', true, true, 500), ch('instagram', false, false)];
const waves: OutreachWave[] = [
  { name: 'announce', label: 'Announce', suggested_at: null },
  { name: 'reminder', label: 'Reminder', suggested_at: null },
];
const MURL = 'https://go.example/m';
const bsky = 'Join us for Soldering 101.\n\nLearn to solder.\n\n#soldering #sf';
const mast = `Join us for Soldering 101.\n\nLearn to solder.\n\nSpots are limited, so you can sign up here: ${MURL}\n\n#soldering #sf`;

function post(over: Partial<OutreachPost>): OutreachPost {
  return {
    id: 1, outreach_id: 1, channel: 'bluesky', wave: 'announce', body: bsky, status: 'draft', prepared: true, stale: false, customized: false, held: false,
    short_link_key: 'k', short_url: 'https://go.example/b', destination_url: 'https://x', utm: { source: '', medium: '', campaign: '', content: '' },
    scheduled_at: null, posted_at: null, remote_url: null, last_error: null, created_at: '', updated_at: null, ...over,
  };
}
const bPost = post({});
const mPost = post({ id: 2, channel: 'mastodon', body: mast, short_url: MURL });
const view = (posts: OutreachPost[]) => ({ channels, waves, posts });

describe('isHashtagParagraph', () => {
  it('accepts hashtag-only lines and rejects prose', () => {
    expect(isHashtagParagraph('#soldering #sf')).toBe(true);
    expect(isHashtagParagraph('#café')).toBe(true);
    expect(isHashtagParagraph('Come to #soldering')).toBe(false);
    expect(isHashtagParagraph('')).toBe(false);
  });
});

describe('splitBody and composeBody', () => {
  it('peels the hashtag line from Bluesky', () => {
    expect(splitBody(bsky, null)).toEqual({ message: 'Join us for Soldering 101.\n\nLearn to solder.', tail: ['#soldering #sf'] });
  });
  it('peels the link line and the hashtags from Mastodon', () => {
    const s = splitBody(mast, MURL);
    expect(s.message).toBe('Join us for Soldering 101.\n\nLearn to solder.');
    expect(s.tail).toEqual([`Spots are limited, so you can sign up here: ${MURL}`, '#soldering #sf']);
  });
  it('peels a paragraph that still holds the link token', () => {
    expect(splitBody('Hello\n\nSign up: {link}', null).tail).toEqual(['Sign up: {link}']);
  });
  it('round-trips', () => {
    const s = splitBody(mast, MURL);
    expect(composeBody(s.message, s.tail)).toBe(mast);
  });
  it('a body with no tail is all message', () => {
    expect(splitBody('Just words.\n\nMore words.', null)).toEqual({ message: 'Just words.\n\nMore words.', tail: [] });
  });
});

describe('messageTargets and sharedMessage', () => {
  it('skips customized, posted, queued, manual and other-wave posts', () => {
    const v = view([
      bPost,
      post({ id: 2, channel: 'mastodon', customized: true }),
      post({ id: 3, channel: 'instagram', status: 'manual' }),
      post({ id: 4, wave: 'reminder' }),
    ]);
    expect(messageTargets(v, 'announce').map((t) => t.post.id)).toEqual([1]);
    expect(messageTargets(view([post({ status: 'posted' })]), 'announce')).toEqual([]);
    expect(messageTargets(view([post({ status: 'queued' })]), 'announce')).toEqual([]);
  });
  it('the shared message is the first target message', () => {
    expect(sharedMessage(view([bPost, mPost]), 'announce')).toBe('Join us for Soldering 101.\n\nLearn to solder.');
    expect(sharedMessage(view([]), 'announce')).toBe('');
  });
});

describe('planSaveMessage', () => {
  it('composes each post with its own tail and flags the over-limit ones', () => {
    const plan = planSaveMessage(view([bPost, mPost]), 'announce', 'New words.');
    expect(plan.map((i) => i.body)).toEqual([
      'New words.\n\n#soldering #sf',
      `New words.\n\nSpots are limited, so you can sign up here: ${MURL}\n\n#soldering #sf`,
    ]);
    expect(plan.every((i) => !i.over)).toBe(true);
    const long = planSaveMessage(view([bPost, mPost]), 'announce', 'x'.repeat(400));
    expect(long.map((i) => i.over)).toEqual([true, false]);
  });
  it('never drops the Mastodon link line or the hashtags, even when the message itself holds the URL (#0582 review)', () => {
    const plan = planSaveMessage(view([bPost, mPost]), 'announce', `New words.\n\nSee ${MURL}`);
    expect(plan[1].body).toBe(`New words.\n\nSee ${MURL}\n\nSpots are limited, so you can sign up here: ${MURL}\n\n#soldering #sf`);
    const unprepared = { ...mPost, prepared: false, short_url: null, body: mast.replace(MURL, '{link}') };
    expect(planSaveMessage(view([unprepared]), 'announce', 'New words.')[0].body).toBe(
      'New words.\n\nSpots are limited, so you can sign up here: {link}\n\n#soldering #sf',
    );
  });
  it('leaves customized posts out', () => {
    expect(planSaveMessage(view([bPost, { ...mPost, customized: true }]), 'announce', 'a').map((i) => i.post.id)).toEqual([1]);
  });
});

describe('waveRowStatus and manualRowStatus', () => {
  it('Not written with a write action when the wave has no posts', () => {
    expect(waveRowStatus(view([bPost]), 'reminder')).toEqual({ text: 'Not written', action: 'write' });
  });
  it('reads Drafted, scheduled, posting, failed and posted', () => {
    const r = (over: Partial<OutreachPost>) => waveRowStatus(view([post({ wave: 'reminder', ...over })]), 'reminder');
    expect(r({})).toEqual({ text: 'Drafted · not scheduled', action: 'open' });
    expect(r({ status: 'queued' }).text).toBe('Posting now…');
    expect(r({ status: 'failed' }).text).toBe('Drafted · a post failed');
    expect(r({ status: 'scheduled', scheduled_at: '2026-11-30T20:00:00Z' }).text).toMatch(/^Posts automatically /);
    expect(r({ status: 'scheduled', scheduled_at: '2026-11-30T20:00:00Z', held: true }).text).toMatch(/^Scheduled · on hold/);
    expect(r({ status: 'posted' }).text).toBe('Posted 1 of 1');
  });
  it('counts only automatic posts', () => {
    const v = view([post({ wave: 'reminder', status: 'posted' }), post({ id: 2, wave: 'reminder', channel: 'mastodon' })]);
    expect(waveRowStatus(v, 'reminder').text).toBe('Posted 1 of 2');
  });
  it('the manual row counts hand-posted cards', () => {
    expect(manualRowStatus(view([bPost]))).toBe('Nothing drafted');
    expect(manualRowStatus(view([post({ id: 5, channel: 'instagram', status: 'posted' }), post({ id: 6, channel: 'instagram', wave: 'reminder', status: 'manual' })]))).toBe('Posted 1 of 2');
  });
});
