// @vitest-environment jsdom
//
// #0624: mounts the status board under jsdom. jsdom is not a browser
// (AGENTS.md §1): structure and words are real, layout is not.
import { cleanup, fireEvent, render, screen, within } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import OutreachStatusBoard from './OutreachStatusBoard.svelte';
import { sectionPanelId, weekdayTime } from '../../lib/outreachCards';
import type { OutreachChannel, OutreachPost, WorkshopOutreach } from '../../lib/types';

afterEach(cleanup);

const TIME = '2026-11-20T18:00:00Z';
const ch = (name: string, label: string, automatic: boolean): OutreachChannel => ({
  name, label, utm_source: name, utm_medium: 'social', char_limit: 0, automatic, configured: automatic, account: '',
});
const channels = [ch('bluesky', 'Bluesky', true), ch('mastodon', 'Mastodon', true), ch('instagram', 'Instagram', false), ch('tiktok', 'TikTok', false), ch('youtube_shorts', 'YouTube Shorts', false)];
const waves = [
  { name: 'announce', label: 'Announce', suggested_at: null },
  { name: 'reminder', label: 'Reminder', suggested_at: null },
  { name: 'last-call', label: 'Final', suggested_at: null },
];
let nextId = 1;
function post(channel: string, wave: string, over: Partial<OutreachPost> = {}): OutreachPost {
  return {
    id: nextId++, outreach_id: 9, channel, wave, body: 'Hello', status: 'draft', prepared: true, stale: false, customized: false, held: false,
    short_link_key: 'k', short_url: 'https://go.example/k', destination_url: 'https://x', utm: { source: '', medium: '', campaign: '', content: '' },
    scheduled_at: null, posted_at: null, remote_url: null, last_error: null, created_at: '', updated_at: null, ...over,
  };
}
function view(posts: OutreachPost[]): WorkshopOutreach {
  return {
    available: true, unavailable_reason: '',
    workshop: { id: 5, slug: 's', title: 'S', status: 'published', starts_at: TIME },
    outreach: { id: 9, workshop_id: 5, campaign_slug: 's', created_at: '' },
    channels, waves, posts,
  } as WorkshopOutreach;
}
const cell = (c: HTMLElement, wave: string, channel: string) =>
  c.querySelector(`tr[data-wave="${wave}"] td[data-channel="${channel}"]`) as HTMLElement;

const mount = (v: WorkshopOutreach, over: Record<string, unknown> = {}) =>
  render(OutreachStatusBoard, { view: v, openWave: null, unsaved: {}, onSelect: vi.fn(), ...over });

