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
  const props = (v: WorkshopOutreach, over: Record<string, unknown> = {}) => ({
    view: v, results: null, wave, workshopId: 5, open: false, onSelect: vi.fn(), onChange: vi.fn(), reload: vi.fn(), ...over,
  });
  const rowOf = (w: typeof wave, posts: OutreachPost[]) => {
    const { container } = render(OutreachWaveRow, props({ ...view(posts), waves: [{ name: 'announce', label: 'Announce', suggested_at: null }, w] }, { wave: w }));
    return container.querySelector('.wave-row')!;
  };
  const panelOf = () => document.getElementById('outreach-section-reminder-panel')!;
  const heading = () => screen.getByRole('heading', { level: 3 });

  it('shows the ordinal, label and status in the row', () => {
    const row = rowOf(wave, []);
    expect(row.querySelector('.wave-pin')!.contains(row.querySelector('h3'))).toBe(true);
    expect(row.querySelector('.ordinal')!.textContent).toBe('2');
    expect(row.querySelector('.name')!.textContent).toBe('Reminder');
    expect(row.querySelector('.timing')!.textContent).toMatch(/^Not written · not scheduled · suggested /);
  });

  it('shows no suggested date in the row once scheduled', () => {
    const text = rowOf(wave, [{ ...reminderPost(), status: 'scheduled', scheduled_at: '2099-11-20T18:00:00Z' }]).querySelector('h3')!.textContent;
    expect(text).toMatch(/Posts automatically/);
    expect(text).not.toMatch(/suggested/);
    expect(text).not.toMatch(/Nov 28/);
  });

  it('shows no past suggested date', () => {
    const row = rowOf({ ...wave, suggested_at: '2020-01-01T18:00:00Z' }, []);
    expect(row.querySelector('.timing')!.textContent).toBe('Not written · not scheduled');
    expect(row.textContent).not.toMatch(/2020|Jan/);
  });

  it('is collapsed with the label, suggested time and status in the heading; Write it is beside it', () => {
    render(OutreachWaveRow, props(view([])));
    expect(panelOf().hidden).toBe(true);
    expect(heading().textContent).toMatch(/Not written/);
    expect(heading().textContent).toMatch(/Sat|Sun|Mon|Tue|Wed|Thu|Fri/);
    expect(heading().contains(screen.getByRole('button', { name: 'Write it' }))).toBe(false);
  });

  it('Write it drafts just this wave and selects the row', async () => {
    const reload = vi.fn();
    const onSelect = vi.fn();
    draftAllOutreach.mockResolvedValue({ created: [], skipped: 0, without_link: 0 });
    render(OutreachWaveRow, props(view([]), { reload, onSelect }));
    await fireEvent.click(screen.getByRole('button', { name: 'Write it' }));
    await waitFor(() => expect(draftAllOutreach).toHaveBeenCalledWith(5, ['reminder']));
    await waitFor(() => expect(onSelect).toHaveBeenCalledTimes(1));
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it('a written wave has no Write it and shows its message section when opened', () => {
    render(OutreachWaveRow, props(view([reminderPost()]), { open: true }));
    expect(screen.queryByRole('button', { name: 'Write it' })).toBeNull();
    expect(screen.getByText('Drafted · not scheduled', { exact: false })).toBeTruthy();
    expect((screen.getByLabelText('Message') as HTMLTextAreaElement).value).toBe('Reminder words');
  });

  it('is a heading holding a native button that controls its panel (#0625)', () => {
    render(OutreachWaveRow, props(view([])));
    const btn = heading().querySelector('button')!;
    expect(btn.tagName).toBe('BUTTON');
    expect(btn.getAttribute('type')).toBe('button');
    expect(btn.hasAttribute('tabindex')).toBe(false);
    expect(btn.getAttribute('aria-controls')).toBe(panelOf().id);
  });

  it('a closed row says collapsed, hides its panel, and selects when activated (#0625)', async () => {
    const onSelect = vi.fn();
    render(OutreachWaveRow, props(view([]), { onSelect }));
    const btn = heading().querySelector('button')!;
    expect(btn.getAttribute('aria-expanded')).toBe('false');
    expect(btn.hasAttribute('aria-disabled')).toBe(false);
    expect(panelOf().hidden).toBe(true);
    await fireEvent.click(btn);
    expect(onSelect).toHaveBeenCalledTimes(1);
  });

  it('an open row says expanded, is aria-disabled, shows its panel, and activating it does nothing (#0625)', async () => {
    const onSelect = vi.fn();
    render(OutreachWaveRow, props(view([reminderPost()]), { open: true, onSelect }));
    const btn = heading().querySelector('button')!;
    expect(btn.getAttribute('aria-expanded')).toBe('true');
    expect(btn.getAttribute('aria-disabled')).toBe('true');
    expect(panelOf().hidden).toBe(false);
    await fireEvent.click(btn);
    expect(onSelect).not.toHaveBeenCalled();
  });

  it('the screen-reader name includes the number, the name and the status (#0625)', () => {
    render(OutreachWaveRow, props(view([])));
    expect(screen.getByRole('button', { name: /1.*Reminder.*Not written/ })).toBeTruthy();
  });

  it('a closed panel keeps its message field mounted (#0625)', () => {
    render(OutreachWaveRow, props(view([reminderPost()])));
    expect(panelOf().hidden).toBe(true);
    expect(panelOf().querySelector('textarea')).not.toBeNull();
  });

  it('says changes are not saved only while closed with unsaved typing (#0625)', async () => {
    const onUnsavedChange = vi.fn();
    const { container, rerender } = render(OutreachWaveRow, props(view([reminderPost()]), { open: true, onUnsavedChange }));
    const note = container.querySelector('.unsaved-note')!;
    expect(note.getAttribute('role')).toBe('status');
    await fireEvent.input(screen.getByLabelText('Message'), { target: { value: 'Changed words' } });
    expect(onUnsavedChange).toHaveBeenLastCalledWith(true);
    expect(note.textContent).toBe('');
    await rerender({ open: false });
    expect(container.querySelector('.unsaved-note')).toBe(note);
    expect(note.textContent).toBe('Your changes here are not saved yet. Open this section to save them.');
  });
});
