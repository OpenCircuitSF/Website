// @vitest-environment jsdom
//
// #0569, #0582: mounts OutreachPostCards.svelte (real OutreachPostCard,
// OutreachComposer, dialogs) under jsdom with '../../lib/api' mocked. The list
// owns no data, so the test plays the panel's part: onChange swaps the changed
// post into the view and re-renders. jsdom is not a browser (AGENTS.md §1): no
// browser was run for this change.
import { render, cleanup, waitFor, screen, fireEvent, within } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { fromLocalInputValue } from '../../lib/outreach';
import type { OutreachPost, WorkshopOutreach } from '../../lib/types';

const sendOutreachPost = vi.fn();
const updateOutreachPost = vi.fn();
const unmarkOutreachPosted = vi.fn();
const takedownOutreachPost = vi.fn();
const markOutreachPosted = vi.fn();
const prepareOutreachPost = vi.fn();
const scheduleOutreachPost = vi.fn();
const unscheduleOutreachPost = vi.fn();
const deleteOutreachPost = vi.fn();
const reloadMock = vi.fn();
vi.mock('../../lib/api', () => ({
  draftAllOutreach: vi.fn(),
  createOutreachPost: vi.fn(),
  sendOutreachPost: (id: number) => sendOutreachPost(id),
  unscheduleOutreachPost: (id: number) => unscheduleOutreachPost(id),
  markOutreachPosted: (id: number, url?: string) => markOutreachPosted(id, url),
  unmarkOutreachPosted: (id: number) => unmarkOutreachPosted(id),
  takedownOutreachPost: (id: number) => takedownOutreachPost(id),
  updateOutreachPost: (id: number, b: unknown) => updateOutreachPost(id, b),
  prepareOutreachPost: (id: number) => prepareOutreachPost(id),
  scheduleOutreachPost: (id: number, at: string) => scheduleOutreachPost(id, at),
  deleteOutreachPost: (id: number) => deleteOutreachPost(id),
  ApiError: class ApiError extends Error {},
}));

const { default: OutreachPostCards } = await import('./OutreachPostCards.svelte');