describe('OutreachStatusBoard (#0624)', () => {
  it('has a row for each section and a column for each channel', () => {
    mount(view([]));
    const table = screen.getByRole('table', { name: 'Posts and when they go out' });
    expect(within(table).getAllByRole('rowheader')).toHaveLength(3);
    expect(within(table).getAllByRole('columnheader')).toHaveLength(6);
  });
  it("states each cell's channel, status and time in text", () => {
    const { container } = mount(view([post('bluesky', 'announce', { status: 'scheduled', scheduled_at: TIME })]));
    const text = cell(container, 'announce', 'bluesky').textContent ?? '';
    expect(text).toContain('Bluesky');
    expect(text).toContain('Scheduled');
    expect(text).toContain(weekdayTime(TIME));
  });
  it('gives each hand-posted platform its own state', () => {
    const { container } = mount(view([post('tiktok', 'announce', { status: 'posted', posted_at: TIME }), post('youtube_shorts', 'announce', { status: 'manual' })]));
    expect(cell(container, 'announce', 'tiktok').querySelector('.board-status')?.textContent).toBe('Posted');
    expect(cell(container, 'announce', 'youtube_shorts').querySelector('.board-status')?.textContent).toBe('Post by hand');
  });
  it('says 1 not scheduled on a partly scheduled row and nothing on a fully scheduled one', () => {
    const sched = { status: 'scheduled', scheduled_at: TIME } as const;
    const { container } = mount(view([post('bluesky', 'reminder', sched), post('mastodon', 'reminder'), post('bluesky', 'announce', sched), post('mastodon', 'announce', sched)]));
    expect(container.querySelector('tr[data-wave="reminder"] .board-note')?.textContent).toBe('1 not scheduled');
    expect(container.querySelector('tr[data-wave="announce"] .board-note')).toBeNull();
  });
  it('labels the third row Final', () => {
    const { container } = mount(view([]));
    const row = container.querySelectorAll('tbody tr')[2];
    expect(row.querySelector('.board-ordinal')?.textContent).toBe('3');
    expect(row.querySelector('.board-label')?.textContent).toBe('Final');
  });
  it('changes when the view changes', async () => {
    const { container, rerender } = mount(view([]));
    expect(cell(container, 'announce', 'bluesky').querySelector('.board-status')?.textContent).toBe('Not written');
    await rerender({ view: view([post('bluesky', 'announce', { status: 'posted', posted_at: TIME })]) });
    expect(cell(container, 'announce', 'bluesky').querySelector('.board-status')?.textContent).toBe('Posted');
  });
  it('gives every cell a status in words, whatever its kind', () => {
    const { container } = mount(view([
        post('bluesky', 'announce', { status: 'posted', posted_at: TIME }),
        post('mastodon', 'announce', { status: 'scheduled', scheduled_at: TIME }),
        post('bluesky', 'reminder', { status: 'scheduled', scheduled_at: TIME, held: true }),
        post('mastodon', 'reminder'),
        post('bluesky', 'last-call', { status: 'failed' }),
        post('mastodon', 'last-call', { status: 'queued' }),
        post('instagram', 'announce', { status: 'manual' }),
      ]));
    const kinds = new Set<string>();
    const cells = container.querySelectorAll<HTMLElement>('td[data-kind]');
    expect(cells).toHaveLength(15);
    for (const td of cells) {
      kinds.add(td.dataset.kind as string);
      expect(td.querySelector('.board-status')?.textContent?.trim()).toBeTruthy();
    }
    expect([...kinds].sort()).toEqual(['failed', 'manual', 'not_drafted', 'not_scheduled', 'on_hold', 'posted', 'queued', 'scheduled']);
  });

  it('each row head holds a native button named by its number and name only (#0625)', () => {
    mount(view([post('bluesky', 'reminder', { body: 'Excerpt words here' })]));
    const btn = screen.getByRole('button', { name: /Reminder/ });
    expect(btn.tagName).toBe('BUTTON');
    expect(btn.closest('th')).not.toBeNull();
    expect(btn.textContent).not.toContain('Excerpt');
    expect(btn.closest('th')?.textContent).toContain('Excerpt');
  });
  it('selecting a row reports that section (#0625)', async () => {
    const onSelect = vi.fn();
    mount(view([]), { onSelect });
    await fireEvent.click(screen.getByRole('button', { name: /Final/ }));
    expect(onSelect).toHaveBeenCalledWith('last-call');
  });
  it('marks only the open row with aria-current and the open marker (#0625)', () => {
    const { container } = mount(view([]), { openWave: 'reminder' });
    expect(container.querySelectorAll('[aria-current]')).toHaveLength(1);
    expect(screen.getByRole('button', { name: /Reminder/ }).getAttribute('aria-current')).toBe('true');
    expect(container.querySelectorAll('tr.board-open')).toHaveLength(1);
    expect(container.querySelector('tr.board-open')?.getAttribute('data-wave')).toBe('reminder');
    expect(container.querySelector('tr[data-wave="reminder"] .board-chevron')?.textContent).toBe('▾');
    expect(container.querySelector('tr[data-wave="announce"] .board-chevron')?.textContent).toBe('▸');
  });
  it('says Not saved yet on a row with unsaved changes (#0625)', () => {
    const { container } = mount(view([]), { unsaved: { reminder: true, announce: false } });
    expect(container.querySelector('tr[data-wave="reminder"] .board-row-head')?.textContent).toContain('Not saved yet');
    expect(container.querySelector('tr[data-wave="announce"] .board-row-head')?.textContent).not.toContain('Not saved yet');
  });
  it('the row buttons control their section panels (#0625)', () => {
    mount(view([]));
    expect(screen.getByRole('button', { name: /Reminder/ }).getAttribute('aria-controls')).toBe(sectionPanelId('reminder'));
  });
});

