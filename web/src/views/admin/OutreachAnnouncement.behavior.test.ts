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
const scheduleOutreachSection = vi.fn();
const unscheduleOutreachSection = vi.fn();
vi.mock('../../lib/api', () => ({
  updateOutreachPost: (id: number, b: unknown) => updateOutreachPost(id, b),
  sendOutreachPost: (id: number) => sendOutreachPost(id),
  draftAllOutreach: (id: number, w: string[]) => draftAllOutreach(id, w),
  scheduleOutreachSection: (w: number, wave: string, at: string) => scheduleOutreachSection(w, wave, at),
  unscheduleOutreachSection: (w: number, wave: string) => unscheduleOutreachSection(w, wave),
  prepareOutreachPost: vi.fn(),
  deleteOutreachPost: vi.fn(),
  ApiError: class ApiError extends Error {},
}));

const { default: OutreachAnnouncement } = await import('./OutreachAnnouncement.svelte');

afterEach(() => {
  cleanup();
  for (const m of [updateOutreachPost, sendOutreachPost, draftAllOutreach, scheduleOutreachSection, unscheduleOutreachSection]) m.mockReset();
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

function mountView(posts: OutreachPost[], wave = 'announce', onUnsavedChange?: (unsaved: boolean) => void) {
  let current: WorkshopOutreach = {
    available: true, unavailable_reason: '',
    workshop: { id: 5, slug: 's', title: 'Soldering 101', status: 'published', starts_at: '2026-12-05T18:00:00Z' },
    outreach: { id: 9, workshop_id: 9, campaign_slug: 's', created_at: '' },
    channels, waves, posts,
  };
  const reload = vi.fn();
  const r = render(OutreachAnnouncement, {
    view: current, results: null, wave, workshopId: 5, reload, onUnsavedChange,
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
  it('titles each preview with its channel and wave, and keeps the account', () => {
    mountView([bPost(), mPost()]);
    const title = screen.getByRole('article', { name: 'Bluesky Announce preview' }).querySelector('h5')!;
    expect(title.textContent).toContain('Bluesky · Announce');
    expect(title.querySelector('.account')!.textContent).toBe('@club.test');
  });

  it('titles previews with the wave they belong to in a later section', () => {
    mountView([post({ wave: 'reminder' })], 'reminder');
    expect(screen.getByRole('article', { name: 'Bluesky Reminder preview' }).querySelector('h5')!.textContent).toContain('Bluesky · Reminder');
  });

  it('renders no pinned strip of its own in any section (#0625)', () => {
    mountView([bPost(), mPost()]);
    expect(document.querySelector('.wave-pin')).toBeNull();
    cleanup();
    mountView([post({ wave: 'reminder' })], 'reminder');
    expect(document.querySelector('.wave-pin')).toBeNull();
  });

  it('reports unsaved typing and clears it after Save message (#0625)', async () => {
    const onUnsavedChange = vi.fn();
    updateOutreachPost.mockImplementation(async (id: number, b: { body: string }) => (id === 1 ? { ...bPost(), body: b.body } : { ...mPost(), body: b.body }));
    mountView([bPost(), mPost()], 'announce', onUnsavedChange);
    expect(onUnsavedChange).toHaveBeenLastCalledWith(false);
    await type('New words');
    expect(onUnsavedChange).toHaveBeenLastCalledWith(true);
    await fireEvent.click(screen.getByRole('button', { name: 'Save message' }));
    await waitFor(() => expect(onUnsavedChange).toHaveBeenLastCalledWith(false));
  });

  it('reports an unsaved Customize edit and clears it on Cancel (#0625)', async () => {
    const onUnsavedChange = vi.fn();
    mountView([bPost(), mPost()], 'announce', onUnsavedChange);
    await fireEvent.click(screen.getAllByRole('button', { name: 'Customize…' })[0]);
    const article = screen.getByRole('article', { name: 'Bluesky Announce preview' });
    await fireEvent.input(article.querySelector('textarea')!, { target: { value: 'Edited' } });
    expect(onUnsavedChange).toHaveBeenLastCalledWith(true);
    await fireEvent.click(within(article).getByRole('button', { name: 'Cancel' }));
    await fireEvent.click(await screen.findByRole('button', { name: 'Discard changes' }));
    await waitFor(() => expect(onUnsavedChange).toHaveBeenLastCalledWith(false));
  });

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
    expect(screen.getByRole('article', { name: 'Bluesky Announce preview' }).textContent).toContain('@club.test');
    expect(screen.getByRole('article', { name: 'Mastodon Announce preview' }).textContent).toContain(MURL);
    expect(screen.getByRole('button', { name: 'Post to Bluesky and Mastodon' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Schedule…' })).toBeTruthy();
    expect((screen.getByRole('button', { name: 'Save message' }) as HTMLButtonElement).disabled).toBe(true);
  });

  it('Save message writes the message plus each post\'s own tail, in order', async () => {
    mountView([bPost(), mPost()]);
    updateOutreachPost.mockImplementation((id: number, b: { body: string }) => Promise.resolve(post({ id, channel: id === 1 ? 'bluesky' : 'mastodon', body: b.body })));
    await type('A fresh message.');
    // Live preview of what Save will write.
    expect(screen.getByRole('article', { name: 'Bluesky Announce preview' }).textContent).toContain('A fresh message.');
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
    expect(screen.getByRole('article', { name: 'Mastodon Announce preview' }).textContent).toContain('Customized');
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
    const card = screen.getByRole('article', { name: 'Mastodon Announce preview' });
    await fireEvent.click(within(card).getByRole('button', { name: 'Customize…' }));
    const box = within(card).getByLabelText('Post text');
    await fireEvent.input(box, { target: { value: 'Mastodon only text' } });
    await fireEvent.click(within(card).getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(updateOutreachPost).toHaveBeenCalledWith(2, { body: 'Mastodon only text', customized: true }));
    await waitFor(() => expect(within(screen.getByRole('article', { name: 'Mastodon Announce preview' })).queryByLabelText('Post text')).toBeNull());
    expect(screen.getByRole('article', { name: 'Mastodon Announce preview' }).textContent).toContain('Customized');
  });

  it('Customize… opens an editor with no schedule controls (#0640)', async () => {
    mountView([bPost(), mPost()]);
    const card = screen.getByRole('article', { name: 'Mastodon Announce preview' });
    await fireEvent.click(within(card).getByRole('button', { name: 'Customize…' }));
    expect(card.querySelector('textarea')).toBeTruthy();
    expect(card.querySelector('input[type="datetime-local"]')).toBeNull();
    for (const gone of ['Schedule…', 'Change time…', 'Unschedule']) expect(within(card).queryByRole('button', { name: gone })).toBeNull();
  });

  it('Use shared message hands a customized post back with customized:false and its own tail', async () => {
    mountView([bPost(), { ...mPost(), customized: true }]);
    updateOutreachPost.mockImplementation((id: number, b: { body: string }) => Promise.resolve(post({ id, channel: 'mastodon', body: b.body })));
    const card = screen.getByRole('article', { name: 'Mastodon Announce preview' });
    await fireEvent.click(within(card).getByRole('button', { name: 'Use shared message' }));
    await waitFor(() =>
      expect(updateOutreachPost).toHaveBeenCalledWith(2, {
        body: `Join us for Soldering 101.\n\nLearn to solder.\n\nSpots are limited, so you can sign up here: ${MURL}\n\n#soldering #sf`,
        customized: false,
      }),
    );
  });

  it('Use shared message restores the link line and hashtags a customized Mastodon body lost (#0593)', async () => {
    mountView([bPost(), { ...mPost(), customized: true, body: 'My own words.' }]);
    updateOutreachPost.mockImplementation((id: number, b: { body: string }) => Promise.resolve(post({ id, channel: 'mastodon', body: b.body })));
    const card = screen.getByRole('article', { name: 'Mastodon Announce preview' });
    await fireEvent.click(within(card).getByRole('button', { name: 'Use shared message' }));
    await waitFor(() => expect(updateOutreachPost).toHaveBeenCalledTimes(1));
    const sent = updateOutreachPost.mock.calls[0][1] as { body: string; customized: boolean };
    expect(sent.customized).toBe(false);
    expect(sent.body).toContain(`Sign up here: ${MURL}`);
    expect(sent.body).toContain('#soldering #sf');
  });

  it('warns on the Announcement section when a post is stale, and Regenerate rewrites only non-customized ones (#0593)', async () => {
    mountView([{ ...bPost(), stale: true }, { ...mPost(), stale: true, customized: true }]);
    expect(screen.getByText(/changed since Bluesky and Mastodon were drafted/)).toBeTruthy();
    updateOutreachPost.mockImplementation((id: number) => Promise.resolve(post({ id })));
    await fireEvent.click(screen.getByRole('button', { name: 'Regenerate' }));
    await waitFor(() => expect(updateOutreachPost).toHaveBeenCalledTimes(1));
    expect(updateOutreachPost).toHaveBeenCalledWith(1, { regenerate: true });
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

  it('Schedule… sends one call for the section with one time, then reloads (#0640)', async () => {
    const m = mountView([post({ wave: 'reminder' }), post({ id: 2, wave: 'reminder', channel: 'mastodon', body: mast, short_url: MURL })], 'reminder');
    scheduleOutreachSection.mockImplementation((_w: number, _wave: string, at: string) =>
      Promise.resolve({ posts: [post({ wave: 'reminder', status: 'scheduled', scheduled_at: at }), post({ id: 2, wave: 'reminder', channel: 'mastodon', status: 'scheduled', scheduled_at: at })], left_alone: [] }),
    );
    await fireEvent.click(screen.getByRole('button', { name: 'Schedule…' }));
    await fireEvent.input(screen.getByLabelText('Post on (Pacific time)'), { target: { value: '2099-11-28T10:00' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Schedule' }));
    await waitFor(() => expect(scheduleOutreachSection).toHaveBeenCalledTimes(1));
    expect(scheduleOutreachSection).toHaveBeenCalledWith(5, 'reminder', '2099-11-28T18:00:00Z');
    await waitFor(() => expect(m.reload).toHaveBeenCalled());
    expect(screen.getByRole('status').textContent).toMatch(/^Scheduled for .* on Bluesky and Mastodon\.$/);
    expect(screen.queryByLabelText('Post on (Pacific time)')).toBeNull();
  });

  it('says a channel already posted was left alone', async () => {
    mountView([post({ wave: 'reminder', status: 'posted', posted_at: '2026-11-01T18:00:00Z' }), post({ id: 2, wave: 'reminder', channel: 'mastodon', body: mast, short_url: MURL })], 'reminder');
    scheduleOutreachSection.mockResolvedValue({
      posts: [post({ id: 2, wave: 'reminder', channel: 'mastodon', status: 'scheduled', scheduled_at: '2099-11-28T18:00:00Z' })],
      left_alone: [{ channel: 'bluesky', channel_label: 'Bluesky', reason: 'posted' }],
    });
    await fireEvent.click(screen.getByRole('button', { name: 'Schedule…' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Schedule' }));
    await waitFor(() => expect(screen.getByRole('status').textContent).toMatch(/on Mastodon\. Bluesky was already posted and was left alone\.$/));
  });

  it('Unschedule sends one call for the section and reloads', async () => {
    const at = '2099-11-28T18:00:00Z';
    const m = mountView([post({ status: 'scheduled', scheduled_at: at }), post({ id: 2, channel: 'mastodon', body: mast, short_url: MURL, status: 'scheduled', scheduled_at: at })]);
    unscheduleOutreachSection.mockResolvedValue({ posts: [bPost(), mPost()], left_alone: [] });
    await fireEvent.click(screen.getByRole('button', { name: 'Unschedule' }));
    await waitFor(() => expect(unscheduleOutreachSection).toHaveBeenCalledTimes(1));
    expect(unscheduleOutreachSection).toHaveBeenCalledWith(5, 'announce');
    expect(await screen.findByText('Back to draft.')).toBeTruthy();
    expect(m.reload).toHaveBeenCalled();
  });

  it('a scheduled section reads Scheduled for … on Bluesky and Mastodon with Change time…, Unschedule and Post', () => {
    const at = '2099-11-28T18:00:00Z';
    mountView([post({ status: 'scheduled', scheduled_at: at }), post({ id: 2, channel: 'mastodon', body: mast, short_url: MURL, status: 'scheduled', scheduled_at: at })]);
    expect(document.querySelector('.when-line')!.textContent).toMatch(/^Scheduled for .* on Bluesky and Mastodon\.$/);
    const row = document.querySelector('.when-line')!.nextElementSibling!;
    expect(Array.from(row.querySelectorAll('button')).map((b) => b.textContent?.trim())).toEqual(['Change time…', 'Unschedule', 'Post to Bluesky and Mastodon']);
  });

  it("shows the server's message naming the channel when the section cannot be scheduled", async () => {
    const { ApiError } = await import('../../lib/api');
    mountView([post({ wave: 'reminder' }), post({ id: 2, wave: 'reminder', channel: 'mastodon', body: mast, short_url: MURL })], 'reminder');
    scheduleOutreachSection.mockRejectedValue(new (ApiError as unknown as new (m: string) => Error)('Mastodon has no link yet'));
    await fireEvent.click(screen.getByRole('button', { name: 'Schedule…' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Schedule' }));
    expect(await screen.findByText('Mastodon has no link yet')).toBeTruthy();
    expect(screen.getByLabelText('Post on (Pacific time)')).toBeTruthy();
  });

  it('shows no time under a scheduled preview; the block holds it', () => {
    const at = '2099-11-28T18:00:00Z';
    mountView([post({ status: 'scheduled', scheduled_at: at }), post({ id: 2, channel: 'mastodon', body: mast, short_url: MURL, status: 'scheduled', scheduled_at: at })]);
    for (const a of screen.getAllByRole('article')) expect(a.querySelector('.status')).toBeNull();
    expect(document.body.textContent).not.toMatch(/Posts automatically/);
  });

  it('Reminder leads with Schedule…, Announce with Post', () => {
    mountView([post({ wave: 'reminder' }), post({ id: 2, wave: 'reminder', channel: 'mastodon', body: mast, short_url: MURL })], 'reminder');
    expect(screen.getByRole('button', { name: 'Schedule…' }).className).toMatch(/primary/);
    expect(screen.getByRole('button', { name: 'Post to Bluesky and Mastodon' }).className).not.toMatch(/primary/);
    cleanup();
    mountView([bPost(), mPost()]);
    expect(screen.getByRole('button', { name: 'Schedule…' }).className).not.toMatch(/primary/);
    expect(screen.getByRole('button', { name: 'Post to Bluesky and Mastodon' }).className).toMatch(/primary/);
  });

  it('the When it goes out block sits between Save message and How it will look', () => {
    mountView([bPost(), mPost()]);
    const save = screen.getByRole('button', { name: 'Save message' });
    const when = screen.getByRole('heading', { name: 'When it goes out' });
    const how = screen.getByRole('heading', { name: 'How it will look' });
    expect(save.compareDocumentPosition(when) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(when.compareDocumentPosition(how) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(document.querySelector('.when-line')!.textContent).toBe('Not scheduled. Bluesky and Mastodon go out together.');
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

describe('OutreachAnnouncement: what a finished section shows (#0626)', () => {
  const TIME = '2026-10-18T17:00:00Z';
  const live = (p: OutreachPost): OutreachPost => ({ ...p, status: 'posted', posted_at: TIME });
  const ig = { name: 'instagram', label: 'Instagram', utm_source: 'instagram', utm_medium: 'social', char_limit: 0, automatic: false, configured: false, account: '' };
  const igPost = () => post({ id: 3, channel: 'instagram', status: 'manual', body: 'Hand words' });
  const mountWith = (chs: WorkshopOutreach['channels'], posts: OutreachPost[]) =>
    render(OutreachAnnouncement, {
      view: {
        available: true, unavailable_reason: '',
        workshop: { id: 5, slug: 's', title: 'Soldering 101', status: 'published', starts_at: '2026-12-05T18:00:00Z' },
        outreach: { id: 9, workshop_id: 9, campaign_slug: 's', created_at: '' },
        channels: chs, waves, posts,
      },
      results: null, wave: 'announce', workshopId: 5, reload: vi.fn(), onChange: vi.fn(),
    });

  it('a fully posted section shows what was posted, with no Message box or Save message', () => {
    mountView([live(bPost()), live(mPost())]);
    expect(screen.queryByLabelText('Message')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Save message' })).toBeNull();
    expect(screen.getByRole('heading', { name: 'What was posted' })).toBeTruthy();
    expect(screen.queryByRole('heading', { name: 'How it will look' })).toBeNull();
    expect(screen.getByRole('article', { name: 'Bluesky Announce preview' }).textContent).toContain('Join us for Soldering 101.');
  });

  it('a partly posted section keeps the Message box', () => {
    mountView([live(bPost()), mPost()]);
    expect(screen.getByLabelText('Message')).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'How it will look' })).toBeTruthy();
  });

  it('customized drafts keep the Message box', () => {
    mountView([{ ...bPost(), customized: true }, { ...mPost(), customized: true }]);
    expect(screen.getByLabelText('Message')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Save message' })).toBeTruthy();
  });

  it('text typed before the section locked no longer counts as unsaved', async () => {
    const onUnsaved = vi.fn();
    const { rerender } = mountView([bPost(), mPost()], 'announce', onUnsaved);
    await type('Typed words');
    await waitFor(() => expect(onUnsaved.mock.lastCall?.[0]).toBe(true));
    await rerender([live(bPost()), live(mPost())]);
    await waitFor(() => expect(onUnsaved.mock.lastCall?.[0]).toBe(false));
    expect(screen.queryByText('Save the message before posting or scheduling.')).toBeNull();
  });

  it('with only hand-posted channels and a written post there is no Write prompt', () => {
    mountWith([ig], [igPost()]);
    expect(screen.queryByRole('button', { name: /^Write the/ })).toBeNull();
    expect(screen.queryByText(/No post/)).toBeNull();
  });

  it('with Bluesky connected but unwritten beside a hand post it says which channels have no post', () => {
    mountWith([channels[0], ig], [igPost()]);
    expect(screen.getByText('No post yet for Bluesky.')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Write the announcement' })).toBeTruthy();
  });
});

describe('OutreachAnnouncement with a switched-off channel', () => {
  const off = [{ name: 'mastodon', label: 'Mastodon', automatic: true }];
  const mountOff = (posts: OutreachPost[], extra: typeof channels = []) => render(OutreachAnnouncement, {
    view: {
      available: true, unavailable_reason: '',
      workshop: { id: 5, slug: 's', title: 'Soldering 101', status: 'published', starts_at: '2026-12-05T18:00:00Z' },
      outreach: { id: 9, workshop_id: 9, campaign_slug: 's', created_at: '' },
      channels: [...channels, ...extra], waves, posts, off_channels: off,
    } as WorkshopOutreach,
    results: null, wave: 'announce', workshopId: 5, reload: vi.fn(), onChange: vi.fn(),
  });

  it('does not say No post yet for an off channel (#0628)', () => {
    const instagram = { ...ch('instagram', 0, ''), label: 'Instagram', automatic: false, configured: false };
    const { container } = mountOff([post({ id: 3, channel: 'instagram', status: 'manual' })], [instagram]);
    expect(container.textContent).toContain('No post yet for Bluesky.');
    expect(container.textContent).not.toContain('Mastodon');
  });
  it('keeps Unschedule for a scheduled post on a disconnected channel (#0640)', () => {
    render(OutreachAnnouncement, {
      view: {
        available: true, unavailable_reason: '',
        workshop: { id: 5, slug: 's', title: 'Soldering 101', status: 'published', starts_at: '2026-12-05T18:00:00Z' },
        outreach: { id: 9, workshop_id: 9, campaign_slug: 's', created_at: '' },
        channels: [channels[0], { ...channels[1], configured: false }], waves,
        posts: [bPost(), post({ id: 2, channel: 'mastodon', body: mast, short_url: MURL, status: 'scheduled', scheduled_at: '2099-11-28T18:00:00Z' })],
      } as WorkshopOutreach,
      results: null, wave: 'announce', workshopId: 5, reload: vi.fn(), onChange: vi.fn(),
    });
    expect(screen.getByRole('button', { name: 'Unschedule' })).toBeTruthy();
  });
  it('keeps Unschedule on a scheduled post of an off channel (#0628)', () => {
    mountOff([post({ id: 2, channel: 'mastodon', body: mast, short_url: MURL, status: 'scheduled', scheduled_at: '2099-11-28T18:00:00Z' })]);
    expect(screen.getByRole('button', { name: 'Unschedule' })).toBeTruthy();
  });
});
