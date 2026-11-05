// @vitest-environment jsdom
//
// #0640: mounts OutreachSectionTabs.svelte under jsdom. jsdom is not a browser
// (AGENTS.md §1): roles, attributes and events are real, layout is not.
import { render, cleanup, screen, fireEvent, within } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { OutreachPost, WorkshopOutreach } from '../../lib/types';
import OutreachSectionTabs from './OutreachSectionTabs.svelte';

afterEach(() => cleanup());

const channels = [
  { name: 'bluesky', label: 'Bluesky', utm_source: 'bluesky', utm_medium: 'social', char_limit: 300, automatic: true, configured: true, account: '' },
];
const waves = [
  { name: 'announce', label: 'Announce', suggested_at: null },
  { name: 'reminder', label: 'Reminder', suggested_at: null },
  { name: 'last-call', label: 'Final', suggested_at: null },
];
const AT = '2099-11-20T18:00:00Z';
const scheduled = {
  id: 1, outreach_id: 9, channel: 'bluesky', wave: 'reminder', body: 'Hi', status: 'scheduled', prepared: true, stale: false, customized: false, held: false,
  short_link_key: 'k', short_url: 'https://go.example/k', destination_url: 'https://x', utm: { source: '', medium: '', campaign: '', content: '' },
  scheduled_at: AT, posted_at: null, remote_url: null, last_error: null, created_at: '', updated_at: null,
} as OutreachPost;
const view: WorkshopOutreach = {
  available: true, unavailable_reason: '',
  workshop: { id: 5, slug: 's', title: 'S', status: 'published', starts_at: '2099-12-05T18:00:00Z' },
  outreach: { id: 9, workshop_id: 5, campaign_slug: 's', created_at: '' },
  channels, waves, posts: [scheduled],
};

function mount(selected = 'announce', unsaved: Record<string, boolean> = {}) {
  const onSelect = vi.fn();
  const r = render(OutreachSectionTabs, { view, selected, unsaved, onSelect });
  return { ...r, onSelect };
}
const tabs = () => within(screen.getByRole('tablist', { name: 'Section' })).getAllByRole('tab');

describe('OutreachSectionTabs', () => {
  it('is a tablist with one tab per section, named by number and name', () => {
    mount();
    expect(tabs().map((t) => t.textContent?.replace(/\s+/g, ' ').trim())).toEqual(['1 Announce', '2 Reminder', '3 Final']);
  });

  it('marks only the selected tab aria-selected and points it at its panel', () => {
    mount('reminder');
    const t = tabs();
    expect(t.map((x) => x.getAttribute('aria-selected'))).toEqual(['false', 'true', 'false']);
    expect(t[1].getAttribute('aria-controls')).toBe('outreach-section-reminder-panel');
    expect(t[1].id).toBe('outreach-section-reminder-head');
    expect(t.map((x) => x.tabIndex)).toEqual([-1, 0, -1]);
  });

  it('selecting a tab reports that section', async () => {
    const { onSelect } = mount();
    await fireEvent.click(tabs()[2]);
    expect(onSelect).toHaveBeenCalledWith('last-call');
  });

  it('arrow keys move between tabs', async () => {
    const { onSelect } = mount('reminder');
    await fireEvent.keyDown(tabs()[1], { key: 'ArrowRight' });
    expect(onSelect).toHaveBeenLastCalledWith('last-call');
    await fireEvent.keyDown(tabs()[1], { key: 'ArrowLeft' });
    expect(onSelect).toHaveBeenLastCalledWith('announce');
    await fireEvent.keyDown(tabs()[1], { key: 'End' });
    expect(onSelect).toHaveBeenLastCalledWith('last-call');
    await fireEvent.keyDown(tabs()[1], { key: 'Home' });
    expect(onSelect).toHaveBeenLastCalledWith('announce');
  });

  it("shows the selected section's line under the tabs, with PT kept on its line", () => {
    const { container } = mount('reminder');
    const line = container.querySelector('.section-line') as HTMLElement;
    expect(line.textContent).toMatch(/Scheduled/);
    const when = line.querySelector('.when') as HTMLElement;
    expect(when.textContent).toMatch(/ PT$/);
  });

  it('says not saved on a tab with unsaved typing (#0625)', () => {
    mount('announce', { reminder: true });
    const t = tabs();
    expect(t[1].textContent).toContain('not saved');
    expect(t[0].textContent).not.toContain('not saved');
  });
});
