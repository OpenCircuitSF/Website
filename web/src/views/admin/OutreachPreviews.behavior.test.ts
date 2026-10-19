// @vitest-environment jsdom
//
// #0583: mounts the Bluesky and Mastodon previews under jsdom. jsdom is not a
// browser (AGENTS.md section 1): the line clamp and the picture's real layout
// are not proved here, only what the markup says.
import { render, cleanup, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it } from 'vitest';
import type { OutreachChannel, OutreachPost } from '../../lib/types';
import OutreachPostPreview from './OutreachPostPreview.svelte';

afterEach(cleanup);

const bsky: OutreachChannel = { name: 'bluesky', label: 'Bluesky', utm_source: 'bluesky', utm_medium: 'social', char_limit: 300, automatic: true, configured: true, account: '@club.test' };
const mast: OutreachChannel = { ...bsky, name: 'mastodon', label: 'Mastodon', char_limit: 500, account: 'social.test' };

function post(over: Partial<OutreachPost>): OutreachPost {
  return {
    id: 1, outreach_id: 9, channel: 'bluesky', wave: 'announce', body: 'Join us #soldering https://go.example/b', status: 'draft', prepared: true, stale: false,
    customized: false, held: false, short_link_key: 'k', short_url: 'https://go.example/b', destination_url: 'https://x/w',
    utm: { source: 'bluesky', medium: 'social', campaign: 'c', content: 'announce' }, scheduled_at: null, posted_at: null, remote_url: null,
    last_error: null, created_at: '', updated_at: null, ...over,
  };
}
const workshop = (over: Record<string, unknown> = {}) => ({
  id: 5, slug: 'solder-101', title: 'Soldering 101', status: 'published', starts_at: null, summary: 'Learn to solder.', cover_image: '/media/cover.png', ...over,
});

describe('Bluesky preview', () => {
  it('styles hashtags and links, and draws the card with title, summary and short host', () => {
    const { container } = render(OutreachPostPreview, { view: { workshop: workshop(), thumb_ok: true }, channel: bsky, post: post({}) });
    expect(container.querySelector('.hashtag')?.textContent).toBe('#soldering');
    expect(container.querySelector('.link')?.textContent).toBe('https://go.example/b');
    const card = container.querySelector('.card') as HTMLElement;
    expect(card.textContent).toContain('Soldering 101');
    expect(card.textContent).toContain('Learn to solder.');
    expect(card.querySelector('.card-host')?.textContent).toBe('go.example');
    expect(card.querySelector('img')?.getAttribute('src')).toBe('/media/cover.png');
  });
  it('shows the site host when the canonical URL is configured', () => {
    const { container } = render(OutreachPostPreview, { view: { workshop: workshop(), bluesky_card: 'canonical', card_host: 'www.example.org' }, channel: bsky, post: post({}) });
    expect(container.querySelector('.card-host')?.textContent).toBe('www.example.org');
  });
  it('draws no picture when the server says the publisher would not attach one, and says so', () => {
    const { container } = render(OutreachPostPreview, { view: { workshop: workshop(), thumb_ok: false }, channel: bsky, post: post({}) });
    expect(container.querySelector('img')).toBeNull();
    expect(screen.getByText('No image: the card goes out without a picture.')).toBeTruthy();
  });
  it('shows the count against the limit', () => {
    render(OutreachPostPreview, { view: { workshop: workshop() }, channel: bsky, post: post({ body: 'abc' }) });
    expect(screen.getByText('3 / 300 characters')).toBeTruthy();
  });
});

describe('Mastodon preview', () => {
  const mPost = () => post({ id: 2, channel: 'mastodon', body: 'Sign up https://go.example/m #soldering' });
  it('styles links and hashtags and draws a link preview, falling back to the og image', () => {
    const { container } = render(OutreachPostPreview, { view: { workshop: workshop({ cover_image: null }) }, channel: mast, post: mPost() });
    expect(container.querySelector('.hashtag')?.textContent).toBe('#soldering');
    expect(container.querySelector('.link')?.textContent).toBe('https://go.example/m');
    expect(container.querySelector('.card img')?.getAttribute('src')).toBe('/workshops/solder-101/og.png');
    expect(container.querySelector('.card-host')?.textContent).toBe('Open Circuit SF');
  });
  it('draws no link preview when the text has no URL', () => {
    const { container } = render(OutreachPostPreview, { view: { workshop: workshop() }, channel: mast, post: post({ channel: 'mastodon', body: 'no link' }) });
    expect(container.querySelector('.card')).toBeNull();
  });
  it('has no image block until the workshop exposes cover_alt', () => {
    const { container } = render(OutreachPostPreview, { view: { workshop: workshop() }, channel: mast, post: mPost() });
    expect(container.querySelector('.attachment')).toBeNull();
    expect(container.textContent).not.toContain('alt text missing');
  });
  it('shows the image with an ALT badge when alt text is set', () => {
    const { container } = render(OutreachPostPreview, { view: { workshop: workshop({ cover_alt: 'A soldering iron' }) }, channel: mast, post: mPost() });
    expect(container.querySelector('.attachment .alt-badge')?.textContent).toBe('ALT');
    // Mastodon draws no link preview for a status that carries media.
    expect(container.querySelector('.card')).toBeNull();
  });
  it('says the image cannot go out when alt text is missing', () => {
    const { container } = render(OutreachPostPreview, { view: { workshop: workshop({ cover_alt: '' }) }, channel: mast, post: mPost() });
    expect(container.querySelector('.attachment')).toBeNull();
    expect(screen.getByText(/No image: alt text missing/)).toBeTruthy();
    // Nothing is attached, so the link preview comes back with its picture.
    expect(container.querySelector('.card img')?.getAttribute('src')).toBe('/media/cover.png');
  });
});

describe('other channels', () => {
  it('keep the plain text and count', () => {
    const reddit = { ...bsky, name: 'reddit', label: 'Reddit', char_limit: 0, automatic: false };
    const { container } = render(OutreachPostPreview, { view: { workshop: workshop() }, channel: reddit, post: post({ channel: 'reddit', body: 'plain text' }) });
    expect(container.querySelector('pre.body')?.textContent).toBe('plain text');
  });
});
