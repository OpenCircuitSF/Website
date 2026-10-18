// @vitest-environment jsdom
//
// #0582: mounts OutreachWaveRow.svelte (with the real OutreachAnnouncement)
// under jsdom with '../../lib/api' mocked. jsdom is not a browser (AGENTS.md
// section 1).
import { render, cleanup, waitFor, screen, fireEvent } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { OutreachPost, WorkshopOutreach } from '../../lib/types';

const draftAllOutreach = vi.fn();
vi.mock('../../lib/api', () => ({
  draftAllOutreach: (id: number, w: string[]) => draftAllOutreach(id, w),
  updateOutreachPost: vi.fn(),
  sendOutreachPost: vi.fn(),
  scheduleOutreachPost: vi.fn(),
  unscheduleOutreachPost: vi.fn(),
  prepareOutreachPost: vi.fn(),
  deleteOutreachPost: vi.fn(),
  ApiError: class ApiError extends Error {},
}));

const { default: OutreachWaveRow } = await import('./OutreachWaveRow.svelte');

afterEach(() => {
  cleanup();
  draftAllOutreach.mockReset();
});

const bluesky = { name: 'bluesky', label: 'Bluesky', utm_source: 'bluesky', utm_medium: 'social', char_limit: 300, automatic: true, configured: true, account: '' };
const wave = { name: 'reminder', label: 'Reminder', suggested_at: '2099-11-28T18:00:00Z' };

function view(posts: OutreachPost[]): WorkshopOutreach {
  return {
    available: true, unavailable_reason: '',
    workshop: { id: 5, slug: 's', title: 'W', status: 'published', starts_at: '2026-12-05T18:00:00Z' },
    outreach: { id: 9, workshop_id: 5, campaign_slug: 's', created_at: '' },
    channels: [bluesky], waves: [wave], posts,
  };
}
const reminderPost = (): OutreachPost => ({
  id: 3, outreach_id: 9, channel: 'bluesky', wave: 'reminder', body: 'Reminder words\n\n#sf', status: 'draft', prepared: true, stale: false, customized: false, held: false,
  short_link_key: 'k', short_url: 'https://go.example/r', destination_url: 'https://x', utm: { source: '', medium: '', campaign: '', content: '' },
  scheduled_at: null, posted_at: null, remote_url: null, last_error: null, created_at: '', updated_at: null,
});

describe('OutreachWaveRow', () => {
  it('is collapsed with the label, suggested time and status in the summary; Write it is beside it', () => {
    render(OutreachWaveRow, { view: view([]), results: null, wave, workshopId: 5, onChange: vi.fn(), reload: vi.fn() });
    const details = screen.getByText('Reminder', { selector: 'strong' }).closest('details')!;
    expect(details.hasAttribute('open')).toBe(false);
    expect(details.querySelector('summary')!.textContent).toMatch(/Not written/);
    expect(details.querySelector('summary')!.textContent).toMatch(/Sat|Sun|Mon|Tue|Wed|Thu|Fri/);
    const write = screen.getByRole('button', { name: 'Write it' });
    expect(details.contains(write)).toBe(false);
  });

  it('Write it drafts just this wave and opens the row', async () => {
    const reload = vi.fn();
    draftAllOutreach.mockResolvedValue({ created: [], skipped: 0, without_link: 0 });
    render(OutreachWaveRow, { view: view([]), results: null, wave, workshopId: 5, onChange: vi.fn(), reload });
    await fireEvent.click(screen.getByRole('button', { name: 'Write it' }));
    await waitFor(() => expect(draftAllOutreach).toHaveBeenCalledWith(5, ['reminder']));
    expect(reload).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(screen.getByText('Reminder', { selector: 'strong' }).closest('details')!.hasAttribute('open')).toBe(true));
  });

  it('a written wave has no Write it and shows its message section when opened', () => {
    render(OutreachWaveRow, { view: view([reminderPost()]), results: null, wave, workshopId: 5, onChange: vi.fn(), reload: vi.fn() });
    expect(screen.queryByRole('button', { name: 'Write it' })).toBeNull();
    expect(screen.getByText('Drafted · not scheduled', { exact: false })).toBeTruthy();
    expect((screen.getByLabelText('Message') as HTMLTextAreaElement).value).toBe('Reminder words');
  });
});
