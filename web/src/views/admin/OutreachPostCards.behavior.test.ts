// @vitest-environment jsdom
//
// #0569: mounts OutreachWorkshop.svelte (real OutreachPanel, OutreachPostCards,
// OutreachComposer) under jsdom with '../../lib/api' mocked. jsdom is not a
// browser (AGENTS.md §1): no browser was run for this change.
import { render, cleanup, waitFor, screen, fireEvent, within } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { OutreachLinksView, OutreachPost, OutreachResults, WorkshopOutreach } from '../../lib/types';

const getWorkshopOutreach = vi.fn<(id: number) => Promise<WorkshopOutreach>>();
const sendOutreachPost = vi.fn();
const updateOutreachPost = vi.fn();
vi.mock('../../lib/api', () => ({
  getWorkshopOutreach: (id: number) => getWorkshopOutreach(id),
  startOutreach: vi.fn(),
  getWorkshopOutreachResults: () => Promise.resolve({ status: 'not_started', posts: [], totals: null }),
  getWorkshopOutreachLinks: () =>
    Promise.resolve({
      available: true, unavailable_reason: '', outreach: { id: 9, campaign_slug: 's' }, clicks_status: 'ok', missing: 0, links: [], report: null,
    } as OutreachLinksView),
  createWorkshopOutreachLinks: vi.fn(),
  draftAllOutreach: vi.fn(),
  scheduleAllOutreach: vi.fn(),
  createOutreachPost: vi.fn(),
  sendOutreachPost: (id: number) => sendOutreachPost(id),
  unscheduleOutreachPost: vi.fn(),
  markOutreachPosted: vi.fn(),
  updateOutreachPost: (id: number, b: unknown) => updateOutreachPost(id, b),
  prepareOutreachPost: vi.fn(),
  scheduleOutreachPost: vi.fn(),
  deleteOutreachPost: vi.fn(),
  ApiError: class ApiError extends Error {},
}));

const { default: OutreachWorkshop } = await import('./OutreachWorkshop.svelte');

afterEach(() => {
  cleanup();
  getWorkshopOutreach.mockReset();
  sendOutreachPost.mockReset();
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
    prepared: true, stale: false, held: false, short_link_key: 'abc', short_url: 'https://go.example/abc', destination_url: 'https://x/w',
    utm: { source: 'bluesky', medium: 'social', campaign: 'c', content: 'announce' }, scheduled_at: null, posted_at: null, remote_url: null,
    last_error: null, created_at: '', updated_at: null, ...over,
  };
}

function setup(posts: OutreachPost[]): void {
  getWorkshopOutreach.mockResolvedValue({
    available: true, unavailable_reason: '',
    workshop: { id: 5, slug: 's', title: 'Soldering 101', status: 'published', starts_at: '2026-12-05T18:00:00Z' },
    outreach: { id: 9, workshop_id: 5, campaign_slug: 's', created_at: '' },
    channels, waves, posts,
  });
  render(OutreachWorkshop, { workshopId: 5 });
}

