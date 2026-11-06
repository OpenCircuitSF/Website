// @vitest-environment jsdom
//
// #0632: mounts OutreachSection.svelte (the real OutreachAnnouncement,
// OutreachPostCards, OutreachPostCard and OutreachComposer) under jsdom with
// '../../lib/api' mocked, to prove a hand-posted row's unsaved typing is not
// overwritten by the section's Save message. jsdom is not a browser (AGENTS.md §1).
import { render, cleanup, waitFor, screen, fireEvent, within } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { OutreachPost, WorkshopOutreach } from '../../lib/types';

const updateOutreachPost = vi.fn();
vi.mock('../../lib/api', () => ({
  updateOutreachPost: (id: number, b: unknown) => updateOutreachPost(id, b),
  sendOutreachPost: vi.fn(),
  draftAllOutreach: vi.fn(),
  scheduleOutreachSection: vi.fn(),
  unscheduleOutreachSection: vi.fn(),
  takedownOutreachPost: vi.fn(),
  createOutreachPost: vi.fn(),
  markOutreachPosted: vi.fn(),
  unmarkOutreachPosted: vi.fn(),
  prepareOutreachPost: vi.fn(),
  deleteOutreachPost: vi.fn(),
  ApiError: class ApiError extends Error {},
}));

const { default: OutreachSection } = await import('./OutreachSection.svelte');

afterEach(() => {
  cleanup();
  updateOutreachPost.mockReset();
});

const TT = 'https://go.example/u/tt';
function post(over: Partial<OutreachPost>): OutreachPost {
  return {
    id: 1, outreach_id: 9, channel: 'bluesky', wave: 'announce', body: 'Join us.\n\n#sf', status: 'draft', prepared: true, stale: false, customized: false, held: false,
    short_link_key: 'k', short_url: 'https://go.example/b', destination_url: 'https://x', utm: { source: '', medium: '', campaign: '', content: '' },
    scheduled_at: null, posted_at: null, remote_url: null, last_error: null, created_at: '', updated_at: null, ...over,
  };
}

describe('OutreachSection (#0632)', () => {
  it('a hand row with unsaved typing is not overwritten by Save message (#0632)', async () => {
    let current: WorkshopOutreach = {
      available: true, unavailable_reason: '',
      workshop: { id: 5, slug: 's', title: 'Soldering 101', status: 'published', starts_at: '2099-12-05T18:00:00Z' },
      outreach: { id: 9, workshop_id: 5, campaign_slug: 's', created_at: '' },
      channels: [
        { name: 'bluesky', label: 'Bluesky', utm_source: 'bluesky', utm_medium: 'social', char_limit: 300, automatic: true, configured: true, account: '' },
        { name: 'tiktok', label: 'TikTok', utm_source: 'tiktok', utm_medium: 'video', char_limit: 0, automatic: false, configured: false, account: '' },
      ],
      waves: [{ name: 'announce', label: 'Announce', suggested_at: null }],
      posts: [post({}), post({ id: 2, channel: 'tiktok', status: 'manual', body: `Join us.\n\nSign up: ${TT}\n\n#sf`, short_url: TT })],
    };
    const r = render(OutreachSection, {
      view: current, results: null, wave: { name: 'announce', label: 'Announce', suggested_at: null }, workshopId: 5, open: true, reload: vi.fn(),
      onChange: (p: OutreachPost) => {
        current = { ...current, posts: current.posts.map((x) => (x.id === p.id ? p : x)) };
        void r.rerender({ view: current });
      },
    });
    updateOutreachPost.mockImplementation((id: number, b: { body: string }) => Promise.resolve({ ...current.posts.find((x) => x.id === id)!, body: b.body }));
    const row = screen.getByRole('article', { name: 'TikTok Announce' });
    await fireEvent.click(within(row).getByRole('button', { name: 'Edit' }));
    const box = within(row).getByLabelText('Post text') as HTMLTextAreaElement;
    await fireEvent.input(box, { target: { value: 'Unsaved' } });
    await fireEvent.input(screen.getByLabelText('Message'), { target: { value: 'Fresh words.' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save message' }));
    const status = () => document.querySelector('.announcement > [role="status"]')?.textContent ?? '';
    await waitFor(() => expect(status()).toContain('TikTok'));
    expect(status()).toBe('Saved to Bluesky. TikTok has unsaved edits open, so it was not changed.');
    expect(updateOutreachPost.mock.calls.map((c) => c[0])).toEqual([1]);
    expect((within(screen.getByRole('article', { name: 'TikTok Announce' })).getByLabelText('Post text') as HTMLTextAreaElement).value).toBe('Unsaved');
  });
});
