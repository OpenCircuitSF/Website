// @vitest-environment jsdom
//
// #0660: mounts GuidePosts.svelte under jsdom with '../../lib/api' mocked.
// jsdom is not a browser (AGENTS.md §1): this proves what the page renders
// and calls, not how it looks.
import { render, cleanup, waitFor, screen, fireEvent, within } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { GuidePostCreated, GuidePostRow, GuidePostsList, GuidePostsOverview, GuideShare } from '../../lib/types';

const listGuidePosts = vi.fn<() => Promise<GuidePostsList>>();
const getGuidePostsOverview = vi.fn<() => Promise<GuidePostsOverview>>();
const createGuidePost = vi.fn<(req: unknown) => Promise<GuidePostCreated>>();
const sendGuidePost = vi.fn<(id: number) => Promise<unknown>>();
const copyGuidePost = vi.fn<(id: number, c: string) => Promise<GuidePostCreated>>();
vi.mock('../../lib/api', () => ({
  listGuidePosts: () => listGuidePosts(),
  getGuidePostsOverview: () => getGuidePostsOverview(),
  createGuidePost: (req: unknown) => createGuidePost(req),
  sendGuidePost: (id: number) => sendGuidePost(id),
  copyGuidePost: (id: number, c: string) => copyGuidePost(id, c),
  updateGuidePost: vi.fn(),
  scheduleGuidePost: vi.fn(),
  unscheduleGuidePost: vi.fn(),
  deleteGuidePost: vi.fn(),
}));

const { default: GuidePosts } = await import('./GuidePosts.svelte');

afterEach(() => {
  cleanup();
  for (const m of [listGuidePosts, getGuidePostsOverview, createGuidePost, sendGuidePost, copyGuidePost]) m.mockReset();
});

const BODY = 'Make an LED blink. {link}';

function post(over: Partial<GuidePostRow> = {}): GuidePostRow {
  return {
    id: 1,
    guide_slug: 'blink-led',
    channel: 'bluesky',
    body: BODY,
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
    clicks: 4,
    signups: { requested: 2, confirmed: 1 },
    ...over,
  };
}

function list(over: Partial<GuidePostsList> = {}): GuidePostsList {
  return {
    topics: [
      { slug: 'blink-led', title: 'Blink an LED', hook: 'h', post: 'p', url: 'https://x/blink-led', default_body: BODY },
      { slug: 'read-button', title: 'Read a button', hook: 'h', post: 'p', url: 'https://x/read-button', default_body: 'Buttons! {link}' },
    ],
    posts: [post()],
    channels: [
      { name: 'bluesky', label: 'Bluesky', char_limit: 300, configured: true },
      { name: 'mastodon', label: 'Mastodon', char_limit: 500, configured: true },
    ],
    post_hour: 9,
    suggested_campaign: 'guides-2026-11',
    clicks_error: null,
    signups_error: null,
    ...over,
  };
}

function share(over: Partial<GuideShare> = {}): GuideShare {
  return {
    slug: 'blink-led',
    title: 'Blink an LED',
    url: 'https://x/blink-led',
    last_posted_at: '2026-11-03T00:00:00Z',
    last_campaign: 'guides-2026-11',
    channels: ['bluesky'],
    campaigns: ['guides-2026-11'],
    posted_count: 1,
    next_scheduled_at: null,
    clicks: 4,
    signups: { requested: 2, confirmed: 1 },
    ...over,
  };
}

function overview(): GuidePostsOverview {
  return {
    guides: [
      share({ slug: 'blink-led', title: 'Blink an LED', clicks: 4 }),
      share({ slug: 'read-button', title: 'Read a button', last_posted_at: null, last_campaign: '', channels: [], clicks: null, signups: null }),
      share({ slug: 'servo-sweep', title: 'Sweep a servo', last_posted_at: '2026-10-01T00:00:00Z', clicks: 9 }),
    ],
    clicks_error: null,
    signups_error: null,
  };
}

function setup(q: string, l: GuidePostsList = list()) {
  listGuidePosts.mockResolvedValue(l);
  getGuidePostsOverview.mockResolvedValue(overview());
  return render(GuidePosts, { query: new URLSearchParams(q) });
}

const rowSlugs = () => Array.from(document.querySelectorAll('tbody tr')).map((r) => r.getAttribute('data-slug'));

