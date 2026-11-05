import { describe, expect, it } from 'vitest';
import { composeBody, isHashtagParagraph, messageTargets, planSaveMessage, restoredTail, sharedMessage, splitBody, waveExcerpt } from './outreachMessage';
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
const autoChannels = channels.slice(0, 2);
const autoView = (posts: OutreachPost[]) => ({ channels: autoChannels, waves, posts });

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
  it('treats a final hashtag-only paragraph as tail even when typed (documented ambiguity, #0597), and still round-trips it', () => {
    const body = 'Join us.\n\nLearn to solder.\n\n#typed';
    const s = splitBody(body, null);
    expect(s).toEqual({ message: 'Join us.\n\nLearn to solder.', tail: ['#typed'] });
    expect(composeBody(s.message, s.tail)).toBe(body);
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

describe('a message ending in a hashtag-only paragraph (#0593)', () => {
  it('keeps that paragraph in the message when the post also has its own hashtag line', () => {
    const body = 'Hello.\n\n#soldering\n\n#a #b';
    expect(splitBody(body, null)).toEqual({ message: 'Hello.\n\n#soldering', tail: ['#a #b'] });
  });
  it('round-trips through compose', () => {
    const planned = planSaveMessage(view([bPost]), 'announce', 'Hello.\n\n#workshop');
    expect(splitBody(planned[0].body, null).message).toBe('Hello.\n\n#workshop');
  });
});

describe('restoredTail (#0593)', () => {
  it('keeps a post\'s own tail', () => {
    expect(restoredTail(view([bPost, mPost]), mPost)).toEqual([`Spots are limited, so you can sign up here: ${MURL}`, '#soldering #sf']);
  });
  it('restores the hashtags for Bluesky from a non-customized sibling, with no link', () => {
    const lost = { ...bPost, customized: true, body: 'Only my words.' };
    expect(restoredTail(view([lost, mPost]), lost)).toEqual(['#soldering #sf']);
  });
  it('restores Mastodon\'s link line (own short URL) and hashtags when both were lost', () => {
    const lost = { ...mPost, customized: true, body: 'Only my words.', short_url: 'https://go.example/m2' };
    const sibling = { ...mPost, id: 3, channel: 'mastodon', body: mast };
    expect(restoredTail(view([bPost, lost, sibling]), lost)).toEqual(['Spots are limited, so you can sign up here: https://go.example/m2', '#soldering #sf']);
  });
  it('turns an unprepared sibling\'s link token into this post\'s own short URL', () => {
    const lost = { ...mPost, customized: true, body: 'Only my words.', short_url: 'https://go.example/m2' };
    const sibling = { ...mPost, id: 3, channel: 'mastodon', body: mast.split(MURL).join('{link}'), short_url: null };
    expect(restoredTail(view([bPost, lost, sibling]), lost)).toEqual(['Spots are limited, so you can sign up here: https://go.example/m2', '#soldering #sf']);
  });
  it('makes a plain link line when no sibling carries one', () => {
    const lost = { ...mPost, customized: true, body: 'Only my words.' };
    expect(restoredTail(view([bPost, lost]), lost)).toEqual([`Sign up here: ${MURL}`, '#soldering #sf']);
  });
});

describe('waveExcerpt (#0624)', () => {
  it("takes the first written post's message on one line, without hashtags or link lines", () => {
    expect(waveExcerpt(view([mPost]), 'announce')).toBe('Join us for Soldering 101. Learn to solder.');
  });
  it('still shows a post that has gone out', () => {
    expect(waveExcerpt(view([post({ status: 'posted' })]), 'announce')).toBe('Join us for Soldering 101. Learn to solder.');
  });
  it('shortens long text with an ellipsis without splitting an emoji', () => {
    const out = waveExcerpt(view([post({ body: '🔌'.repeat(100) })]), 'announce');
    expect(Array.from(out)).toHaveLength(80);
    expect(out.endsWith('🔌…')).toBe(true);
  });
  it('is empty for a section with no posts', () => {
    expect(waveExcerpt(view([bPost]), 'reminder')).toBe('');
  });
});

