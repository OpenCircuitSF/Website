import { describe, expect, it } from 'vitest';
import { blueskyCard, copyablePostLink, hostOf, identicalAutomaticBodies, mastodonLinkCard, segments } from './outreachPreview';

const workshop = (over: Record<string, unknown> = {}) => ({
  id: 1, slug: 'solder-101', title: 'Soldering 101', status: 'published', starts_at: null, summary: 'Learn to solder.', cover_image: '/media/cover.png', ...over,
});

describe('segments', () => {
  it('splits text, hashtags and links in order', () => {
    expect(segments('Join us #soldering at https://go.example/x now')).toEqual([
      { kind: 'text', text: 'Join us ' },
      { kind: 'hashtag', text: '#soldering' },
      { kind: 'text', text: ' at ' },
      { kind: 'link', text: 'https://go.example/x' },
      { kind: 'text', text: ' now' },
    ]);
  });
  it('returns nothing for an empty body and one text run for plain text', () => {
    expect(segments('')).toEqual([]);
    expect(segments('plain')).toEqual([{ kind: 'text', text: 'plain' }]);
  });
  it('does not take a mid-word # as a hashtag', () => {
    expect(segments('issue#12')).toEqual([{ kind: 'text', text: 'issue#12' }]);
  });
});

describe('hostOf', () => {
  it('returns the host, or the fallback when the URL is missing or bad', () => {
    expect(hostOf('https://go.example/abc', 'site.test')).toBe('go.example');
    expect(hostOf(null, 'site.test')).toBe('site.test');
    expect(hostOf('not a url', 'site.test')).toBe('site.test');
  });
});

describe('blueskyCard', () => {
  const post = { short_url: 'https://go.example/b' };
  it('uses the short host by default and the site host when canonical', () => {
    expect(blueskyCard({ workshop: workshop() }, post, 'site.test').host).toBe('go.example');
    expect(blueskyCard({ workshop: workshop(), bluesky_card: 'canonical' }, post, 'site.test').host).toBe('site.test');
  });
  it('carries the picture only for a /media/ cover', () => {
    expect(blueskyCard({ workshop: workshop() }, post, 'h').image).toBe('/media/cover.png');
    expect(blueskyCard({ workshop: workshop({ cover_image: '/images/x.png' }) }, post, 'h').image).toBeNull();
    expect(blueskyCard({ workshop: workshop({ cover_image: null }) }, post, 'h').image).toBeNull();
  });
  it('trims the summary and tolerates a missing one', () => {
    expect(blueskyCard({ workshop: workshop({ summary: '  hi  ' }) }, post, 'h').description).toBe('hi');
    expect(blueskyCard({ workshop: workshop({ summary: null }) }, post, 'h').description).toBe('');
  });
});

describe('mastodonLinkCard', () => {
  it('is null without a URL in the body', () => {
    expect(mastodonLinkCard({ workshop: workshop() }, 'no link here')).toBeNull();
  });
  it('uses the cover, else the workshop og image, and the og:site_name byline', () => {
    const body = 'Sign up: https://go.example/m #tag';
    expect(mastodonLinkCard({ workshop: workshop() }, body)).toMatchObject({ image: '/media/cover.png', host: 'Open Circuit SF' });
    expect(mastodonLinkCard({ workshop: workshop({ cover_image: null }) }, body)?.image).toBe('/workshops/solder-101/og.png');
  });
  it('keeps an absolute cover as og:image does, and drops an unsafe one', () => {
    const body = 'https://go.example/m';
    expect(mastodonLinkCard({ workshop: workshop({ cover_image: 'https://cdn.test/c.jpg' }) }, body)?.image).toBe('https://cdn.test/c.jpg');
    expect(mastodonLinkCard({ workshop: workshop({ cover_image: 'javascript:alert(1)' }) }, body)?.image).toBe('/workshops/solder-101/og.png');
  });
});

describe('identicalAutomaticBodies', () => {
  const p = (channel: string, body: string, wave = 'announce') => ({ channel, wave, body });
  it('is true only when both posts exist with the same text', () => {
    expect(identicalAutomaticBodies({ posts: [p('bluesky', 'a'), p('mastodon', 'a')] }, 'announce')).toBe(true);
    expect(identicalAutomaticBodies({ posts: [p('bluesky', 'a'), p('mastodon', 'b')] }, 'announce')).toBe(false);
    expect(identicalAutomaticBodies({ posts: [p('bluesky', 'a')] }, 'announce')).toBe(false);
    expect(identicalAutomaticBodies({ posts: [p('bluesky', 'a', 'reminder'), p('mastodon', 'a')] }, 'announce')).toBe(false);
  });
});

describe('copyablePostLink', () => {
  it('returns a safe remote_url and nothing else', () => {
    expect(copyablePostLink({ remote_url: 'https://bsky.app/p/1' })).toBe('https://bsky.app/p/1');
    expect(copyablePostLink({ remote_url: null })).toBeNull();
    expect(copyablePostLink({ remote_url: 'javascript:alert(1)' })).toBeNull();
  });
});