afterEach(() => {
  cleanup();
  sendOutreachPost.mockReset();
  unmarkOutreachPosted.mockReset();
  takedownOutreachPost.mockReset();
  markOutreachPosted.mockReset();
  prepareOutreachPost.mockReset();
  updateOutreachPost.mockReset();
  scheduleOutreachPost.mockReset();
  unscheduleOutreachPost.mockReset();
  deleteOutreachPost.mockReset();
  reloadMock.mockReset();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

const channels = [
  { name: 'bluesky', label: 'Bluesky', utm_source: 'bluesky', utm_medium: 'social', char_limit: 300, automatic: true, configured: true, account: '@club.test' },
  { name: 'reddit', label: 'Reddit', utm_source: 'reddit', utm_medium: 'social', char_limit: 0, automatic: false, configured: false, account: '' },
];
const waves = [
  { name: 'announce', label: 'Announce', suggested_at: null },
  { name: 'reminder', label: 'Reminder', suggested_at: null },
];

function post(over: Partial<OutreachPost>): OutreachPost {
  return {
    id: 1, outreach_id: 9, channel: 'bluesky', wave: 'announce', body: 'Come solder with us https://go.example/abc', status: 'draft',
    prepared: true, stale: false, customized: false, held: false, short_link_key: 'abc', short_url: 'https://go.example/abc', destination_url: 'https://x/w',
    utm: { source: 'bluesky', medium: 'social', campaign: 'c', content: 'announce' }, scheduled_at: null, posted_at: null, remote_url: null,
    last_error: null, created_at: '', updated_at: null, ...over,
  };
}

function viewOf(over: Partial<WorkshopOutreach>): WorkshopOutreach {
  return {
    available: true, unavailable_reason: '',
    workshop: { id: 5, slug: 's', title: 'Soldering 101', status: 'published', starts_at: '2026-12-05T18:00:00Z' },
    outreach: { id: 9, workshop_id: 5, campaign_slug: 's', created_at: '' },
    channels, waves, posts: [], ...over,
  };
}

/** Mounts the list and plays the panel: a change replaces the post in the view and re-renders. */
function mountView(view: WorkshopOutreach, flyer?: { href: string; label: string }, extra: Record<string, unknown> = {}): void {
  let current = view;
  const r = render(OutreachPostCards, {
    view,
    flyer,
    ...extra,
    results: null,
    reload: reloadMock,
    onChange: (p: OutreachPost) => {
      current = { ...current, posts: current.posts.map((x) => (x.id === p.id ? p : x)) };
      void r.rerender({ view: current });
    },
  });
}

function setup(posts: OutreachPost[]): void {
  mountView(viewOf({ posts }));
}

describe('post cards (#0569)', () => {
  it('a posted card keeps its preview and Copy link to post writes the remote URL (#0583)', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    setup([post({ id: 1, status: 'posted', posted_at: '2026-10-17T20:00:00Z', remote_url: 'https://bsky.app/p/1' })]);
    const card = await screen.findByRole('article', { name: 'Bluesky Announce' });
    expect(card.querySelector('.preview .card')).toBeTruthy();
    await fireEvent.click(within(card).getByRole('button', { name: 'Copy link to post' }));
    expect(writeText).toHaveBeenCalledWith('https://bsky.app/p/1');
    expect(await within(card).findByRole('button', { name: 'Copied' })).toBeTruthy();
  });

  it('a posted card without a stored URL offers no Copy link to post (#0583)', async () => {
    setup([post({ id: 1, status: 'posted', posted_at: '2026-10-17T20:00:00Z', remote_url: null })]);
    await screen.findByRole('article', { name: 'Bluesky Announce' });
    expect(screen.queryByRole('button', { name: 'Copy link to post' })).toBeNull();
  });

  it('shows the full body and each status line, grouped by wave', async () => {
    setup([
      post({ id: 1, status: 'posted', posted_at: '2026-10-17T20:00:00Z', remote_url: 'https://bsky.app/p/1' }),
      post({ id: 2, channel: 'reddit', status: 'manual', body: 'Reddit body text' }),
      post({ id: 3, wave: 'reminder', status: 'scheduled', scheduled_at: '2026-11-30T20:00:00Z', body: 'Reminder body' }),
      post({ id: 4, wave: 'reminder', channel: 'reddit', status: 'failed', body: 'Failing body', last_error: 'raw' }),
    ]);
    expect(await screen.findByText(/Come solder with us/)).toBeTruthy();
    expect(screen.getByText('Reddit body text', { selector: 'pre.body' })).toBeTruthy();
    expect(screen.getByText('Reminder body')).toBeTruthy();
    expect(screen.getByRole('link', { name: 'view post' }).getAttribute('href')).toBe('https://bsky.app/p/1');
    expect(screen.getByText(/^Posts automatically /)).toBeTruthy();
    expect(screen.getByText('No set day: post when you are ready.')).toBeTruthy();
    expect(screen.getByText('Failed: the post could not be sent')).toBeTruthy();
    const headings = screen.getAllByRole('heading', { level: 4 }).map((h) => h.textContent);
    expect(headings).toEqual(['Announce', 'Reminder']);
  });

  it('shows Not scheduled for an automatic draft and Not drafted with a Draft button', async () => {
    setup([post({ id: 1 })]);
    expect(await screen.findByText('Not scheduled')).toBeTruthy();
    // A hand row with no post yet says when to post instead (#0635).
    expect(screen.getAllByText('Not drafted').length).toBe(1);
    expect(screen.getAllByText('No set day: post when you are ready.').length).toBe(2);
    expect(screen.getAllByRole('button', { name: 'Draft' }).length).toBe(3);
  });

  it('automatic cards offer Post now and Schedule…, manual cards offer Copy with link and Copy text only and not Post now', async () => {
    setup([post({ id: 1 }), post({ id: 2, channel: 'reddit', status: 'manual' })]);
    await screen.findByText('Not scheduled');
    expect(screen.getAllByRole('button', { name: 'Post now' }).length).toBe(1);
    expect(screen.getAllByRole('button', { name: 'Schedule…' }).length).toBe(1);
    expect(screen.getAllByRole('button', { name: 'Copy with link' }).length).toBe(1);
    expect(screen.getAllByRole('button', { name: 'Copy text only' }).length).toBe(1);
    expect(screen.getAllByRole('button', { name: 'Mark posted' }).length).toBe(1);
    const manual = screen.getByRole('article', { name: 'Reddit Announce' });
    expect(manual.textContent).not.toContain('Post now');
  });

  it('the composer says Announce usually goes out now instead of "no suggested time" (#0587)', async () => {
    setup([post({ id: 1 })]);
    await screen.findByText('Not scheduled');
    await fireEvent.click(screen.getByRole('button', { name: 'Schedule…' }));
    expect(screen.getByText('The announcement usually goes out now. Pick a time only if you want to wait.')).toBeTruthy();
    expect(screen.queryByText(/No suggested time/)).toBeNull();
  });

  it('the editor row is Save and Cancel with Delete set apart; posting stays on the card (#0591)', async () => {
    setup([post({ id: 1 })]);
    await screen.findByText('Not scheduled');
    await fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    const card = screen.getByRole('article', { name: 'Bluesky Announce' });
    const row = Array.from(card.querySelectorAll('.row')).find((r) => r.textContent?.includes('Save'));
    expect(row).toBeTruthy();
    const names = Array.from(row!.querySelectorAll('button')).map((b) => b.textContent?.trim());
    expect(names.slice(0, 2)).toEqual(['Save', 'Cancel']);
    expect(names[names.length - 1]).toBe('Delete');
    expect(row!.querySelector('.danger-end button')?.textContent?.trim()).toBe('Delete');
    for (const gone of ['Post now', 'Retry', 'Mark posted', 'Copy content']) {
      expect(screen.queryByRole('button', { name: gone })).toBeNull();
    }
  });

  it('Edit opens the composer inline in the card', async () => {
    setup([post({ id: 1 })]);
    await screen.findByText('Not scheduled');
    expect(screen.queryByLabelText('Post text')).toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    const card = screen.getByRole('article', { name: 'Bluesky Announce' });
    expect(card.querySelector('textarea')).toBeTruthy();
    expect(card.textContent).toMatch(/\/ 300 characters/);
  });

  it('Edit replaces the card display with the composer; Cancel restores it (#0579)', async () => {
    setup([post({ id: 1 })]);
    await screen.findByText('Not scheduled');
    const card = () => screen.getByRole('article', { name: 'Bluesky Announce' });
    expect(card().querySelector('.preview .text')).toBeTruthy();
    await fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    expect(card().querySelector('.preview .text')).toBeNull();
    expect(card().textContent).not.toContain('Not scheduled');
    expect(card().querySelector('textarea')).toBeTruthy();
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(card().querySelector('textarea')).toBeNull();
    expect(card().querySelector('.preview .text')).toBeTruthy();
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Edit' })));
  });

  it('Save returns to display mode showing the new text (#0579)', async () => {
    setup([post({ id: 1 })]);
    await screen.findByText('Not scheduled');
    updateOutreachPost.mockResolvedValue(post({ id: 1, body: 'Brand new words' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    await fireEvent.input(screen.getByLabelText('Post text'), { target: { value: 'Brand new words' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(screen.getByRole('article', { name: 'Bluesky Announce' }).querySelector('textarea')).toBeNull());
    expect(screen.getByText('Brand new words')).toBeTruthy();
  });

  it('opening a second card asks first in an in-app dialog when edits are unsaved (#0579, #0581)', async () => {
    const confirmFn = vi.fn(() => true);
    vi.stubGlobal('confirm', confirmFn);
    setup([post({ id: 1 }), post({ id: 2, wave: 'reminder', body: 'Second post' })]);
    await screen.findAllByText('Not scheduled');
    await fireEvent.click(screen.getAllByRole('button', { name: 'Edit' })[0]);
    await fireEvent.input(screen.getByLabelText('Post text'), { target: { value: 'dirty' } });
    await fireEvent.click(screen.getAllByRole('button', { name: 'Edit' })[0]);
    const dlg = screen.getByRole('dialog');
    expect(dlg.textContent).toContain('Discard your unsaved changes?');
    expect(document.querySelectorAll('textarea').length).toBe(1);
    expect((screen.getByLabelText('Post text') as HTMLTextAreaElement).value).toBe('dirty');
    await fireEvent.click(within(dlg).getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect((screen.getByLabelText('Post text') as HTMLTextAreaElement).value).toBe('dirty');
    await fireEvent.click(screen.getAllByRole('button', { name: 'Edit' })[0]);
    await fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Discard changes' }));
    expect(document.querySelectorAll('textarea').length).toBe(1);
    expect((screen.getByLabelText('Post text') as HTMLTextAreaElement).value).toBe('Second post');
    expect(confirmFn).not.toHaveBeenCalled();
  });

  it('Post now opens a dialog with the text and account; Cancel sends nothing, Post sends once (#0581)', async () => {
    const confirmFn = vi.fn(() => true);
    vi.stubGlobal('confirm', confirmFn);
    setup([post({ id: 1 })]);
    await screen.findByText('Not scheduled');
    await fireEvent.click(screen.getByRole('button', { name: 'Post now' }));
    const dlg = screen.getByRole('dialog');
    expect(dlg.textContent).toContain('Come solder with us');
    expect(dlg.textContent).toContain('Posts publicly as @club.test on Bluesky');
    expect(dlg.textContent).toContain('cannot be unsent');
    await fireEvent.click(within(dlg).getByRole('button', { name: 'Cancel' }));
    expect(sendOutreachPost).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).toBeNull();
    sendOutreachPost.mockResolvedValue(post({ id: 1, status: 'queued' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Post now' }));
    await fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Post now' }));
    await waitFor(() => expect(sendOutreachPost).toHaveBeenCalledTimes(1));
    expect(sendOutreachPost).toHaveBeenCalledWith(1);
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(confirmFn).not.toHaveBeenCalled();
  });

  it('the notice lands under the right card, not on a sibling (#0581)', async () => {
    setup([post({ id: 1 }), post({ id: 2, wave: 'reminder', body: 'Second post' })]);
    await screen.findAllByText('Not scheduled');
    sendOutreachPost.mockResolvedValue(post({ id: 2, wave: 'reminder', body: 'Second post', status: 'queued' }));
    const reminder = screen.getByRole('article', { name: 'Bluesky Reminder' });
    await fireEvent.click(within(reminder).getByRole('button', { name: 'Post now' }));
    await fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Post now' }));
    await waitFor(() => expect(within(reminder).getByRole('status').textContent).toMatch(/^Queued\./));
    const announce = screen.getByRole('article', { name: 'Bluesky Announce' });
    expect(within(announce).getByRole('status').textContent).toBe('');
    expect(reminder.querySelector('.spinner')).toBeTruthy();
    expect(reminder.textContent).toContain('usually under ten seconds');
  });

  it('Mark posted opens a dialog whose link field rejects a non-http URL (#0581)', async () => {
    setup([post({ id: 2, channel: 'reddit', status: 'manual', body: 'Reddit body' })]);
    await screen.findByText('Reddit body', { selector: 'pre.body' });
    await fireEvent.click(screen.getByRole('button', { name: 'Mark posted' }));
    const dlg = screen.getByRole('dialog');
    await fireEvent.input(within(dlg).getByLabelText(/Link to the post/), { target: { value: 'ftp://x.example/1' } });
    expect((within(dlg).getByRole('button', { name: 'Mark posted' }) as HTMLButtonElement).disabled).toBe(true);
    expect(dlg.textContent).toContain('http(s)');
  });

  it('Copy with link flips to Copied for two seconds, and copies the link', async () => {
    setup([post({ id: 2, channel: 'reddit', status: 'manual', body: 'Reddit body' })]);
    await screen.findByText('Reddit body', { selector: 'pre.body' });
    const writeText = vi.fn(() => Promise.resolve());
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    await fireEvent.click(screen.getByRole('button', { name: 'Copy with link' }));
    await vi.advanceTimersByTimeAsync(0);
    expect(writeText).toHaveBeenCalledWith('Reddit body\n\nSign up: https://go.example/abc');
    expect(screen.getByRole('button', { name: 'Copied' })).toBeTruthy();
    await vi.advanceTimersByTimeAsync(1999);
    expect(screen.getByRole('button', { name: 'Copied' })).toBeTruthy();
    await vi.advanceTimersByTimeAsync(1);
    expect(screen.getByRole('button', { name: 'Copy with link' })).toBeTruthy();
    expect(screen.getByRole('article', { name: 'Reddit Announce' }).querySelector('.notice')?.textContent).toBe('');
    vi.useRealTimers();
  });
});

describe('post cards: one link, Instagram, video channels (#0576)', () => {
  const chans = [
    ...channels,
    { name: 'instagram', label: 'Instagram', utm_source: 'instagram', utm_medium: 'social', char_limit: 0, automatic: false, configured: false, account: '' },
    { name: 'tiktok', label: 'TikTok', utm_source: 'tiktok', utm_medium: 'video', char_limit: 0, automatic: false, configured: false, account: '' },
  ];
  function setupAll(posts: OutreachPost[]): void {
    mountView(viewOf({ channels: chans, waves: [waves[0]], posts }));
  }
  const igBody = 'Soldering 101: Saturday.\n\nSign up at the link in our bio.';

  it('does not print the short link under the body; manual cards get one labelled Put this in your bio row', async () => {
    setupAll([
      post({ id: 1, body: 'Bluesky text without a link' }),
      post({ id: 2, channel: 'instagram', status: 'manual', body: igBody }),
    ]);
    await screen.findByText('Bluesky text without a link');
    const bluesky = screen.getByRole('article', { name: 'Bluesky Announce' });
    expect(bluesky.textContent).not.toContain('https://go.example/abc');
    expect(bluesky.textContent).not.toContain('Put this in your bio');
    const ig = screen.getByRole('article', { name: 'Instagram Reels Announce' });
    expect(ig.textContent).toContain('Put this in your bio');
    // Once in the text that will be copied and once in the link row.
    expect(ig.textContent?.split('https://go.example/abc').length).toBe(3);
    expect(ig.querySelector('pre')?.textContent).toBe('Soldering 101: Saturday.\n\nSign up: https://go.example/abc');
    expect(screen.getByRole('button', { name: 'Copy link' })).toBeTruthy();
  });

  it('Instagram Reels offers both copies and Open flyer', async () => {
    setupAll([post({ id: 2, channel: 'instagram', status: 'manual', body: igBody })]);
    await screen.findByText(/Sign up at the link in our bio/, { selector: 'pre' });
    const ig = screen.getByRole('article', { name: 'Instagram Reels Announce' });
    expect(within(ig).getByRole('button', { name: 'Copy with link' })).toBeTruthy();
    expect(within(ig).getByRole('button', { name: 'Copy text only' })).toBeTruthy();
    const link = ig.querySelector('a.open-flyer');
    expect(link?.textContent).toBe('Open flyer');
    expect(link?.getAttribute('href')).toMatch(/flyers/);
    const bsky = screen.getByRole('article', { name: 'Bluesky Announce' });
    expect(bsky.querySelector('a.open-flyer')).toBeNull();
  });

  it('Instagram keeps its flyer link when posted and when not written', async () => {
    setupAll([post({ id: 2, channel: 'instagram', status: 'posted', body: igBody, posted_at: '2026-10-18T17:00:00Z' })]);
    expect((await screen.findByRole('article', { name: 'Instagram Reels Announce' })).querySelector('a.open-flyer')).toBeTruthy();
    cleanup();
    setupAll([post({ id: 1, body: 'Bluesky words' })]);
    expect((await screen.findByRole('article', { name: 'Instagram Reels Announce' })).querySelector('a.open-flyer')).toBeTruthy();
  });

  it('with a section name, lists Instagram, TikTok and YouTube under one Video heading, open, one row each', async () => {
    const yt = { ...chans[3], name: 'youtube', label: 'YouTube Shorts', utm_source: 'youtube' };
    mountView(
      viewOf({
        channels: [...chans, yt],
        posts: [
          post({ id: 2, channel: 'instagram', status: 'manual', body: igBody }),
          post({ id: 3, channel: 'tiktok', status: 'manual', body: 'TikTok words' }),
          post({ id: 4, channel: 'youtube', status: 'manual', body: 'YouTube words' }),
          post({ id: 5, wave: 'reminder', channel: 'tiktok', status: 'manual', body: 'Later words' }),
        ],
      }),
      undefined,
      { wave: 'announce', channelFilter: (c: { automatic: boolean }) => !c.automatic },
    );
    expect(await screen.findByRole('heading', { name: 'Video' })).toBeTruthy();
    expect(document.querySelector('details.video')).toBeNull();
    for (const name of ['Instagram Reels Announce', 'TikTok Announce', 'YouTube Shorts Announce']) {
      const row = screen.getByRole('article', { name });
      expect(row.closest('details')).toBeNull();
      expect(row.compareDocumentPosition(screen.getByRole('heading', { name: 'Video' })) & Node.DOCUMENT_POSITION_PRECEDING).toBeTruthy();
    }
    expect(screen.queryByText(/Later words/)).toBeNull();
    expect(screen.queryByRole('heading', { name: 'Announce' })).toBeNull();
  });
});

describe('post cards: both networks (#0581)', () => {
  const two = [
    { name: 'bluesky', label: 'Bluesky', utm_source: 'bluesky', utm_medium: 'social', char_limit: 300, automatic: true, configured: true, account: '@club.test' },
    { name: 'mastodon', label: 'Mastodon', utm_source: 'mastodon', utm_medium: 'social', char_limit: 500, automatic: true, configured: true, account: 'social.test' },
  ];
  function setupTwo(): void {
    mountView(viewOf({
      channels: two, waves: [waves[0]],
      posts: [post({ id: 1, body: 'Bluesky words' }), post({ id: 2, channel: 'mastodon', body: 'Mastodon words' })],
    }));
  }

  it('posts both from one dialog and reports both on one line', async () => {
    setupTwo();
    await fireEvent.click(await screen.findByRole('button', { name: 'Post to Bluesky and Mastodon' }));
    const dlg = screen.getByRole('dialog');
    expect(dlg.textContent).toContain('Bluesky words');
    expect(dlg.textContent).toContain('Mastodon words');
    expect(dlg.textContent).toContain('@club.test');
    expect(dlg.textContent).toContain('social.test');
    sendOutreachPost.mockImplementation((id: number) =>
      id === 1 ? Promise.resolve(post({ id: 1, status: 'queued' })) : Promise.reject(new Error('boom')),
    );
    await fireEvent.click(within(dlg).getByRole('button', { name: 'Post now' }));
    await waitFor(() => expect(sendOutreachPost).toHaveBeenCalledTimes(2));
    expect(sendOutreachPost.mock.calls.map((c) => c[0])).toEqual([1, 2]);
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    const line = screen.getByText('Bluesky: queued · Mastodon: could not post (please try again)');
    // C5: the result sits under the Announce heading, before the first card, not after the last.
    const firstCard = screen.getByRole('article', { name: 'Bluesky Announce' });
    expect(line.compareDocumentPosition(firstCard) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    // Each card also carries its own result.
    expect(screen.getByRole('article', { name: 'Bluesky Announce' }).querySelector('.notice')?.textContent).toMatch(/^Queued\./);
    expect(screen.getByRole('article', { name: 'Mastodon Announce' }).querySelector('.notice')?.textContent).toBe('Could not post: please try again');
  });

  it('clears the both-networks line once the posts it reported leave queued (#0582)', async () => {
    let current = viewOf({
      channels: two, waves: [waves[0]],
      posts: [post({ id: 1, body: 'Bluesky words' }), post({ id: 2, channel: 'mastodon', body: 'Mastodon words' })],
    });
    const r = render(OutreachPostCards, {
      view: current,
      results: null,
      reload: vi.fn(),
      onChange: (p: OutreachPost) => {
        current = { ...current, posts: current.posts.map((x) => (x.id === p.id ? p : x)) };
        void r.rerender({ view: current });
      },
    });
    await fireEvent.click(await screen.findByRole('button', { name: 'Post to Bluesky and Mastodon' }));
    sendOutreachPost.mockImplementation((id: number) =>
      Promise.resolve(id === 1 ? post({ id: 1, status: 'queued', body: 'Bluesky words' }) : post({ id: 2, channel: 'mastodon', status: 'queued', body: 'Mastodon words' })),
    );
    await fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Post now' }));
    expect(await screen.findByText('Bluesky: queued · Mastodon: queued')).toBeTruthy();
    // Polling brings both back posted: the "queued" line must not outlive them.
    await r.rerender({ view: { ...current, posts: current.posts.map((p) => ({ ...p, status: 'posted' as const })) } });
    expect(screen.queryByText('Bluesky: queued · Mastodon: queued')).toBeNull();
  });

  it('is not offered when only one network can post', async () => {
    setup([post({ id: 1 })]);
    await screen.findByText('Not scheduled');
    expect(screen.queryByRole('button', { name: /^Post to / })).toBeNull();
  });
});

describe('post cards: channelFilter (#0582, Post by hand)', () => {
  it('lists only the matching channels, with no Posts heading or both-networks button', async () => {
    const two = [
      { name: 'bluesky', label: 'Bluesky', utm_source: 'bluesky', utm_medium: 'social', char_limit: 300, automatic: true, configured: true, account: '@club.test' },
      { name: 'mastodon', label: 'Mastodon', utm_source: 'mastodon', utm_medium: 'social', char_limit: 500, automatic: true, configured: true, account: 'social.test' },
      channels[1],
    ];
    const v = viewOf({
      channels: two,
      waves: [waves[0]],
      posts: [post({ id: 1 }), post({ id: 2, channel: 'mastodon' }), post({ id: 3, channel: 'reddit', status: 'manual', body: 'Reddit body' })],
    });
    render(OutreachPostCards, { view: v, results: null, reload: vi.fn(), onChange: vi.fn(), channelFilter: (c: { automatic: boolean }) => !c.automatic });
    await screen.findByText('Reddit body', { selector: 'pre.body' });
    expect(screen.queryByRole('article', { name: 'Bluesky Announce' })).toBeNull();
    expect(screen.queryByRole('article', { name: 'Mastodon Announce' })).toBeNull();
    expect(screen.getByRole('article', { name: 'Reddit Announce' })).toBeTruthy();
    expect(screen.queryByRole('heading', { name: 'Posts' })).toBeNull();
    expect(screen.queryByRole('button', { name: /^Post to / })).toBeNull();
  });
});

describe('post cards: manual channels (#0588)', () => {
  const chans = [
    ...channels,
    { name: 'instagram', label: 'Instagram', utm_source: 'instagram', utm_medium: 'social', char_limit: 0, automatic: false, configured: false, account: '' },
    { name: 'tiktok', label: 'TikTok', utm_source: 'tiktok', utm_medium: 'video', char_limit: 0, automatic: false, configured: false, account: '' },
  ];
  const mount = (posts: OutreachPost[], flyer?: { href: string; label: string }) =>
    mountView(viewOf({ channels: chans, waves: [waves[0]], posts }), flyer);

  it('a hand-posted card offers Unmark, confirmed in a dialog, and goes back to a post to make by hand', async () => {
    const posted = post({ id: 2, channel: 'instagram', status: 'posted', body: 'IG body', posted_at: '2026-10-18T17:00:00Z', remote_url: 'https://instagram.com/p/x' });
    unmarkOutreachPosted.mockResolvedValue({ ...posted, status: 'manual', posted_at: null, remote_url: null });
    mount([posted]);
    await screen.findByText(/IG body/, { selector: 'pre.body' });
    const card = screen.getByRole('article', { name: 'Instagram Reels Announce' });
    expect(within(card).queryByRole('button', { name: 'Edit' })).toBeNull();
    await fireEvent.click(within(card).getByRole('button', { name: 'Unmark' }));
    const dlg = screen.getByRole('dialog');
    expect(dlg.textContent).toContain('Unmark the Instagram post?');
    expect(unmarkOutreachPosted).not.toHaveBeenCalled();
    await fireEvent.click(within(dlg).getByRole('button', { name: 'Unmark' }));
    await waitFor(() => expect(unmarkOutreachPosted).toHaveBeenCalledWith(2));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(within(card).getByRole('button', { name: 'Mark posted' })).toBeTruthy();
    expect(within(card).getByRole('button', { name: 'Edit' })).toBeTruthy();
    expect(within(card).getByRole('status').textContent).toBe('Unmarked. It is a post to make by hand again.');
  });

  it('Cancel leaves the post posted; a site-sent post has no Unmark', async () => {
    const posted = post({ id: 2, channel: 'instagram', status: 'posted', body: 'IG body', posted_at: '2026-10-18T17:00:00Z' });
    mount([posted, post({ id: 1, status: 'posted', posted_at: '2026-10-18T17:00:00Z', body: 'Bsky body' })]);
    await screen.findByText(/IG body/, { selector: 'pre.body' });
    expect(within(screen.getByRole('article', { name: 'Bluesky Announce' })).queryByRole('button', { name: 'Unmark' })).toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: 'Unmark' }));
    await fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(unmarkOutreachPosted).not.toHaveBeenCalled();
  });

  it('shows an Unmark failure in the dialog and keeps it open', async () => {
    const posted = post({ id: 2, channel: 'instagram', status: 'posted', body: 'IG body', posted_at: '2026-10-18T17:00:00Z' });
    unmarkOutreachPosted.mockRejectedValue(new Error('x'));
    mount([posted]);
    await screen.findByText(/IG body/, { selector: 'pre.body' });
    await fireEvent.click(screen.getByRole('button', { name: 'Unmark' }));
    await fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Unmark' }));
    await waitFor(() => expect(within(screen.getByRole('dialog')).getByRole('alert').textContent).toContain('Could not unmark'));
  });

  it('every video row shows its link; only Instagram says bio', async () => {
    mount([
      post({ id: 3, channel: 'tiktok', status: 'manual', body: 'Sign up: https://go.example/abc' }),
      post({ id: 2, channel: 'instagram', status: 'manual', body: 'Link in bio.' }),
    ]);
    await screen.findByText(/Link in bio\./, { selector: 'pre.body' });
    const ig = screen.getByRole('article', { name: 'Instagram Reels Announce' });
    expect(ig.textContent).toContain('Put this in your bio');
    const tt = screen.getByRole('article', { name: 'TikTok Announce' });
    expect(tt.textContent).not.toContain('Put this in your bio');
    expect(tt.textContent).toContain('ShortLink');
    expect(within(tt).getByRole('button', { name: 'Copy link' })).toBeTruthy();
    expect(within(tt).getByRole('button', { name: 'Copy with link' })).toBeTruthy();
  });

  it('labels the link row "Link to paste", not the bio, on a hand-posted Bluesky card', async () => {
    const offline = [{ ...channels[0], configured: false }];
    mountView(viewOf({ channels: offline, waves: [waves[0]], posts: [post({ id: 4, channel: 'bluesky', status: 'draft', body: 'Hand Bluesky.' })] }));
    const bs = await screen.findByRole('article', { name: 'Bluesky Announce' });
    expect(bs.textContent).toContain('Link to paste');
    expect(bs.textContent).not.toContain('Put this in your bio');
  });

  it('the Instagram flyer link goes where the page says (F2)', async () => {
    mount([post({ id: 2, channel: 'instagram', status: 'manual', body: 'Link in bio.' })], { href: '#/outreach?view=flyers&flyer=7', label: 'Open flyer' });
    await screen.findByText(/Link in bio\./, { selector: 'pre.body' });
    const link = screen.getByRole('article', { name: 'Instagram Reels Announce' }).querySelector('a.open-flyer');
    expect(link?.getAttribute('href')).toBe('#/outreach?view=flyers&flyer=7');
    cleanup();
    mount([post({ id: 2, channel: 'instagram', status: 'manual', body: 'Link in bio.' })], { href: '#/outreach?view=flyers&workshop=5', label: 'Make a flyer' });
    await screen.findByText(/Link in bio\./, { selector: 'pre.body' });
    const make = screen.getByRole('article', { name: 'Instagram Reels Announce' }).querySelector('a.open-flyer');
    expect(make?.textContent).toBe('Make a flyer');
    expect(make?.getAttribute('href')).toBe('#/outreach?view=flyers&workshop=5');
  });
});

describe('post cards: Delete and redo (#0604)', () => {
  const posted = () =>
    post({ id: 3, status: 'posted', body: 'Old body https://go.example/abc', posted_at: '2026-10-18T17:00:00Z', remote_url: 'https://bsky.app/profile/x/post/abc' });
  const mount = (posts: OutreachPost[]) => mountView(viewOf({ posts }));

  it('a site-sent posted card offers Delete and redo, confirmed in a dialog that says what happens', async () => {
    const draft = post({ id: 3, status: 'draft', body: 'New body https://go.example/u/abc', posted_at: null, remote_url: null });
    takedownOutreachPost.mockResolvedValue(draft);
    mount([posted()]);
    await screen.findByText(/Old body/);
    const card = screen.getByRole('article', { name: 'Bluesky Announce' });
    await fireEvent.click(within(card).getByRole('button', { name: 'Delete and redo…' }));
    const dlg = screen.getByRole('dialog');
    expect(dlg.textContent).toContain('Delete this post from Bluesky?');
    expect(dlg.textContent).toContain('It will be removed for everyone, and the post goes back to draft so you can post it again.');
    expect(takedownOutreachPost).not.toHaveBeenCalled();
    await fireEvent.click(within(dlg).getByRole('button', { name: 'Delete post' }));
    await waitFor(() => expect(takedownOutreachPost).toHaveBeenCalledWith(3));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    // The card is the draft with the corrected link and the usual actions.
    expect(await screen.findByText(/New body/)).toBeTruthy();
    expect(within(card).getByRole('button', { name: 'Post now' })).toBeTruthy();
    expect(within(card).getByRole('button', { name: 'Schedule…' })).toBeTruthy();
    expect(within(card).queryByRole('button', { name: 'Delete and redo…' })).toBeNull();
    expect(within(card).getByRole('status').textContent).toBe('Deleted from Bluesky. It is a draft again, with the current link.');
    // The page refetches, so Reset reflects that nothing is posted any more.
    expect(reloadMock).toHaveBeenCalled();
  });

  it('Cancel deletes nothing', async () => {
    mount([posted()]);
    await screen.findByText(/Old body/);
    await fireEvent.click(screen.getByRole('button', { name: 'Delete and redo…' }));
    await fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(takedownOutreachPost).not.toHaveBeenCalled();
  });

  it('shows the failure in the dialog, keeps it open and leaves the card posted', async () => {
    takedownOutreachPost.mockRejectedValue(new Error('x'));
    mount([posted()]);
    await screen.findByText(/Old body/);
    await fireEvent.click(screen.getByRole('button', { name: 'Delete and redo…' }));
    await fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Delete post' }));
    await waitFor(() => expect(within(screen.getByRole('dialog')).getByRole('alert').textContent).toContain('Could not delete the post. Please try again.'));
    expect(screen.getByRole('dialog')).toBeTruthy();
    expect(reloadMock).not.toHaveBeenCalled();
    expect(within(screen.getByRole('article', { name: 'Bluesky Announce' })).getByRole('button', { name: 'Delete and redo…' })).toBeTruthy();
  });

  it('a draft or a hand-posted card has no Delete and redo', async () => {
    const chans = [...channels, { name: 'instagram', label: 'Instagram', utm_source: 'instagram', utm_medium: 'social', char_limit: 0, automatic: false, configured: false, account: '' }];
    mountView(viewOf({ channels: chans, waves: [waves[0]], posts: [post({ id: 1 }), post({ id: 2, channel: 'instagram', status: 'posted', body: 'IG body', posted_at: '2026-10-18T17:00:00Z' })] }));
    await screen.findByText(/IG body/, { selector: 'pre.body' });
    expect(screen.queryByRole('button', { name: 'Delete and redo…' })).toBeNull();
  });
});

describe('hand-posted copy (#0626)', () => {
  const hand = (name: string, label: string) => ({ name, label, utm_source: name, utm_medium: 'video', char_limit: 0, automatic: false, configured: false, account: '' });
  const video = [hand('instagram', 'Instagram'), hand('tiktok', 'TikTok'), hand('youtube', 'YouTube Shorts')];
  const tiktokBody = 'Soldering: Saturday.\n\nLearn to solder.\n\nSign up: https://go.example/u/tt\n\nOpen Circuit SF: https://www.opencircuitsf.com\n\n#sf';
  const tt = (over: Partial<OutreachPost> = {}) => post({ id: 3, channel: 'tiktok', status: 'manual', body: tiktokBody, short_url: 'https://go.example/u/tt', ...over });
  const onlyVideo = (posts: OutreachPost[], extra: Record<string, unknown> = {}, viewOver: Partial<WorkshopOutreach> = {}) =>
    mountView(viewOf({ channels: video, waves: [waves[0]], posts, ...viewOver }), undefined, { wave: 'announce', ...extra });
  const row = () => screen.getByRole('article', { name: 'TikTok Announce' });
  const btn = (name: string) => within(row()).getByRole('button', { name }) as HTMLButtonElement;
  const box = () => row().querySelector('.manual-copy textarea') as HTMLTextAreaElement | null;
  const stubCopy = () => {
    const writeText = vi.fn(() => Promise.resolve());
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    return writeText;
  };

  it('copying each of the three in one section gives three different links and no token', async () => {
    const urls: Record<string, string> = { instagram: 'https://go.example/u/ig', tiktok: 'https://go.example/u/tt', youtube: 'https://go.example/u/yt' };
    onlyVideo([
      post({ id: 2, channel: 'instagram', status: 'manual', body: 'A.\n\nSign up at the link in our bio.\n\n#sf', short_url: urls.instagram }),
      tt({ body: 'A.\n\nSign up: {link}', short_url: urls.tiktok }),
      post({ id: 4, channel: 'youtube', status: 'manual', body: `A.\n\nSign up: ${urls.youtube}`, short_url: urls.youtube }),
    ]);
    await screen.findByRole('heading', { name: 'Video' });
    const writeText = stubCopy();
    for (const b of screen.getAllByRole('button', { name: 'Copy with link' })) await fireEvent.click(b);
    await waitFor(() => expect(writeText).toHaveBeenCalledTimes(3));
    const copied = writeText.mock.calls.map((c) => (c as unknown as string[])[0]);
    for (const u of Object.values(urls)) expect(copied.filter((t) => t.includes(u)).length).toBe(1);
    for (const t of copied) expect(t).not.toContain('{link}');
  });

  it('Copy text only leaves out the link and the sign-up line', async () => {
    onlyVideo([tt()]);
    await screen.findByRole('heading', { name: 'Video' });
    const writeText = stubCopy();
    await fireEvent.click(btn('Copy text only'));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('Soldering: Saturday.\n\nLearn to solder.\n\n#sf'));
    expect(await within(row()).findByRole('button', { name: 'Copied' })).toBeTruthy();
    expect(within(row()).getByRole('status').textContent).toBe('Copied without the link.');
  });

  it('shows the text that will be copied and the ShortLink before copying', async () => {
    onlyVideo([tt()]);
    await screen.findByRole('heading', { name: 'Video' });
    expect(row().querySelector('.hand-text pre')?.textContent).toBe(tiktokBody);
    expect(row().querySelector('.link-row .short')?.textContent).toBe('https://go.example/u/tt');
    expect(row().querySelector('.text-only-body')?.textContent).toBe('Soldering: Saturday.\n\nLearn to solder.\n\n#sf');
    const scroller = row().querySelector('.hand-text') as HTMLElement;
    expect(scroller.getAttribute('tabindex')).toBe('0');
  });

  it('a post with no link says so, turns off Copy with link, and Copy text only still works', async () => {
    onlyVideo([tt({ short_url: null, prepared: false, body: 'Hello\n\nSign up: {link}' })]);
    await screen.findByRole('heading', { name: 'Video' });
    expect(row().textContent).toContain('This post has no ShortLink yet');
    expect(btn('Copy with link').disabled).toBe(true);
    expect(within(row()).queryByRole('button', { name: 'Mark posted' })).toBeNull();
    const writeText = stubCopy();
    await fireEvent.click(btn('Copy text only'));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('Hello'));
  });

  it('Create link asks for the link when the post has none', async () => {
    const before = tt({ short_url: null, prepared: false, body: 'Hello\n\nSign up: {link}' });
    prepareOutreachPost.mockResolvedValue(tt({ body: 'Hello\n\nSign up: https://go.example/u/tt' }));
    onlyVideo([before]);
    await fireEvent.click(await screen.findByRole('button', { name: 'Create link' }));
    await waitFor(() => expect(prepareOutreachPost).toHaveBeenCalledWith(3));
    await waitFor(() => expect(btn('Copy with link').disabled).toBe(false));
    expect(row().querySelector('.link-row .short')?.textContent).toBe('https://go.example/u/tt');
  });

  it('no Create link when ShortLinks is unavailable', async () => {
    onlyVideo([tt({ short_url: null, prepared: false })], {}, { available: false });
    await screen.findByRole('heading', { name: 'Video' });
    expect(row().textContent).toContain('This post has no ShortLink yet');
    expect(within(row()).queryByRole('button', { name: 'Create link' })).toBeNull();
  });

  it('a post that is only a link turns off Copy text only and says why', async () => {
    onlyVideo([tt({ body: 'https://go.example/u/tt' })]);
    await screen.findByRole('heading', { name: 'Video' });
    expect(btn('Copy text only').disabled).toBe(true);
    expect(row().textContent).toContain('Nothing is left of this post without its link');
    expect(row().querySelector('details.text-only')).toBeNull();
  });

  async function expectManualBox(expected: string): Promise<void> {
    await waitFor(() => expect(box()?.value).toBe(expected));
    expect(within(row()).getByRole('alert').textContent).toContain('Your browser did not allow copying');
    expect(within(row()).queryByRole('button', { name: 'Copied' })).toBeNull();
    expect(row().querySelector('.notice')?.textContent).toBe('');
    await waitFor(() => expect(document.activeElement).toBe(box()));
    expect(box()?.selectionStart).toBe(0);
    expect(box()?.selectionEnd).toBe(expected.length);
  }

  it('when the browser refuses: an alert, the exact text in a box, and never Copied', async () => {
    onlyVideo([tt()]);
    await screen.findByRole('heading', { name: 'Video' });
    vi.stubGlobal('navigator', { clipboard: { writeText: vi.fn(() => Promise.reject(new Error('denied'))) } });
    await fireEvent.click(btn('Copy with link'));
    await expectManualBox(tiktokBody);
    await fireEvent.click(btn('Done'));
    expect(box()).toBeNull();
    expect(within(row()).getByRole('alert').textContent).toBe('');
  });

  it('when there is no clipboard at all: the same', async () => {
    onlyVideo([tt()]);
    await screen.findByRole('heading', { name: 'Video' });
    vi.stubGlobal('navigator', {});
    await fireEvent.click(btn('Copy text only'));
    await expectManualBox('Soldering: Saturday.\n\nLearn to solder.\n\n#sf');
  });

  it('a later copy that works closes the box', async () => {
    onlyVideo([tt()]);
    await screen.findByRole('heading', { name: 'Video' });
    vi.stubGlobal('navigator', { clipboard: { writeText: vi.fn(() => Promise.reject(new Error('denied'))) } });
    await fireEvent.click(btn('Copy with link'));
    await waitFor(() => expect(box()).not.toBeNull());
    stubCopy();
    await fireEvent.click(btn('Copy with link'));
    await waitFor(() => expect(box()).toBeNull());
    expect(within(row()).getByRole('alert').textContent).toBe('');
    expect(await within(row()).findByRole('button', { name: 'Copied' })).toBeTruthy();
  });

  it('the box closes when the post changes under it', async () => {
    const v = viewOf({ channels: video, waves: [waves[0]], posts: [tt()] });
    const r = render(OutreachPostCards, { view: v, results: null, reload: reloadMock, onChange: vi.fn(), wave: 'announce' });
    await screen.findByRole('heading', { name: 'Video' });
    vi.stubGlobal('navigator', {});
    await fireEvent.click(btn('Copy with link'));
    await waitFor(() => expect(box()).not.toBeNull());
    await r.rerender({ view: { ...v, posts: [tt({ body: 'Changed\n\nSign up: https://go.example/u/tt' })] } });
    await waitFor(() => expect(box()).toBeNull());
    expect(within(row()).getByRole('alert').textContent).toBe('');
  });

  it('Mark posted removes both copies and keeps Copy link; Unmark brings them back', async () => {
    const posted = tt({ status: 'posted', posted_at: '2026-10-18T17:00:00Z' });
    markOutreachPosted.mockResolvedValue(posted);
    unmarkOutreachPosted.mockResolvedValue(tt());
    onlyVideo([tt()]);
    await screen.findByRole('heading', { name: 'Video' });
    await fireEvent.click(btn('Mark posted'));
    await fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Mark posted' }));
    await waitFor(() => expect(within(row()).queryByRole('button', { name: 'Copy with link' })).toBeNull());
    expect(within(row()).queryByRole('button', { name: 'Copy text only' })).toBeNull();
    expect(row().querySelector('details.text-only')).toBeNull();
    expect(btn('Copy link')).toBeTruthy();
    await fireEvent.click(btn('Unmark'));
    await fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Unmark' }));
    await waitFor(() => expect(btn('Copy with link')).toBeTruthy());
    expect(btn('Copy text only')).toBeTruthy();
  });

  it('an editor with changes reports unsaved, and Save clears it', async () => {
    const onUnsavedChange = vi.fn();
    updateOutreachPost.mockResolvedValue(tt({ body: 'New words' }));
    onlyVideo([tt()], { onUnsavedChange });
    await screen.findByRole('heading', { name: 'Video' });
    await fireEvent.click(btn('Edit'));
    await fireEvent.input(screen.getByLabelText('Post text'), { target: { value: 'New words' } });
    await waitFor(() => expect(onUnsavedChange.mock.lastCall?.[0]).toBe(true));
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(onUnsavedChange.mock.lastCall?.[0]).toBe(false));
  });

  it('an unconnected Bluesky gets its row under Post by hand, not Video', async () => {
    const offline = { ...channels[0], configured: false };
    mountView(
      viewOf({ channels: [offline, video[1]], waves: [waves[0]], posts: [post({ id: 1, body: 'Hand Bluesky.' }), tt()] }),
      undefined,
      { wave: 'announce' },
    );
    const byHand = await screen.findByRole('heading', { name: 'Post by hand' });
    const videoHead = screen.getByRole('heading', { name: 'Video' });
    const bs = screen.getByRole('article', { name: 'Bluesky Announce' });
    expect(byHand.compareDocumentPosition(bs) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(videoHead.compareDocumentPosition(row()) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(row().compareDocumentPosition(byHand) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(bs.querySelector('.hand-text')).toBeNull();
  });
});

describe('editor layout (#0627)', () => {
  const AT = '2026-12-01T20:00:00Z';
  const card = () => screen.getByRole('article', { name: 'Bluesky Announce' });
  const dateInput = () => card().querySelector('input[type="datetime-local"]') as HTMLInputElement | null;
  const toggle = (name = 'Schedule…') => within(card()).getByRole('button', { name }) as HTMLButtonElement;
  const openEditor = async (over: Partial<OutreachPost> = {}) => {
    setup([post({ id: 1, ...over })]);
    await fireEvent.click(await screen.findByRole('button', { name: 'Edit' }));
  };
  /** Opens the picker and types a date, leaving the Schedule submit button in reach. */
  const pickDate = async () => {
    await fireEvent.click(toggle());
    await fireEvent.input(dateInput()!, { target: { value: '2099-11-28T10:00' } });
    return within(card()).getByRole('button', { name: 'Schedule' }) as HTMLButtonElement;
  };

  it('Edit opens with the picker closed, and the toggle opens and closes it keeping the typed date', async () => {
    await openEditor();
    expect(dateInput()).toBeNull();
    expect(toggle().getAttribute('aria-expanded')).toBe('false');
    await fireEvent.click(toggle());
    expect(within(card()).getByLabelText('Schedule for (Pacific time)')).toBe(dateInput());
    expect(toggle().getAttribute('aria-expanded')).toBe('true');
    await fireEvent.input(dateInput()!, { target: { value: '2099-11-28T10:00' } });
    await fireEvent.click(toggle());
    expect(dateInput()).toBeNull();
    await fireEvent.click(toggle());
    expect(dateInput()?.value).toBe('2099-11-28T10:00');
  });

  it('Schedule… on the card opens the editor with the picker already open', async () => {
    setup([post({ id: 1 })]);
    await fireEvent.click(await screen.findByRole('button', { name: 'Schedule…' }));
    expect(dateInput()).toBeTruthy();
    expect(toggle().getAttribute('aria-expanded')).toBe('true');
  });

  it('Schedule and Unschedule share one row with Save, Cancel and Delete', async () => {
    await openEditor({ status: 'scheduled', scheduled_at: AT });
    const row = card().querySelector('.row.actions');
    expect(Array.from(row!.querySelectorAll('button')).map((b) => b.textContent?.trim())).toEqual(['Save', 'Cancel', 'Change time…', 'Unschedule', 'Delete']);
    expect(card().querySelector('.schedule')).toBeNull();
  });

  it('a scheduled post states its time once', async () => {
    setup([post({ id: 1, status: 'scheduled', scheduled_at: AT })]);
    await screen.findByRole('article', { name: 'Bluesky Announce' });
    expect(card().textContent?.match(/Posts automatically/g)?.length).toBe(1);
    await fireEvent.click(within(card()).getByRole('button', { name: 'Edit' }));
    expect(card().textContent?.match(/Scheduled for/g)?.length).toBe(1);
    expect(dateInput()).toBeNull();
  });

  it('Schedule sends the picked time, closes the picker and returns focus to the toggle', async () => {
    const iso = fromLocalInputValue('2099-11-28T10:00');
    scheduleOutreachPost.mockResolvedValue(post({ id: 1, status: 'scheduled', scheduled_at: iso }));
    await openEditor();
    await fireEvent.click(await pickDate());
    await waitFor(() => expect(scheduleOutreachPost).toHaveBeenCalledWith(1, iso));
    await waitFor(() => expect(dateInput()).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(toggle('Change time…')));
    expect(within(card()).getByText('Scheduled.')).toBeTruthy();
  });

  it('a failed Schedule keeps the picker open and shows the error', async () => {
    scheduleOutreachPost.mockRejectedValue(new Error('x'));
    await openEditor();
    await fireEvent.click(await pickDate());
    expect(await within(card()).findByText('Could not schedule. Please try again.')).toBeTruthy();
    expect(dateInput()).toBeTruthy();
  });

  it('Schedule stays off while the text has unsaved edits', async () => {
    await openEditor();
    const submit = await pickDate();
    await fireEvent.input(screen.getByLabelText('Post text'), { target: { value: 'Edited' } });
    expect(submit.disabled).toBe(true);
    expect(within(card()).getByText('Save your edits before creating the link or scheduling.')).toBeTruthy();
  });

  it('Unschedule goes back to draft and leaves the editor open', async () => {
    unscheduleOutreachPost.mockResolvedValue(post({ id: 1 }));
    await openEditor({ status: 'scheduled', scheduled_at: AT });
    await fireEvent.click(within(card()).getByRole('button', { name: 'Unschedule' }));
    await waitFor(() => expect(unscheduleOutreachPost).toHaveBeenCalledWith(1));
    expect(await within(card()).findByText('Back to draft.')).toBeTruthy();
    expect(toggle()).toBeTruthy();
    expect(within(card()).getByLabelText('Post text')).toBeTruthy();
  });

  it('Delete in the editor deletes the post and closes the editor', async () => {
    deleteOutreachPost.mockResolvedValue(undefined);
    await openEditor();
    await fireEvent.click(within(card()).getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(deleteOutreachPost).toHaveBeenCalledWith(1));
    await waitFor(() => expect(reloadMock).toHaveBeenCalled());
    await waitFor(() => expect(card().querySelector('textarea')).toBeNull());
  });
});

describe('OutreachPostCards with a switched-off channel', () => {
  it('shows an off channel only where it has a post (#0628)', () => {
    mountView(viewOf({
      posts: [post({ id: 2, channel: 'reddit', status: 'posted', posted_at: '2026-11-20T18:00:00Z' })],
      off_channels: [{ name: 'reddit', label: 'Reddit', automatic: false }],
    }));
    expect(screen.getByRole('article', { name: 'Reddit Announce' })).toBeTruthy();
    expect(screen.queryByRole('article', { name: 'Reddit Reminder' })).toBeNull();
    expect(screen.getByRole('article', { name: 'Bluesky Reminder' })).toBeTruthy();
  });
});

describe('when to post (#0635)', () => {
  const tiktok = { name: 'tiktok', label: 'TikTok', utm_source: 'tiktok', utm_medium: 'video', char_limit: 0, automatic: false, configured: false, account: '' };
  const offBluesky = { ...channels[0], configured: false, char_limit: 300 };
  const WAVES = [{ name: 'reminder', label: 'Reminder', suggested_at: '2099-11-28T18:00:00Z' }];
  const handRow = (over: Partial<OutreachPost> = {}) => post({ id: 7, channel: 'tiktok', wave: 'reminder', body: 'Coming up: T.', ...over });

  it('a hand row says when to post', async () => {
    mountView(viewOf({ channels: [tiktok], waves: WAVES, posts: [handRow()] }));
    const card = await screen.findByRole('article', { name: 'TikTok Reminder' });
    expect(card.querySelector('.card-head .status')!.textContent).toMatch(/Aim to post on .*Nov 28\./);
  });
  it('a posted hand row says when it was posted, not when to post', async () => {
    mountView(viewOf({ channels: [tiktok], waves: WAVES, posts: [handRow({ status: 'posted', posted_at: '2026-10-17T20:00:00Z' })] }));
    const card = await screen.findByRole('article', { name: 'TikTok Reminder' });
    const text = card.querySelector('.card-head .status')!.textContent!;
    expect(text).toMatch(/Posted/);
    expect(text).not.toMatch(/Aim/);
  });
  it('an automatic card keeps its status line where it was', async () => {
    setup([post({ id: 1 })]);
    const card = await screen.findByRole('article', { name: 'Bluesky Announce' });
    expect(card.querySelector('.card-head .status')).toBeNull();
    expect(card.querySelector(':scope > .status')).toBeTruthy();
  });
  it('a video row shows no character count', async () => {
    mountView(viewOf({ channels: [tiktok], waves: WAVES, posts: [handRow()] }));
    const card = await screen.findByRole('article', { name: 'TikTok Reminder' });
    expect(card.textContent).not.toMatch(/characters/);
  });
  it('an unconnected Bluesky keeps its count and gets the line', async () => {
    mountView(viewOf({ channels: [offBluesky], waves: WAVES, posts: [post({ id: 8, wave: 'reminder' })] }));
    const card = await screen.findByRole('article', { name: 'Bluesky Reminder' });
    expect(card.querySelector('.card-head .status')!.textContent).toMatch(/Aim to post on/);
    expect(card.textContent).toMatch(/characters/);
  });
});

describe('standard wording (#0635)', () => {
  const tiktok = { name: 'tiktok', label: 'TikTok', utm_source: 'tiktok', utm_medium: 'video', char_limit: 0, automatic: false, configured: false, account: '' };
  const FRESH = 'Coming up: T.\n\nSign up: https://go.example/abc';
  const handRow = (over: Partial<OutreachPost> = {}) => post({ id: 7, channel: 'tiktok', body: 'mine', fresh_body: FRESH, ...over });
  const USE = { name: 'Use the standard wording' };
  const open = async (posts: OutreachPost[], chans = [tiktok]) => {
    mountView(viewOf({ channels: chans, posts }));
    await fireEvent.click(await screen.findByRole('button', { name: 'Edit' }));
    return screen.getByLabelText('Post text') as HTMLTextAreaElement;
  };

  it('an edited hand post offers the standard wording in its editor', async () => {
    await open([handRow()]);
    expect(screen.getByRole('button', USE)).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Regenerate' })).toBeNull();
  });
  it('Use the standard wording fills the box and saves nothing', async () => {
    const box = await open([handRow()]);
    await fireEvent.click(screen.getByRole('button', USE));
    expect(box.value).toBe(FRESH);
    expect(updateOutreachPost).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', USE)).toBeNull();
  });
  it('Cancel after it keeps the saved text', async () => {
    await open([handRow()]);
    await fireEvent.click(screen.getByRole('button', USE));
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    await fireEvent.click(await screen.findByRole('button', { name: 'Discard changes' }));
    const card = await screen.findByRole('article', { name: 'TikTok Announce' });
    await waitFor(() => expect(card.querySelector('pre.body')?.textContent).toContain('mine'));
    expect(updateOutreachPost).not.toHaveBeenCalled();
  });
  it('Save after it sends regenerate, not the text', async () => {
    updateOutreachPost.mockResolvedValue(handRow({ body: FRESH }));
    await open([handRow()]);
    await fireEvent.click(screen.getByRole('button', USE));
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(updateOutreachPost).toHaveBeenCalledWith(7, { regenerate: true }));
  });
  it('changing the filled text and saving sends the text', async () => {
    updateOutreachPost.mockResolvedValue(handRow({ body: 'edited' }));
    const box = await open([handRow()]);
    await fireEvent.click(screen.getByRole('button', USE));
    await fireEvent.input(box, { target: { value: 'edited' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(updateOutreachPost).toHaveBeenCalledWith(7, { body: 'edited' }));
  });
  it('no offer when the text already matches', async () => {
    await open([handRow({ body: FRESH })]);
    expect(screen.queryByRole('button', USE)).toBeNull();
  });
  it('an automatic post has no offer and keeps Regenerate', async () => {
    await open([post({ id: 1, stale: true })], [channels[0]]);
    expect(screen.queryByRole('button', USE)).toBeNull();
    expect(screen.getByRole('button', { name: 'Regenerate' })).toBeTruthy();
  });
  it('a stale hand post offers the standard wording and does not save at once', async () => {
    await open([handRow({ stale: true })]);
    await fireEvent.click(screen.getByRole('button', USE));
    expect(updateOutreachPost).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: 'Regenerate' })).toBeNull();
  });
  it('a stale hand post whose text already matches keeps Regenerate', async () => {
    await open([handRow({ stale: true, body: FRESH })]);
    expect(screen.queryByRole('button', USE)).toBeNull();
    expect(screen.getByRole('button', { name: 'Regenerate' })).toBeTruthy();
  });
});
