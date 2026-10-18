// @vitest-environment jsdom
//
// #0568: mounts OutreachBulkActions.svelte (and its confirm dialog) under
// jsdom with '../../lib/api' mocked. jsdom is not a browser (AGENTS.md §1):
// focus order and layout are approximations; this proves the calls, the
// preview-then-confirm contract and Escape handling.
import { render, cleanup, waitFor, screen, fireEvent, within } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { OutreachDraftAllResult, OutreachScheduleAllResult, WorkshopOutreach } from '../../lib/types';

type ScheduleBody = { dry_run: true } | { dry_run: false; post_ids: number[] };
const draftAllOutreach = vi.fn<(id: number, waves?: string[]) => Promise<OutreachDraftAllResult>>();
const scheduleAllOutreach = vi.fn<(id: number, body: ScheduleBody) => Promise<OutreachScheduleAllResult>>();
vi.mock('../../lib/api', () => ({
  draftAllOutreach: (id: number, waves?: string[]) => draftAllOutreach(id, waves),
  scheduleAllOutreach: (id: number, body: ScheduleBody) => scheduleAllOutreach(id, body),
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

const { default: OutreachBulkActions } = await import('./OutreachBulkActions.svelte');

afterEach(() => {
  cleanup();
  draftAllOutreach.mockReset();
  scheduleAllOutreach.mockReset();
});

function ch(name: string, automatic: boolean, configured: boolean) {
  return { name, label: name, utm_source: name, utm_medium: 'social', char_limit: 0, automatic, configured, account: '' };
}

function wo(over: Partial<WorkshopOutreach> = {}): WorkshopOutreach {
  return {
    available: true, unavailable_reason: '',
    workshop: { id: 5, slug: 'w', title: 'W', status: 'published', starts_at: '2026-12-05T18:00:00Z' },
    outreach: { id: 9, workshop_id: 5, campaign_slug: 'w', created_at: '' },
    channels: [ch('bluesky', true, true), ch('mastodon', true, false), ch('instagram', false, false)],
    waves: [], posts: [], ...over,
  };
}

const plan: OutreachScheduleAllResult = {
  dry_run: true,
  to_schedule: [{ post_id: 11, channel: 'bluesky', channel_label: 'Bluesky', wave: 'last-call', wave_label: 'Last call', scheduled_at: '2026-12-04T18:00:00Z' }],
  skipped: [{ post_id: 10, channel: 'bluesky', channel_label: 'Bluesky', wave: 'reminder', wave_label: 'Reminder', reason: 'time_passed', suggested_at: '2026-11-28T18:00:00Z' }],
};

describe('OutreachBulkActions', () => {
  it('has no Draft all button or Also draft checkboxes (#0582)', () => {
    render(OutreachBulkActions, { workshopId: 5, view: wo(), onDone: vi.fn() });
    expect(screen.queryByRole('button', { name: 'Draft all' })).toBeNull();
    expect(screen.queryByRole('checkbox', { name: 'Reminder' })).toBeNull();
    expect(screen.queryByRole('checkbox', { name: 'Last call' })).toBeNull();
    expect(screen.queryByText('Also draft')).toBeNull();
    expect(draftAllOutreach).not.toHaveBeenCalled();
  });

  it('Schedule all previews, lists scheduled and skipped, and Cancel sends nothing', async () => {
    scheduleAllOutreach.mockResolvedValue(plan);
    render(OutreachBulkActions, { workshopId: 5, view: wo(), onDone: vi.fn() });
    await fireEvent.click(screen.getByRole('button', { name: 'Schedule all' }));
    const dialog = await screen.findByRole('dialog');
    expect(scheduleAllOutreach).toHaveBeenCalledTimes(1);
    expect(scheduleAllOutreach).toHaveBeenCalledWith(5, { dry_run: true });
    expect(dialog.textContent).toContain('Bluesky · Last call');
    expect(dialog.textContent).toContain('Bluesky · Reminder');
    expect(dialog.textContent).toContain('time has passed');
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(scheduleAllOutreach).toHaveBeenCalledTimes(1);
  });

  it('Confirm sends exactly the previewed post ids with dry_run false', async () => {
    scheduleAllOutreach.mockResolvedValueOnce(plan).mockResolvedValueOnce({ dry_run: false, to_schedule: plan.to_schedule, skipped: [] });
    const onDone = vi.fn();
    render(OutreachBulkActions, { workshopId: 5, view: wo(), onDone });
    await fireEvent.click(screen.getByRole('button', { name: 'Schedule all' }));
    // Last call is opt-in: nothing is schedulable until it is ticked.
    expect((await screen.findByRole('button', { name: 'Schedule 0 posts' }) as HTMLButtonElement).disabled).toBe(true);
    await fireEvent.click(within(screen.getByRole('dialog')).getByRole('checkbox', { name: 'Last call' }));
    await fireEvent.click(await screen.findByRole('button', { name: 'Schedule 1 post' }));
    await waitFor(() => expect(onDone).toHaveBeenCalledTimes(1));
    expect(scheduleAllOutreach).toHaveBeenLastCalledWith(5, { dry_run: false, post_ids: [11] });
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(screen.getByRole('status').textContent).toBe('Scheduled 1 post.');
  });

  it('is disabled with a Settings link when no automatic channel is configured', () => {
    render(OutreachBulkActions, {
      workshopId: 5, onDone: vi.fn(),
      view: wo({ channels: [ch('bluesky', true, false), ch('instagram', false, false)] }),
    });
    expect((screen.getByRole('button', { name: 'Schedule all' }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByRole('link', { name: 'Open Settings' }).getAttribute('href')).toBe('#/settings?section=outreach');
  });

  it('points an unpublished workshop at the editor, not Settings', () => {
    render(OutreachBulkActions, {
      workshopId: 5, onDone: vi.fn(),
      view: wo({ workshop: { id: 5, slug: 'w', title: 'W', status: 'draft', starts_at: '2026-12-05T18:00:00Z' } }),
    });
    expect((screen.getByRole('button', { name: 'Schedule all' }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByRole('link', { name: 'Open the workshop editor' }).getAttribute('href')).toBe('#/workshops/5');
    expect(screen.queryByRole('link', { name: 'Open Settings' })).toBeNull();
  });

  it('Escape closes the dialog without scheduling', async () => {
    scheduleAllOutreach.mockResolvedValue(plan);
    render(OutreachBulkActions, { workshopId: 5, view: wo(), onDone: vi.fn() });
    await fireEvent.click(screen.getByRole('button', { name: 'Schedule all' }));
    const dialog = await screen.findByRole('dialog');
    await fireEvent.keyDown(dialog, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(scheduleAllOutreach).toHaveBeenCalledTimes(1);
  });
});
