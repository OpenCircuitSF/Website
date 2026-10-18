// @vitest-environment jsdom
//
// #0582: mounts OutreachAnnouncement.svelte (real OutreachComposer and
// OutreachPostDialog) under jsdom with '../../lib/api' mocked. The test plays
// the panel's part: onChange swaps the changed post into the view and
// re-renders. jsdom is not a browser (AGENTS.md section 1): layout is not
// proved here.
import { render, cleanup, waitFor, screen, fireEvent, within } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { OutreachPost, WorkshopOutreach } from '../../lib/types';

const updateOutreachPost = vi.fn();
const sendOutreachPost = vi.fn();
const draftAllOutreach = vi.fn();
const scheduleOutreachPost = vi.fn();
const unscheduleOutreachPost = vi.fn();
vi.mock('../../lib/api', () => ({
  updateOutreachPost: (id: number, b: unknown) => updateOutreachPost(id, b),
  sendOutreachPost: (id: number) => sendOutreachPost(id),
  draftAllOutreach: (id: number, w: string[]) => draftAllOutreach(id, w),
  scheduleOutreachPost: (id: number, at: string) => scheduleOutreachPost(id, at),
  unscheduleOutreachPost: (id: number) => unscheduleOutreachPost(id),
  prepareOutreachPost: vi.fn(),
  deleteOutreachPost: vi.fn(),
  ApiError: class ApiError extends Error {},
}));

const { default: OutreachAnnouncement } = await import('./OutreachAnnouncement.svelte');

afterEach(() => {
  cleanup();
  for (const m of [updateOutreachPost, sendOutreachPost, draftAllOutreach, scheduleOutreachPost, unscheduleOutreachPost]) m.mockReset();
});

const ch = (name: string, limit: number, account: string) => ({
  name, label: name === 'bluesky' ? 'Bluesky' : 'Mastodon', utm_source: name, utm_medium: 'social', char_limit: limit, automatic: true, configured: true, account,
});
const channels = [ch('bluesky', 300, '@club.test'), ch('mastodon', 500, 'social.test')];
const waves = [
  { name: 'announce', label: 'Announce', suggested_at: null },
  { name: 'reminder', label: 'Reminder', suggested_at: '2099-11-28T18:00:00Z' },
];
const MURL = 'https://go.example/m';
const bsky = 'Join us for Soldering 101.\n\nLearn to solder.\n\n#soldering #sf';
const mast = `Join us for Soldering 101.\n\nLearn to solder.\n\nSpots are limited, so you can sign up here: ${MURL}\n\n#soldering #sf`;

function post(over: Partial<OutreachPost>): OutreachPost {
  return {
    id: 1, outreach_id: 9, channel: 'bluesky', wave: 'announce', body: bsky, status: 'draft', prepared: true, stale: false, customized: false, held: false,
    short_link_key: 'k', short_url: 'https://go.example/b', destination_url: 'https://x/w', utm: { source: 'bluesky', medium: 'social', campaign: 'c', content: 'announce' },
    scheduled_at: null, posted_at: null, remote_url: null, last_error: null, created_at: '', updated_at: null, ...over,
  };
}
const bPost = () => post({});
const mPost = () => post({ id: 2, channel: 'mastodon', body: mast, short_url: MURL });

function mountView(posts: OutreachPost[], wave = 'announce') {
  let current: WorkshopOutreach = {
    available: true, unavailable_reason: '',
    workshop: { id: 5, slug: 's', title: 'Soldering 101', status: 'published', starts_at: '2026-12-05T18:00:00Z' },
    outreach: { id: 9, workshop_id: 9, campaign_slug: 's', created_at: '' },
    channels, waves, posts,
  };
  const reload = vi.fn();
  const r = render(OutreachAnnouncement, {
    view: current, results: null, wave, workshopId: 5, reload,
    onChange: (p: OutreachPost) => {
      current = { ...current, posts: current.posts.map((x) => (x.id === p.id ? p : x)) };
      void r.rerender({ view: current });
    },
  });
  return { reload, rerender: (posts2: OutreachPost[]) => r.rerender({ view: { ...current, posts: posts2 } }), current: () => current };
}

const message = () => screen.getByLabelText('Message') as HTMLTextAreaElement;
const type = (v: string) => fireEvent.input(message(), { target: { value: v } });

