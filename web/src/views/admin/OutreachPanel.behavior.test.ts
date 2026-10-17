// @vitest-environment jsdom
//
// #0544: mounts OutreachPanel.svelte under jsdom with '../../lib/api' mocked,
// following Pending.behavior.test.ts. jsdom is not a browser (CLAUDE.md §1):
// this proves the panel renders what the payload says and wires the right
// calls, not layout or clipboard permissions. navigator.clipboard does not
// exist under jsdom, so it is stubbed.
import { render, cleanup, waitFor, screen, fireEvent } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { OutreachPost, WorkshopOutreach } from '../../lib/types';

const getWorkshopOutreach = vi.fn<(id: number) => Promise<WorkshopOutreach>>();
const startOutreach = vi.fn<(id: number) => Promise<WorkshopOutreach>>();
vi.mock('../../lib/api', () => ({
  getWorkshopOutreach: (id: number) => getWorkshopOutreach(id),
  startOutreach: (id: number) => startOutreach(id),
  createOutreachPost: vi.fn(),
  updateOutreachPost: vi.fn(),
  prepareOutreachPost: vi.fn(),
  markOutreachPosted: vi.fn(),
  deleteOutreachPost: vi.fn(),
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

const { default: OutreachPanel } = await import('./OutreachPanel.svelte');

afterEach(() => {
  cleanup();
  getWorkshopOutreach.mockReset();
  startOutreach.mockReset();
});

const channels = [
  { name: 'bluesky', label: 'Bluesky', utm_source: 'bluesky', utm_medium: 'social', char_limit: 300, automatic: true },
  { name: 'mastodon', label: 'Mastodon', utm_source: 'mastodon', utm_medium: 'social', char_limit: 500, automatic: true },
  { name: 'instagram', label: 'Instagram', utm_source: 'instagram', utm_medium: 'social', char_limit: 0, automatic: false },
  { name: 'tiktok', label: 'TikTok', utm_source: 'tiktok', utm_medium: 'video', char_limit: 0, automatic: false },
  { name: 'youtube', label: 'YouTube', utm_source: 'youtube', utm_medium: 'video', char_limit: 0, automatic: false },
];
const waves = [
  { name: 'announce', label: 'Announce', suggested_at: null },
  { name: 'reminder', label: 'Reminder', suggested_at: '2026-11-28T18:00:00Z' },
  { name: 'last-call', label: 'Last call', suggested_at: '2026-12-04T18:00:00Z' },
];

function view(over: Partial<WorkshopOutreach> = {}): WorkshopOutreach {
  return {
    available: true,
    unavailable_reason: '',
    workshop: { id: 5, slug: 'soldering-101', title: 'Soldering 101', status: 'published', starts_at: '2026-12-05T18:00:00Z' },
    outreach: { id: 9, workshop_id: 5, campaign_slug: 'soldering-101', created_at: '2026-10-01T00:00:00Z' },
    channels,
    waves,
    posts: [],
    ...over,
  };
}

function manualPost(): OutreachPost {
  return {
    id: 3, outreach_id: 9, channel: 'instagram', wave: 'announce', body: 'Hello https://go.example/k1', status: 'manual',
    prepared: true, stale: false, short_link_key: 'k1', short_url: 'https://go.example/k1',
    destination_url: 'https://site/workshops/soldering-101?utm_source=instagram',
    utm: { source: 'instagram', medium: 'social', campaign: 'soldering-101', content: 'announce' },
    scheduled_at: null, posted_at: null, remote_url: null, last_error: null, created_at: '2026-10-01T00:00:00Z', updated_at: null,
  };
}

describe('OutreachPanel', () => {
  it('explains when ShortLinks is not configured', async () => {
    getWorkshopOutreach.mockResolvedValue(
      view({ available: false, unavailable_reason: 'shortlinks_not_configured', outreach: null }),
    );
    render(OutreachPanel, { workshopId: 5, workshopUpdatedAt: 'a' });
    await waitFor(() => expect(screen.getByText(/SHORTLINKS_BASE_URL/)).toBeTruthy());
    expect(screen.queryByText('Start outreach')).toBeNull();
  });

  it('offers Start outreach, then renders a 5 x 3 grid of cells', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ outreach: null }));
    startOutreach.mockResolvedValue(view());
    render(OutreachPanel, { workshopId: 5, workshopUpdatedAt: 'a' });
    const start = await screen.findByText('Start outreach');
    await fireEvent.click(start);
    await waitFor(() => expect(screen.getAllByText('Not drafted').length).toBe(15));
    expect(startOutreach).toHaveBeenCalledWith(5);
  });

  it('shows Copy content and Mark posted for a prepared manual post', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ posts: [manualPost()] }));
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    render(OutreachPanel, { workshopId: 5, workshopUpdatedAt: 'a' });
    await fireEvent.click(await screen.findByText('Ready to post'));
    const copy = await screen.findByText('Copy content');
    expect(screen.getByText('Mark posted')).toBeTruthy();
    expect(screen.getByText('https://go.example/k1', { selector: 'p' })).toBeTruthy();
    await fireEvent.click(copy);
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('Hello https://go.example/k1'));
  });
});