describe('post cards (#0569)', () => {
  it('shows the full body and each status line, grouped by wave', async () => {
    setup([
      post({ id: 1, status: 'posted', posted_at: '2026-10-17T20:00:00Z', remote_url: 'https://bsky.app/p/1' }),
      post({ id: 2, channel: 'reddit', status: 'manual', body: 'Reddit body text' }),
      post({ id: 3, wave: 'reminder', status: 'scheduled', scheduled_at: '2026-11-30T20:00:00Z', body: 'Reminder body' }),
      post({ id: 4, wave: 'reminder', channel: 'reddit', status: 'failed', body: 'Failing body', last_error: 'raw' }),
    ]);
    expect(await screen.findByText(/Come solder with us/)).toBeTruthy();
    expect(screen.getByText('Reddit body text')).toBeTruthy();
    expect(screen.getByText('Reminder body')).toBeTruthy();
    expect(screen.getByRole('link', { name: 'view post' }).getAttribute('href')).toBe('https://bsky.app/p/1');
    expect(screen.getByText(/^Posts automatically /)).toBeTruthy();
    expect(screen.getByText('Post by hand: copy and paste')).toBeTruthy();
    expect(screen.getByText('Failed: the post could not be sent')).toBeTruthy();
    const headings = screen.getAllByRole('heading', { level: 4 }).map((h) => h.textContent);
    expect(headings).toEqual(['Announce', 'Reminder']);
  });

  it('shows Not scheduled for an automatic draft and Not drafted with a Draft button', async () => {
    setup([post({ id: 1 })]);
    expect(await screen.findByText('Not scheduled')).toBeTruthy();
    expect(screen.getAllByText('Not drafted').length).toBe(3);
    expect(screen.getAllByRole('button', { name: 'Draft' }).length).toBe(3);
  });

  it('automatic cards offer Post now and Schedule…, manual cards offer Copy text and not Post now', async () => {
    setup([post({ id: 1 }), post({ id: 2, channel: 'reddit', status: 'manual' })]);
    await screen.findByText('Not scheduled');
    expect(screen.getAllByRole('button', { name: 'Post now' }).length).toBe(1);
    expect(screen.getAllByRole('button', { name: 'Schedule…' }).length).toBe(1);
    expect(screen.getAllByRole('button', { name: 'Copy text' }).length).toBe(1);
    expect(screen.getAllByRole('button', { name: 'Mark posted' }).length).toBe(1);
    const manual = screen.getByRole('article', { name: 'Reddit Announce' });
    expect(manual.textContent).not.toContain('Post now');
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
    expect(card().querySelector('pre.body')).toBeTruthy();
    await fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    expect(card().querySelector('pre.body')).toBeNull();
    expect(card().textContent).not.toContain('Not scheduled');
    expect(card().querySelector('textarea')).toBeTruthy();
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(card().querySelector('textarea')).toBeNull();
    expect(card().querySelector('pre.body')).toBeTruthy();
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
    await screen.findByText('Reddit body');
    await fireEvent.click(screen.getByRole('button', { name: 'Mark posted' }));
    const dlg = screen.getByRole('dialog');
    await fireEvent.input(within(dlg).getByLabelText(/Link to the post/), { target: { value: 'ftp://x.example/1' } });
    expect((within(dlg).getByRole('button', { name: 'Mark posted' }) as HTMLButtonElement).disabled).toBe(true);
    expect(dlg.textContent).toContain('http(s)');
  });

  it('Copy text flips to Copied for two seconds (#0581)', async () => {
    setup([post({ id: 2, channel: 'reddit', status: 'manual', body: 'Reddit body' })]);
    await screen.findByText('Reddit body');
    const writeText = vi.fn(() => Promise.resolve());
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    await fireEvent.click(screen.getByRole('button', { name: 'Copy text' }));
    await vi.advanceTimersByTimeAsync(0);
    expect(writeText).toHaveBeenCalledWith('Reddit body');
    expect(screen.getByRole('button', { name: 'Copied' })).toBeTruthy();
    await vi.advanceTimersByTimeAsync(1999);
    expect(screen.getByRole('button', { name: 'Copied' })).toBeTruthy();
    await vi.advanceTimersByTimeAsync(1);
    expect(screen.getByRole('button', { name: 'Copy text' })).toBeTruthy();
    expect(screen.getByRole('article', { name: 'Reddit Announce' }).querySelector('.notice')?.textContent).toBe('');
    vi.useRealTimers();
  });

  it('the tracked links table is collapsed by default and counts links', async () => {
    setup([post({ id: 1 })]);
    await screen.findByText('Not scheduled');
    const details = screen.getByText(/Tracked links \(/).closest('details');
    expect(details).toBeTruthy();
    expect(details?.hasAttribute('open')).toBe(false);
  });

  it('shows the summary line', async () => {
    setup([post({ id: 1 }), post({ id: 2, wave: 'reminder', status: 'scheduled', scheduled_at: '2026-11-30T20:00:00Z' })]);
    expect(await screen.findByText('Drafted: Announce 1, Reminder 1 · 1 scheduled · 0 posted')).toBeTruthy();
  });
});

describe('post cards: one link, Instagram, video channels (#0576)', () => {
  const chans = [
    ...channels,
    { name: 'instagram', label: 'Instagram', utm_source: 'instagram', utm_medium: 'social', char_limit: 0, automatic: false, configured: false, account: '' },
    { name: 'tiktok', label: 'TikTok', utm_source: 'tiktok', utm_medium: 'video', char_limit: 0, automatic: false, configured: false, account: '' },
  ];
  function setupAll(posts: OutreachPost[]): void {
    getWorkshopOutreach.mockResolvedValue({
      available: true, unavailable_reason: '',
      workshop: { id: 5, slug: 's', title: 'Soldering 101', status: 'published', starts_at: '2026-12-05T18:00:00Z' },
      outreach: { id: 9, workshop_id: 5, campaign_slug: 's', created_at: '' },
      channels: chans, waves: [waves[0]], posts,
    });
    render(OutreachWorkshop, { workshopId: 5 });
  }
  const igBody = 'Soldering 101: Saturday.\n\nSign up at the link in our bio.';

  it('does not print the short link under the body; manual cards get one labelled Link to paste row', async () => {
    setupAll([
      post({ id: 1, body: 'Bluesky text without a link' }),
      post({ id: 2, channel: 'instagram', status: 'manual', body: igBody }),
    ]);
    await screen.findByText('Bluesky text without a link');
    const bluesky = screen.getByRole('article', { name: 'Bluesky Announce' });
    expect(bluesky.textContent).not.toContain('https://go.example/abc');
    expect(bluesky.textContent).not.toContain('Link to paste');
    const ig = screen.getByRole('article', { name: 'Instagram Announce' });
    expect(ig.textContent).toContain('Link to paste');
    expect(ig.textContent?.split('https://go.example/abc').length).toBe(2); // exactly once
    expect(ig.querySelector('pre')?.textContent).toBe(igBody);
    expect(screen.getByRole('button', { name: 'Copy link' })).toBeTruthy();
  });

  it('Instagram offers Copy text and Open flyer', async () => {
    setupAll([post({ id: 2, channel: 'instagram', status: 'manual', body: igBody })]);
    await screen.findByText(/Sign up at the link in our bio/);
    const ig = screen.getByRole('article', { name: 'Instagram Announce' });
    expect(ig.textContent).toContain('Copy text');
    const link = ig.querySelector('a.open-flyer');
    expect(link?.textContent).toBe('Open flyer');
    expect(link?.getAttribute('href')).toMatch(/flyers/);
    const bsky = screen.getByRole('article', { name: 'Bluesky Announce' });
    expect(bsky.querySelector('a.open-flyer')).toBeNull();
  });

  it('collapses TikTok under Video channels by default', async () => {
    setupAll([post({ id: 3, channel: 'tiktok', status: 'manual', body: 'TikTok description block' })]);
    await screen.findByText('TikTok description block');
    const details = screen.getByText(/^Video channels/).closest('details');
    expect(details).toBeTruthy();
    expect(details?.hasAttribute('open')).toBe(false);
    expect(details?.textContent).toContain('TikTok');
    expect(details?.textContent).toContain('TikTok description block');
    expect(screen.getByRole('article', { name: 'Bluesky Announce' }).closest('details.video')).toBeNull();
  });
});

describe('post cards: both networks (#0581)', () => {
  const two = [
    { name: 'bluesky', label: 'Bluesky', utm_source: 'bluesky', utm_medium: 'social', char_limit: 300, automatic: true, configured: true, account: '@club.test' },
    { name: 'mastodon', label: 'Mastodon', utm_source: 'mastodon', utm_medium: 'social', char_limit: 500, automatic: true, configured: true, account: 'social.test' },
  ];
  function setupTwo(): void {
    getWorkshopOutreach.mockResolvedValue({
      available: true, unavailable_reason: '',
      workshop: { id: 5, slug: 's', title: 'Soldering 101', status: 'published', starts_at: '2026-12-05T18:00:00Z' },
      outreach: { id: 9, workshop_id: 5, campaign_slug: 's', created_at: '' },
      channels: two, waves: [waves[0]],
      posts: [post({ id: 1, body: 'Bluesky words' }), post({ id: 2, channel: 'mastodon', body: 'Mastodon words' })],
    });
    render(OutreachWorkshop, { workshopId: 5 });
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

  it('is not offered when only one network can post', async () => {
    setup([post({ id: 1 })]);
    await screen.findByText('Not scheduled');
    expect(screen.queryByRole('button', { name: /^Post to / })).toBeNull();
  });
});