describe('GuidePosts', () => {
  it('shows the recently shared table first, with a never-posted guide', async () => {
    setup('');
    await screen.findByText('Recently shared', { selector: 'header' });
    expect(screen.getByText('Signups since first shared')).toBeTruthy();
    const never = document.querySelector('tr.never');
    expect(never?.getAttribute('data-slug')).toBe('read-button');
    expect(within(never as HTMLElement).getByText('Never')).toBeTruthy();
    const draft = within(never as HTMLElement).getByText('Draft a post') as HTMLAnchorElement;
    expect(draft.getAttribute('href')).toBe('#/guide-posts?view=posts&guide=read-button');
  });

  it('sorts by oldest share by default and re-sorts on change', async () => {
    setup('');
    await screen.findByText('Recently shared', { selector: 'header' });
    expect(rowSlugs()).toEqual(['read-button', 'servo-sweep', 'blink-led']);
    await fireEvent.change(screen.getByLabelText('Sort'), { target: { value: 'clicks' } });
    expect(rowSlugs()).toEqual(['servo-sweep', 'blink-led', 'read-button']);
  });

  it('prefills a new draft from the guide copy and the suggested campaign', async () => {
    setup('view=posts&guide=blink-led');
    const text = (await screen.findByLabelText('Text')) as HTMLTextAreaElement;
    expect(text.value).toBe(BODY);
    expect((screen.getByLabelText('Campaign') as HTMLInputElement).value).toBe('guides-2026-11');
    expect((screen.getByLabelText('Guide') as HTMLSelectElement).value).toBe('blink-led');
    expect((screen.getByLabelText('Bluesky') as HTMLInputElement).checked).toBe(true);
  });

  it('shows a live Bluesky count including the short link', async () => {
    setup('view=posts&guide=blink-led');
    await screen.findByLabelText('Text');
    const counter = screen.getByTestId('guide-post-counter');
    const n = 'Make an LED blink. '.length + 'https://go.opencircuitsf.com/u/XXXXXXXXXXXX'.length;
    expect(counter.textContent?.trim()).toBe(`≈ ${n} / 300 characters, including the short link`);
  });

  it('blocks create over the character limit', async () => {
    setup('view=posts&guide=blink-led');
    const text = await screen.findByLabelText('Text');
    await fireEvent.input(text, { target: { value: 'x'.repeat(280) + ' {link}' } });
    expect(screen.getByTestId('guide-post-counter').textContent).toContain('Too long for Bluesky.');
    expect((screen.getByRole('button', { name: 'Create draft' }) as HTMLButtonElement).disabled).toBe(true);
  });

  it('creates a draft and warns when the short link failed', async () => {
    setup('view=posts&guide=blink-led');
    createGuidePost.mockResolvedValue({ post: post({ id: 2, short_url: null }), link_error: 'not_configured' });
    await screen.findByLabelText('Text');
    await fireEvent.click(screen.getByRole('button', { name: 'Create draft' }));
    await waitFor(() => expect(createGuidePost).toHaveBeenCalledWith({ slug: 'blink-led', channel: 'bluesky', campaign: 'guides-2026-11', body: BODY }));
    await waitFor(() => expect(screen.getByRole('status').textContent).toContain('its short link could not be made'));
  });

  it('shows an unconnected channel as unavailable, not an error', async () => {
    const l = list();
    l.channels[0].configured = false;
    setup('view=posts', l);
    const card = await waitFor(() => {
      const c = document.querySelector('[data-post-id="1"]');
      expect(c).not.toBeNull();
      return c as HTMLElement;
    });
    expect((within(card).getByRole('button', { name: 'Schedule…' }) as HTMLButtonElement).disabled).toBe(true);
    expect((within(card).getByRole('button', { name: 'Post now…' }) as HTMLButtonElement).disabled).toBe(true);
    expect(within(card).getByText(/Bluesky is not connected/)).toBeTruthy();
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('confirms before posting now', async () => {
    setup('view=posts');
    sendGuidePost.mockResolvedValue(post({ status: 'queued' }));
    const btn = await screen.findByRole('button', { name: 'Post now…' });
    await fireEvent.click(btn);
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText(/right away and can't be undone/)).toBeTruthy();
    expect(sendGuidePost).not.toHaveBeenCalled();
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Post now' }));
    await waitFor(() => expect(sendGuidePost).toHaveBeenCalledWith(1));
  });

  it('schedules again under a new campaign', async () => {
    setup('view=posts', list({ posts: [post({ status: 'posted', posted_at: '2026-11-03T00:00:00Z' })], suggested_campaign: 'guides-2026-12' }));
    copyGuidePost.mockResolvedValue({ post: post({ id: 3, campaign_slug: 'guides-2026-12' }), link_error: null });
    await fireEvent.click(await screen.findByRole('button', { name: 'Schedule again…' }));
    const dialog = await screen.findByRole('dialog');
    const input = within(dialog).getByLabelText('Campaign') as HTMLInputElement;
    expect(input.value).toBe('guides-2026-12');
    await fireEvent.input(input, { target: { value: 'Bad Name' } });
    expect((within(dialog).getByRole('button', { name: 'Create draft' }) as HTMLButtonElement).disabled).toBe(true);
    await fireEvent.input(input, { target: { value: 'guides-winter' } });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Create draft' }));
    await waitFor(() => expect(copyGuidePost).toHaveBeenCalledWith(1, 'guides-winter'));
  });

  it('shows clicks and signups by campaign and by post', async () => {
    setup('view=results', list({ posts: [post(), post({ id: 2, campaign_slug: 'guides-2026-10', clicks: 1, signups: { requested: 1, confirmed: 1 } })] }));
    await screen.findByText('By campaign', { selector: 'header' });
    const byCampaign = document.querySelector('[data-table="campaigns"]') as HTMLElement;
    expect(within(byCampaign).getByText('guides-2026-11')).toBeTruthy();
    expect(within(byCampaign).getByText('4 clicks')).toBeTruthy();
    expect(within(byCampaign).getByText('1 signup, 1 confirmed')).toBeTruthy();
    const byPost = document.querySelector('[data-table="posts"]') as HTMLElement;
    expect(byPost.querySelectorAll('tbody tr')).toHaveLength(2);
    expect(within(byPost).getAllByText('Blink an LED')).toHaveLength(2);
  });

  it('keeps the page usable when click counts fail', async () => {
    setup('view=posts', list({ clicks_error: 'timeout', posts: [post({ clicks: null })] }));
    await waitFor(() => expect(document.querySelector('[data-post-id="1"]')).not.toBeNull());
    expect(screen.getByRole('status').textContent).toContain('Some click counts are unavailable right now (timed out).');
    expect(screen.queryByRole('alert')).toBeNull();
  });
});
