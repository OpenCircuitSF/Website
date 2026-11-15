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
const scheduleGuidePost = vi.fn<(id: number, at: string) => Promise<unknown>>();
vi.mock('../../lib/api', () => ({
  listGuidePosts: () => listGuidePosts(),
  getGuidePostsOverview: () => getGuidePostsOverview(),
  createGuidePost: (req: unknown) => createGuidePost(req),
  sendGuidePost: (id: number) => sendGuidePost(id),
  copyGuidePost: (id: number, c: string) => copyGuidePost(id, c),
  updateGuidePost: vi.fn(),
  scheduleGuidePost: (id: number, at: string) => scheduleGuidePost(id, at),
  unscheduleGuidePost: vi.fn(),
  deleteGuidePost: vi.fn(),
}));

const { default: GuidePosts } = await import('./GuidePosts.svelte');

afterEach(() => {
  cleanup();
  for (const m of [listGuidePosts, getGuidePostsOverview, createGuidePost, sendGuidePost, copyGuidePost, scheduleGuidePost]) m.mockReset();
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
    expect((screen.getByLabelText('Mastodon') as HTMLInputElement).checked).toBe(true);
    expect((screen.getByLabelText('Schedule for (Pacific, optional)') as HTMLInputElement).value).toMatch(/T09:00$/);
  });

  it('shows a live Bluesky count including the short link', async () => {
    setup('view=posts&guide=blink-led');
    await screen.findByLabelText('Text');
    const counter = screen.getByTestId('guide-post-counter-bluesky');
    const n = 'Make an LED blink. '.length + 'https://go.opencircuitsf.com/u/XXXXXXXXXXXX'.length;
    expect(counter.textContent?.trim()).toBe(`Bluesky: ≈ ${n} / 300 characters, including the short link`);
    // Mastodon counts every link as 23 characters, as the server does.
    const m = 'Make an LED blink. '.length + 23;
    expect(screen.getByTestId('guide-post-counter-mastodon').textContent?.trim()).toBe(`Mastodon: ≈ ${m} / 500 characters, including the short link`);
  });

  it("drops a channel's counter when it is unticked", async () => {
    setup('view=posts&guide=blink-led');
    await screen.findByLabelText('Text');
    await fireEvent.click(screen.getByLabelText('Mastodon'));
    expect(screen.queryByTestId('guide-post-counter-mastodon')).toBeNull();
    expect(screen.getByTestId('guide-post-counter-bluesky')).toBeTruthy();
  });

  it('blocks create over the character limit', async () => {
    setup('view=posts&guide=blink-led');
    const text = await screen.findByLabelText('Text');
    await fireEvent.input(text, { target: { value: 'x'.repeat(280) + ' {link}' } });
    expect(screen.getByTestId('guide-post-counter-bluesky').textContent).toContain('Too long for Bluesky.');
    expect(screen.getByTestId('guide-post-counter-mastodon').textContent).not.toContain('Too long');
    expect((screen.getByRole('button', { name: 'Create and schedule' }) as HTMLButtonElement).disabled).toBe(true);
    // Untick the channel that is over, and Create is allowed again.
    await fireEvent.click(screen.getByLabelText('Bluesky'));
    expect((screen.getByRole('button', { name: 'Create and schedule' }) as HTMLButtonElement).disabled).toBe(false);
  });

  it('creates a draft on both channels with the same text and reports each', async () => {
    setup('view=posts&guide=blink-led');
    createGuidePost.mockImplementation(async (req) => {
      const ch = (req as { channel: string }).channel;
      return ch === 'bluesky'
        ? { post: post({ id: 2, channel: 'bluesky', short_url: 'https://go.opencircuitsf.com/u/a' }), link_error: null }
        : { post: post({ id: 3, channel: 'mastodon', short_url: 'https://go.opencircuitsf.com/u/b' }), link_error: null };
    });
    await screen.findByLabelText('Text');
    await fireEvent.click(screen.getByRole('button', { name: 'Drafts only' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Create drafts' }));
    await waitFor(() => expect(createGuidePost).toHaveBeenCalledTimes(2));
    expect(createGuidePost).toHaveBeenNthCalledWith(1, { slug: 'blink-led', channel: 'bluesky', campaign: 'guides-2026-11', body: BODY });
    expect(createGuidePost).toHaveBeenNthCalledWith(2, { slug: 'blink-led', channel: 'mastodon', campaign: 'guides-2026-11', body: BODY });
    expect(scheduleGuidePost).not.toHaveBeenCalled();
    await waitFor(() =>
      expect(screen.getByRole('status').textContent).toContain(
        'Bluesky draft created with https://go.opencircuitsf.com/u/a. Mastodon draft created with https://go.opencircuitsf.com/u/b.',
      ),
    );
  });

  it('keeps the other draft when one channel fails, and warns on a failed short link', async () => {
    setup('view=posts&guide=blink-led');
    createGuidePost.mockImplementation(async (req) => {
      if ((req as { channel: string }).channel === 'mastodon') throw new Error('the server could not save the draft');
      return { post: post({ id: 2, short_url: null }), link_error: 'not_configured' };
    });
    await screen.findByLabelText('Text');
    await fireEvent.click(screen.getByRole('button', { name: 'Drafts only' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Create drafts' }));
    await waitFor(() => expect(createGuidePost).toHaveBeenCalledTimes(2));
    const status = await waitFor(() => {
      const t = screen.getByRole('status').textContent ?? '';
      expect(t).toContain('Bluesky draft created, but its short link could not be made');
      return t;
    });
    expect(status).toContain('Mastodon: the draft was not created. the server could not save the draft.');
    // The form stays open with only the failed channel ticked, ready to retry.
    expect((screen.getByLabelText('Bluesky') as HTMLInputElement).checked).toBe(false);
    expect((screen.getByLabelText('Mastodon') as HTMLInputElement).checked).toBe(true);
    expect(screen.getByRole('button', { name: 'Create draft' })).toBeTruthy();
  });

  it('schedules both new drafts for the chosen time and reports a schedule failure per channel', async () => {
    setup('view=posts&guide=blink-led');
    createGuidePost.mockImplementation(async (req) => {
      const ch = (req as { channel: string }).channel;
      return { post: post({ id: ch === 'bluesky' ? 2 : 3, channel: ch }), link_error: null };
    });
    scheduleGuidePost.mockImplementation(async (id) => {
      if (id === 3) throw new Error('Mastodon is not connected');
      return post({ id, status: 'scheduled' });
    });
    await screen.findByLabelText('Text');
    const when = screen.getByLabelText('Schedule for (Pacific, optional)') as HTMLInputElement;
    await fireEvent.input(when, { target: { value: '2026-11-20T09:00' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Create and schedule' }));
    await waitFor(() => expect(scheduleGuidePost).toHaveBeenCalledTimes(2));
    expect(scheduleGuidePost).toHaveBeenNthCalledWith(1, 2, '2026-11-20T17:00:00Z');
    expect(scheduleGuidePost).toHaveBeenNthCalledWith(2, 3, '2026-11-20T17:00:00Z');
    await waitFor(() => {
      const t = screen.getByRole('status').textContent ?? '';
      expect(t).toContain('Bluesky draft created with https://go.opencircuitsf.com/u/k1, and scheduled for Nov 20, 2026, 9:00 AM PT.');
      expect(t).toContain('Mastodon draft created with https://go.opencircuitsf.com/u/k1. It was not scheduled: Mastodon is not connected.');
    });
  });

  it('shows an unconnected channel unticked and disabled in the composer', async () => {
    const l = list();
    l.channels[1].configured = false;
    setup('view=posts&guide=blink-led', l);
    await screen.findByLabelText('Text');
    const m = screen.getByLabelText('Mastodon') as HTMLInputElement;
    expect(m.checked).toBe(false);
    expect(m.disabled).toBe(true);
    expect(screen.getByText(/Mastodon is not connected/)).toBeTruthy();
    expect(screen.queryByTestId('guide-post-counter-mastodon')).toBeNull();
  });

  it('shows posts made together as one share, each card keeping its actions', async () => {
    const pair = [
      post({ id: 1, channel: 'bluesky', created_at: '2026-11-10T10:00:00Z' }),
      post({ id: 2, channel: 'mastodon', created_at: '2026-11-10T10:00:02Z' }),
    ];
    setup('view=posts', list({ posts: pair }));
    const share = await waitFor(() => {
      const s = document.querySelector('[data-share="1,2"]');
      expect(s).not.toBeNull();
      return s as HTMLElement;
    });
    expect(within(share).getByText('Shared together on Bluesky and Mastodon')).toBeTruthy();
    expect(within(share).getAllByRole('button', { name: 'Post now…' })).toHaveLength(2);
  });

  it('schedules a posted pair again on both channels with one campaign prompt', async () => {
    const pair = [
      post({ id: 1, channel: 'bluesky', status: 'posted', posted_at: '2026-11-03T00:00:00Z' }),
      post({ id: 2, channel: 'mastodon', status: 'posted', posted_at: '2026-11-03T00:00:00Z' }),
    ];
    setup('view=posts', list({ posts: pair, suggested_campaign: 'guides-2026-12' }));
    copyGuidePost.mockImplementation(async (id, c) => ({ post: post({ id: id + 10, campaign_slug: c, short_url: `https://go.opencircuitsf.com/u/n${id}` }), link_error: null }));
    await fireEvent.click(await screen.findByRole('button', { name: 'Schedule again on Bluesky and Mastodon…' }));
    const dialog = await screen.findByRole('dialog');
    const input = within(dialog).getByLabelText('Campaign') as HTMLInputElement;
    expect(input.value).toBe('guides-2026-12');
    await fireEvent.input(input, { target: { value: 'guides-winter' } });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Create drafts' }));
    await waitFor(() => expect(copyGuidePost).toHaveBeenCalledTimes(2));
    expect(copyGuidePost).toHaveBeenNthCalledWith(1, 1, 'guides-winter');
    expect(copyGuidePost).toHaveBeenNthCalledWith(2, 2, 'guides-winter');
    await waitFor(() =>
      expect(screen.getByRole('status').textContent).toContain(
        'Bluesky draft created with https://go.opencircuitsf.com/u/n1. Mastodon draft created with https://go.opencircuitsf.com/u/n2.',
      ),
    );
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
