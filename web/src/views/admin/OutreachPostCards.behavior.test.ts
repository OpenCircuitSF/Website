// @vitest-environment jsdom
//
// #0569, #0582: mounts OutreachPostCards.svelte (real OutreachPostCard,
// OutreachComposer, dialogs) under jsdom with '../../lib/api' mocked. The list
// owns no data, so the test plays the panel's part: onChange swaps the changed
// post into the view and re-renders. jsdom is not a browser (AGENTS.md §1): no
// browser was run for this change.
import { render, cleanup, waitFor, screen, fireEvent, within } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { OutreachPost, WorkshopOutreach } from '../../lib/types';

const sendOutreachPost = vi.fn();
const updateOutreachPost = vi.fn();
const unmarkOutreachPosted = vi.fn();
const takedownOutreachPost = vi.fn();
const markOutreachPosted = vi.fn();
const prepareOutreachPost = vi.fn();
const deleteOutreachPost = vi.fn();
const reloadMock = vi.fn();
vi.mock('../../lib/api', () => ({
  draftAllOutreach: vi.fn(),
  createOutreachPost: vi.fn(),
  sendOutreachPost: (id: number) => sendOutreachPost(id),
  markOutreachPosted: (id: number, url?: string) => markOutreachPosted(id, url),
  unmarkOutreachPosted: (id: number) => unmarkOutreachPosted(id),
  takedownOutreachPost: (id: number) => takedownOutreachPost(id),
  updateOutreachPost: (id: number, b: unknown) => updateOutreachPost(id, b),
  prepareOutreachPost: (id: number) => prepareOutreachPost(id),
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
    wave: 'announce',
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
  const ig = { name: 'instagram', label: 'Instagram', utm_source: 'instagram', utm_medium: 'social', char_limit: 0, automatic: false, configured: false, account: '' };
  const hand = (over: Partial<OutreachPost>) => post({ channel: 'reddit', status: 'manual', body: 'Reddit body', ...over });

  it('a posted card keeps its preview and Copy link to post writes the remote URL (#0583)', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    setup([hand({ id: 1, status: 'posted', posted_at: '2026-10-17T20:00:00Z', remote_url: 'https://reddit.example/p/1' })]);
    const card = await screen.findByRole('article', { name: 'Reddit Announce' });
    await fireEvent.click(within(card).getByRole('button', { name: 'Copy link to post' }));
    expect(writeText).toHaveBeenCalledWith('https://reddit.example/p/1');
    expect(await within(card).findByRole('button', { name: 'Copied' })).toBeTruthy();
  });

  it('a posted card without a stored URL offers no Copy link to post (#0583)', async () => {
    setup([hand({ id: 1, status: 'posted', posted_at: '2026-10-17T20:00:00Z', remote_url: null })]);
    await screen.findByRole('article', { name: 'Reddit Announce' });
    expect(screen.queryByRole('button', { name: 'Copy link to post' })).toBeNull();
  });

  it('lists only the hand-posted channels of its own section (#0640)', async () => {
    setup([post({ id: 1 }), hand({ id: 2 }), hand({ id: 3, wave: 'reminder', body: 'Reminder body' })]);
    await screen.findByRole('article', { name: 'Reddit Announce' });
    expect(screen.queryByRole('article', { name: 'Bluesky Announce' })).toBeNull();
    expect(screen.queryByText('Reminder body')).toBeNull();
    expect(screen.queryByRole('heading', { name: 'Posts' })).toBeNull();
    expect(screen.getByRole('region', { name: 'Post by hand' })).toBeTruthy();
  });

  it('hand rows offer Copy with link and Copy text only and never Post now or Schedule…', async () => {
    setup([hand({ id: 2 })]);
    await screen.findByRole('article', { name: 'Reddit Announce' });
    expect(screen.getAllByRole('button', { name: 'Copy with link' }).length).toBe(1);
    expect(screen.getAllByRole('button', { name: 'Copy text only' }).length).toBe(1);
    expect(screen.getAllByRole('button', { name: 'Mark posted' }).length).toBe(1);
    for (const gone of ['Post now', 'Schedule…', 'Unschedule']) expect(screen.queryByRole('button', { name: gone })).toBeNull();
  });

  it('the editor row is Save and Cancel with Delete set apart, and no schedule controls (#0591, #0640)', async () => {
    setup([hand({ id: 1 })]);
    await screen.findByRole('article', { name: 'Reddit Announce' });
    await fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    const card = screen.getByRole('article', { name: 'Reddit Announce' });
    const row = Array.from(card.querySelectorAll('.row')).find((r) => r.textContent?.includes('Save'));
    expect(row).toBeTruthy();
    const names = Array.from(row!.querySelectorAll('button')).map((b) => b.textContent?.trim());
    expect(names.slice(0, 2)).toEqual(['Save', 'Cancel']);
    expect(names[names.length - 1]).toBe('Delete');
    expect(row!.querySelector('.danger-end button')?.textContent?.trim()).toBe('Delete');
    for (const gone of ['Post now', 'Retry', 'Mark posted', 'Copy content', 'Schedule…', 'Change time…', 'Unschedule']) {
      expect(screen.queryByRole('button', { name: gone })).toBeNull();
    }
    expect(screen.queryByLabelText(/Pacific time/)).toBeNull();
  });

  it('Edit opens the composer inline in the card', async () => {
    setup([hand({ id: 1 })]);
    await screen.findByRole('article', { name: 'Reddit Announce' });
    expect(screen.queryByLabelText('Post text')).toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    const card = screen.getByRole('article', { name: 'Reddit Announce' });
    expect(card.querySelector('textarea')).toBeTruthy();
    expect(card.textContent).toMatch(/characters/);
  });

  it('Edit replaces the card display with the composer; Cancel restores it (#0579)', async () => {
    setup([hand({ id: 1 })]);
    const card = () => screen.getByRole('article', { name: 'Reddit Announce' });
    await screen.findByRole('article', { name: 'Reddit Announce' });
    expect(card().querySelector('pre.body')).toBeTruthy();
    await fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    expect(card().querySelector('pre.body')).toBeNull();
    expect(card().querySelector('textarea')).toBeTruthy();
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(card().querySelector('textarea')).toBeNull();
    expect(card().querySelector('pre.body')).toBeTruthy();
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Edit' })));
  });

  it('Save returns to display mode showing the new text (#0579)', async () => {
    setup([hand({ id: 1 })]);
    await screen.findByRole('article', { name: 'Reddit Announce' });
    updateOutreachPost.mockResolvedValue(hand({ id: 1, body: 'Brand new words' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    await fireEvent.input(screen.getByLabelText('Post text'), { target: { value: 'Brand new words' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(screen.getByRole('article', { name: 'Reddit Announce' }).querySelector('textarea')).toBeNull());
    expect(screen.getByText('Brand new words', { selector: 'pre.body' })).toBeTruthy();
  });

  it('opening a second card asks first in an in-app dialog when edits are unsaved (#0579, #0581)', async () => {
    const confirmFn = vi.fn(() => true);
    vi.stubGlobal('confirm', confirmFn);
    mountView(viewOf({ channels: [...channels, ig], posts: [hand({ id: 1 }), post({ id: 2, channel: 'instagram', status: 'manual', body: 'Second post' })] }));
    await screen.findByRole('article', { name: 'Reddit Announce' });
    const edit = (name: string) => within(screen.getByRole('article', { name })).getByRole('button', { name: 'Edit' });
    await fireEvent.click(edit('Reddit Announce'));
    await fireEvent.input(screen.getByLabelText('Post text'), { target: { value: 'dirty' } });
    await fireEvent.click(edit('Instagram Reels Announce'));
    const dlg = screen.getByRole('dialog');
    expect(dlg.textContent).toContain('Discard your unsaved changes?');
    expect(document.querySelectorAll('textarea').length).toBe(1);
    expect((screen.getByLabelText('Post text') as HTMLTextAreaElement).value).toBe('dirty');
    await fireEvent.click(within(dlg).getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect((screen.getByLabelText('Post text') as HTMLTextAreaElement).value).toBe('dirty');
    await fireEvent.click(edit('Instagram Reels Announce'));
    await fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Discard changes' }));
    expect(document.querySelectorAll('textarea').length).toBe(1);
    expect((screen.getByLabelText('Post text') as HTMLTextAreaElement).value).toBe('Second post');
    expect(confirmFn).not.toHaveBeenCalled();
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
    setupAll([post({ id: 2, channel: 'instagram', status: 'manual', body: igBody })]);
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
    expect(screen.queryByRole('article', { name: 'Bluesky Announce' })).toBeNull();
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
  it('a hand-posted card has no Delete and redo', async () => {
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

  it("saving a hand row's edit marks it customized and shows the badge (#0634)", async () => {
    updateOutreachPost.mockResolvedValue(tt({ body: 'My own words', customized: true }));
    onlyVideo([tt()]);
    await screen.findByRole('article', { name: 'TikTok Announce' });
    await fireEvent.click(btn('Edit'));
    await fireEvent.input(screen.getByLabelText('Post text'), { target: { value: 'My own words' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(updateOutreachPost).toHaveBeenCalledWith(3, { body: 'My own words', customized: true }));
    await waitFor(() => expect(row().querySelector('h5')?.textContent).toContain('Customized'));
  });

  it('reports which post has unsaved typing, and null once saved (#0632)', async () => {
    const onEditingDirty = vi.fn();
    updateOutreachPost.mockResolvedValue(tt({ body: 'Typed', customized: true }));
    onlyVideo([tt()], { onEditingDirty });
    await screen.findByRole('article', { name: 'TikTok Announce' });
    await fireEvent.click(btn('Edit'));
    await fireEvent.input(screen.getByLabelText('Post text'), { target: { value: 'Typed' } });
    await waitFor(() => expect(onEditingDirty).toHaveBeenLastCalledWith(3));
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(onEditingDirty).toHaveBeenLastCalledWith(null));
  });

  it('a hand row offers no Post now, Retry or Delete and redo (#0634)', async () => {
    onlyVideo([
      tt(),
      post({ id: 2, channel: 'instagram', status: 'failed', body: 'IG.\n\nSign up at the link in our bio.', short_url: 'https://go.example/u/ig', last_error: 'x' }),
      post({ id: 4, channel: 'youtube', status: 'posted', body: 'YT body', short_url: 'https://go.example/u/yt', posted_at: '2026-10-18T17:00:00Z' }),
    ]);
    await screen.findByRole('article', { name: 'TikTok Announce' });
    for (const name of ['Post now', 'Retry', 'Delete and redo…']) expect(screen.queryByRole('button', { name })).toBeNull();
  });

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

describe('editor layout (#0627, #0640)', () => {
  const card = () => screen.getByRole('article', { name: 'Reddit Announce' });
  const openEditor = async () => {
    setup([post({ id: 1, channel: 'reddit', status: 'manual' })]);
    await fireEvent.click(await screen.findByRole('button', { name: 'Edit' }));
  };

  it('the editor has no schedule picker and asks only to save before creating the link', async () => {
    await openEditor();
    expect(card().querySelector('input[type="datetime-local"]')).toBeNull();
    await fireEvent.input(screen.getByLabelText('Post text'), { target: { value: 'Edited' } });
    expect(within(card()).getByText('Save your edits before creating the link.')).toBeTruthy();
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
    const v = viewOf({
      posts: [post({ id: 2, channel: 'reddit', status: 'posted', posted_at: '2026-11-20T18:00:00Z' })],
      off_channels: [{ name: 'reddit', label: 'Reddit', automatic: false }],
    });
    mountView(v);
    expect(screen.getByRole('article', { name: 'Reddit Announce' })).toBeTruthy();
    cleanup();
    mountView(v, undefined, { wave: 'reminder' });
    expect(screen.queryByRole('article', { name: 'Reddit Reminder' })).toBeNull();
  });
});

describe('when to post (#0635, #0640)', () => {
  const tiktok = { name: 'tiktok', label: 'TikTok', utm_source: 'tiktok', utm_medium: 'video', char_limit: 0, automatic: false, configured: false, account: '' };
  const instagram = { ...tiktok, name: 'instagram', label: 'Instagram', utm_source: 'instagram' };
  const offBluesky = { ...channels[0], configured: false, char_limit: 300 };
  const WAVES = [{ name: 'reminder', label: 'Reminder', suggested_at: '2099-11-28T18:00:00Z' }];
  const handRow = (over: Partial<OutreachPost> = {}) => post({ id: 7, channel: 'tiktok', wave: 'reminder', body: 'Coming up: T.', ...over });
  const mountReminder = (v: WorkshopOutreach) => mountView(v, undefined, { wave: 'reminder' });

  it('the Video intro says the day once and no row title carries it (#0640)', async () => {
    mountReminder(viewOf({ channels: [tiktok, instagram], waves: WAVES, posts: [handRow(), handRow({ id: 8, channel: 'instagram' })] }));
    await screen.findByRole('article', { name: 'TikTok Reminder' });
    const intro = screen.getByText(/^One video, posted in each place by hand\./);
    expect(intro.textContent).toMatch(/^One video, posted in each place by hand\. Aim for .*Nov 28\. Each has its own link\.$/);
    expect(document.body.textContent?.match(/Aim for/g)?.length).toBe(1);
    for (const a of screen.getAllByRole('article')) expect(a.querySelector('.card-head .status')).toBeNull();
  });
  it('a posted hand row says when it was posted, not when to post', async () => {
    mountReminder(viewOf({ channels: [tiktok], waves: WAVES, posts: [handRow({ status: 'posted', posted_at: '2026-10-17T20:00:00Z' })] }));
    const card = await screen.findByRole('article', { name: 'TikTok Reminder' });
    const text = card.querySelector('.card-head .status')!.textContent!;
    expect(text).toMatch(/Posted/);
    expect(text).not.toMatch(/Aim/);
  });
  it('a video row shows no character count', async () => {
    mountReminder(viewOf({ channels: [tiktok], waves: WAVES, posts: [handRow()] }));
    const card = await screen.findByRole('article', { name: 'TikTok Reminder' });
    expect(card.textContent).not.toMatch(/characters/);
  });
  it('an unconnected Bluesky keeps its count and has no when line', async () => {
    mountReminder(viewOf({ channels: [offBluesky], waves: WAVES, posts: [post({ id: 8, wave: 'reminder' })] }));
    const card = await screen.findByRole('article', { name: 'Bluesky Reminder' });
    expect(card.querySelector('.card-head .status')).toBeNull();
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
    await waitFor(() => expect(updateOutreachPost).toHaveBeenCalledWith(7, { body: 'edited', customized: true }));
  });
  it('no offer when the text already matches', async () => {
    await open([handRow({ body: FRESH })]);
    expect(screen.queryByRole('button', USE)).toBeNull();
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