describe('OutreachStatusBoard with a switched-off channel', () => {
  const offOf = (...names: string[]) => channels.filter((c) => names.includes(c.name)).map(({ name, label, automatic }) => ({ name, label, automatic }));
  const onlyWith = (off: string[], kept: string[], posts: OutreachPost[]): WorkshopOutreach => ({
    ...view(posts), channels: channels.filter((c) => !off.includes(c.name) || kept.includes(c.name)), off_channels: offOf(...off),
  });
  const line = (c: HTMLElement) => c.querySelector('.board-off');

  it('shows an off channel with no post in the line and not as a column (#0628)', () => {
    const { container } = mount(onlyWith(['youtube_shorts'], [], []));
    expect(line(container)!.textContent).toContain('YouTube Shorts is off in Settings, so new posts and links skip it.');
    expect(screen.queryByRole('columnheader', { name: /YouTube/ })).toBeNull();
    expect(cell(container, 'announce', 'youtube_shorts')).toBeNull();
  });
  it('heads an off channel with a post as off and fills its empty slots with Off (#0628)', () => {
    const { container } = mount(onlyWith(['tiktok'], ['tiktok'], [post('tiktok', 'announce', { status: 'posted', posted_at: TIME })]));
    expect(screen.getByRole('columnheader', { name: 'TikTok (off)' })).toBeTruthy();
    const empty = cell(container, 'reminder', 'tiktok');
    expect(empty.dataset.kind).toBe('off');
    expect(empty.querySelector('.board-status')!.textContent).toBe('Off');
    expect(empty.querySelector('.board-channel')!.textContent).toBe('TikTok (off)');
    expect(container.textContent).toContain('Not written');
    expect(empty.textContent).not.toContain('Not written');
  });
  it('keeps Posted and Scheduled with their times on an off channel (#0628)', () => {
    const { container } = mount(onlyWith(['bluesky'], ['bluesky'], [
      post('bluesky', 'announce', { status: 'posted', posted_at: TIME }),
      post('bluesky', 'reminder', { status: 'scheduled', scheduled_at: TIME }),
    ]));
    const posted = cell(container, 'announce', 'bluesky');
    expect(posted.querySelector('.board-status')!.textContent).toBe('Posted');
    expect(posted.querySelector('.board-detail')!.textContent).toBe(weekdayTime(TIME));
    const scheduled = cell(container, 'reminder', 'bluesky');
    expect(scheduled.querySelector('.board-status')!.textContent).toBe('Scheduled');
    expect(scheduled.querySelector('.board-detail')!.textContent).toBe(weekdayTime(TIME));
    expect(line(container)!.textContent).toContain('Posts already scheduled there still go out.');
  });
  it('links the line to the Outreach part of Settings (#0628)', () => {
    mount(onlyWith(['tiktok'], [], []));
    expect(screen.getByRole('link', { name: 'Open Settings' }).getAttribute('href')).toBe('#/settings?section=outreach');
  });
  it('shows no line when every channel is on (#0628)', () => {
    const { container } = mount({ ...view([]), off_channels: [] });
    expect(line(container)).toBeNull();
    expect(screen.queryByRole('link', { name: 'Open Settings' })).toBeNull();
  });
  it('still explains itself when every channel is off (#0628)', () => {
    const { container } = mount({ ...view([]), channels: [], off_channels: offOf(...channels.map((c) => c.name)) });
    expect(line(container)!.textContent).toContain('Bluesky, Mastodon, Instagram, TikTok and YouTube Shorts are off in Settings');
    expect(screen.getByRole('link', { name: 'Open Settings' })).toBeTruthy();
  });
});