describe('OutreachAnnouncement', () => {
  it('previews each network as its platform card and warns when both texts are identical (#0583)', async () => {
    mountView([bPost(), mPost()]);
    expect(document.querySelectorAll('.preview .card').length).toBe(2);
    expect(document.querySelector('.hashtag')?.textContent).toBe('#soldering');
    expect(screen.queryByText('Bluesky and Mastodon will get exactly the same text.')).toBeNull();
    cleanup();
    mountView([bPost(), post({ id: 2, channel: 'mastodon', body: bsky, short_url: MURL })]);
    expect(screen.getAllByText('Bluesky and Mastodon will get exactly the same text.').length).toBe(1);
  });
  it('seeds the one Message field from the posts and previews each network', () => {
    mountView([bPost(), mPost()]);
    expect(message().value).toBe('Join us for Soldering 101.\n\nLearn to solder.');
    expect(screen.getByRole('article', { name: 'Bluesky preview' }).textContent).toContain('@club.test');
    expect(screen.getByRole('article', { name: 'Mastodon preview' }).textContent).toContain(MURL);
    expect(screen.getByRole('button', { name: 'Post to Bluesky and Mastodon' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Schedule for later…' })).toBeTruthy();
    expect((screen.getByRole('button', { name: 'Save message' }) as HTMLButtonElement).disabled).toBe(true);
  });

  it('Save message writes the message plus each post\'s own tail, in order', async () => {
    mountView([bPost(), mPost()]);
    updateOutreachPost.mockImplementation((id: number, b: { body: string }) => Promise.resolve(post({ id, channel: id === 1 ? 'bluesky' : 'mastodon', body: b.body })));
    await type('A fresh message.');
    // Live preview of what Save will write.
    expect(screen.getByRole('article', { name: 'Bluesky preview' }).textContent).toContain('A fresh message.');
    await fireEvent.click(screen.getByRole('button', { name: 'Save message' }));
    await waitFor(() => expect(updateOutreachPost).toHaveBeenCalledTimes(2));
    expect(updateOutreachPost.mock.calls[0]).toEqual([1, { body: 'A fresh message.\n\n#soldering #sf' }]);
    expect(updateOutreachPost.mock.calls[1]).toEqual([2, { body: `A fresh message.\n\nSpots are limited, so you can sign up here: ${MURL}\n\n#soldering #sf` }]);
    await waitFor(() => expect(screen.getByRole('status').textContent).toBe('Saved to Bluesky and Mastodon.'));
  });

  it('Save message skips a customized post and says so', async () => {
    mountView([bPost(), { ...mPost(), customized: true }]);
    updateOutreachPost.mockImplementation((id: number, b: { body: string }) => Promise.resolve(post({ id, channel: id === 1 ? 'bluesky' : 'mastodon', body: b.body })));
    await type('Only for Bluesky.');
    await fireEvent.click(screen.getByRole('button', { name: 'Save message' }));
    await waitFor(() => expect(updateOutreachPost).toHaveBeenCalledTimes(1));
    expect(updateOutreachPost.mock.calls[0][0]).toBe(1);
    await waitFor(() => expect(screen.getByRole('status').textContent).toBe('Saved to Bluesky. Mastodon is customized and was left alone.'));
    expect(screen.getByRole('article', { name: 'Mastodon preview' }).textContent).toContain('Customized');
  });

  it('Save message is disabled, with a reason, when the message is over a limit', async () => {
    mountView([bPost(), mPost()]);
    await type('x'.repeat(400));
    expect((screen.getByRole('button', { name: 'Save message' }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText(/over the limit for Bluesky/)).toBeTruthy();
    expect(updateOutreachPost).not.toHaveBeenCalled();
  });

  it('Customize… opens the composer for one post and Save sends customized:true', async () => {
    mountView([bPost(), mPost()]);
    updateOutreachPost.mockImplementation((id: number, b: { body: string }) => Promise.resolve(post({ id, channel: id === 1 ? 'bluesky' : 'mastodon', body: b.body, customized: true })));
    const card = screen.getByRole('article', { name: 'Mastodon preview' });
    await fireEvent.click(within(card).getByRole('button', { name: 'Customize…' }));
    const box = within(card).getByLabelText('Post text');
    await fireEvent.input(box, { target: { value: 'Mastodon only text' } });
    await fireEvent.click(within(card).getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(updateOutreachPost).toHaveBeenCalledWith(2, { body: 'Mastodon only text', customized: true }));
    await waitFor(() => expect(within(screen.getByRole('article', { name: 'Mastodon preview' })).queryByLabelText('Post text')).toBeNull());
    expect(screen.getByRole('article', { name: 'Mastodon preview' }).textContent).toContain('Customized');
  });

  it('Use shared message hands a customized post back with customized:false and its own tail', async () => {
    mountView([bPost(), { ...mPost(), customized: true }]);
    updateOutreachPost.mockImplementation((id: number, b: { body: string }) => Promise.resolve(post({ id, channel: 'mastodon', body: b.body })));
    const card = screen.getByRole('article', { name: 'Mastodon preview' });
    await fireEvent.click(within(card).getByRole('button', { name: 'Use shared message' }));
    await waitFor(() =>
      expect(updateOutreachPost).toHaveBeenCalledWith(2, {
        body: `Join us for Soldering 101.\n\nLearn to solder.\n\nSpots are limited, so you can sign up here: ${MURL}\n\n#soldering #sf`,
        customized: false,
      }),
    );
  });

  it('Post opens the shared dialog; Cancel sends nothing, Post sends each once and clears its result line when posted', async () => {
    const m = mountView([bPost(), mPost()]);
    await fireEvent.click(screen.getByRole('button', { name: 'Post to Bluesky and Mastodon' }));
    const dlg = screen.getByRole('dialog');
    expect(dlg.textContent).toContain('cannot be unsent');
    await fireEvent.click(within(dlg).getByRole('button', { name: 'Cancel' }));
    expect(sendOutreachPost).not.toHaveBeenCalled();
    sendOutreachPost.mockImplementation((id: number) => Promise.resolve(post({ id, channel: id === 1 ? 'bluesky' : 'mastodon', status: 'queued' })));
    await fireEvent.click(screen.getByRole('button', { name: 'Post to Bluesky and Mastodon' }));
    await fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Post now' }));
    await waitFor(() => expect(sendOutreachPost).toHaveBeenCalledTimes(2));
    expect(await screen.findByText('Bluesky: queued · Mastodon: queued')).toBeTruthy();
    await m.rerender([post({ status: 'posted' }), post({ id: 2, channel: 'mastodon', status: 'posted' })]);
    expect(screen.queryByText('Bluesky: queued · Mastodon: queued')).toBeNull();
  });

  it('the Post button is disabled while the message has unsaved typing', async () => {
    mountView([bPost(), mPost()]);
    await type('Unsaved words');
    expect((screen.getByRole('button', { name: 'Post to Bluesky and Mastodon' }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText('Save the message before posting or scheduling.')).toBeTruthy();
  });

  it('Schedule for later… schedules every postable post at one time', async () => {
    mountView([bPost(), mPost()]);
    scheduleOutreachPost.mockImplementation((id: number, at: string) =>
      Promise.resolve(post({ id, channel: id === 1 ? 'bluesky' : 'mastodon', status: 'scheduled', scheduled_at: at })),
    );
    await fireEvent.click(screen.getByRole('button', { name: 'Schedule for later…' }));
    await fireEvent.input(screen.getByLabelText('Schedule for (Pacific time)'), { target: { value: '2099-11-28T10:00' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Schedule' }));
    await waitFor(() => expect(scheduleOutreachPost).toHaveBeenCalledTimes(2));
    expect(scheduleOutreachPost.mock.calls.map((c) => c[0])).toEqual([1, 2]);
    expect(scheduleOutreachPost.mock.calls[0][1]).toBe(scheduleOutreachPost.mock.calls[1][1]);
    expect(scheduleOutreachPost.mock.calls[0][1]).toBe('2099-11-28T18:00:00Z');
    expect(await screen.findByRole('button', { name: 'Unschedule' })).toBeTruthy();
  });

  it('Unschedule returns every scheduled post to draft (#0582 review)', async () => {
    const at = '2099-11-28T18:00:00Z';
    mountView([post({ status: 'scheduled', scheduled_at: at }), post({ id: 2, channel: 'mastodon', body: mast, short_url: MURL, status: 'scheduled', scheduled_at: at })]);
    unscheduleOutreachPost.mockImplementation((id: number) =>
      Promise.resolve(id === 1 ? bPost() : mPost()),
    );
    await fireEvent.click(screen.getByRole('button', { name: 'Unschedule' }));
    await waitFor(() => expect(unscheduleOutreachPost).toHaveBeenCalledTimes(2));
    expect(unscheduleOutreachPost.mock.calls.map((c) => c[0])).toEqual([1, 2]);
    expect(await screen.findByText('Back to draft.')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Unschedule' })).toBeNull();
  });

  it('with no posts, shows the empty state and Write the announcement drafts and reloads', async () => {
    const m = mountView([]);
    draftAllOutreach.mockResolvedValue({ created: [], skipped: 0, without_link: 0 });
    expect(screen.getByText('No posts yet. Start with the announcement.')).toBeTruthy();
    expect(screen.queryByLabelText('Message')).toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: 'Write the announcement' }));
    await waitFor(() => expect(draftAllOutreach).toHaveBeenCalledWith(5, []));
    await waitFor(() => expect(m.reload).toHaveBeenCalledTimes(1));
  });

  it('keeps its live regions at the top level of the section, not inside a list', () => {
    mountView([bPost(), mPost()]);
    const section = screen.getByRole('region', { name: 'Announce' });
    expect(section.querySelectorAll('article [role="status"], article [role="alert"]').length).toBe(0);
  });
});
