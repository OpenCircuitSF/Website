// @vitest-environment jsdom
//
// #0646: the Outreach home timeline, mounted under jsdom. jsdom is not a
// browser (AGENTS.md §1): this proves DOM content and links, not layout.
import { render, cleanup, screen, within } from '@testing-library/svelte';
import { afterEach, describe, expect, it } from 'vitest';
import type { OutreachChannel, OutreachPost, OutreachTimeline as Timeline, OutreachTimelineEvent } from '../../lib/types';

const { default: OutreachTimeline } = await import('./OutreachTimeline.svelte');

afterEach(cleanup);

const ch = (name: string, label: string, automatic: boolean): OutreachChannel => ({
  name, label, utm_source: name, utm_medium: 'social', char_limit: 0, automatic, configured: automatic, account: '',
});
const waves = [
  { name: 'announce', label: 'Announce', suggested_at: null },
  { name: 'reminder', label: 'Reminder', suggested_at: '2026-11-13T18:00:00Z' },
];
function post(channel: string, wave: string, over: Partial<OutreachPost> = {}): OutreachPost {
  return {
    id: 1, outreach_id: 1, channel, wave, body: 'Hello', status: 'draft', prepared: true, stale: false, customized: false, held: false,
    short_link_key: 'k', short_url: 'https://go.example/k', destination_url: 'https://x', utm: { source: '', medium: '', campaign: '', content: '' },
    scheduled_at: null, posted_at: null, remote_url: null, last_error: null, created_at: '', updated_at: null, ...over,
  };
}
const started: OutreachTimelineEvent = {
  workshop: { id: 7, slug: 'led', title: 'LED Programming', status: 'published', starts_at: '2026-11-20T03:00:00Z' },
  outreach: { id: 1, workshop_id: 7, campaign_slug: 'led', created_at: '' },
  channels: [ch('bluesky', 'Bluesky', true), ch('instagram', 'Instagram', false)],
  waves,
  posts: [post('bluesky', 'announce', { status: 'posted', posted_at: '2026-11-07T18:00:00Z' })],
  off_channels: [],
};
const notStarted: OutreachTimelineEvent = {
  workshop: { id: 8, slug: 'hack', title: 'Hack Night 2', status: 'published', starts_at: '2026-11-25T03:00:00Z' },
  outreach: null, channels: [ch('bluesky', 'Bluesky', true)], waves, posts: [], off_channels: [],
};
const timeline: Timeline = { now: '2026-11-06T18:00:00Z', events: [started, notStarted] };

describe('OutreachTimeline', () => {
  it('renders a lane per upcoming event with its channel rows and markers', () => {
    const { container } = render(OutreachTimeline, { timeline });
    const lanes = container.querySelectorAll('section.lane');
    expect(lanes).toHaveLength(2);
    expect(Array.from(lanes[0].querySelectorAll('.row-label')).map((e) => e.textContent)).toEqual(['Bluesky', 'Instagram Reels']);
    expect(lanes[0].querySelector('.pill.posted')?.textContent).toBe('Announce');
    expect(screen.getByText('1 posted')).toBeTruthy();
  });

  it('an event with no outreach says Outreach not started and offers Promote', () => {
    render(OutreachTimeline, { timeline });
    const lane = screen.getByRole('region', { name: 'Hack Night 2' });
    expect(within(lane).getByRole('link', { name: 'Outreach: Outreach not started' }).getAttribute('href')).toBe('#/outreach?view=workshop&workshop=8');
    expect(within(lane).getByRole('link', { name: 'Promote Hack Night 2' })).toBeTruthy();
    expect(lane.classList.contains('not-started')).toBe(true);
  });

  it('gap lines and markers link to the event section', () => {
    const { container } = render(OutreachTimeline, { timeline });
    const lane = screen.getByRole('region', { name: 'LED Programming' });
    expect(within(lane).getByRole('link', { name: /^Reminder: / }).getAttribute('href')).toBe('#/outreach?view=workshop&workshop=7&section=reminder');
    const pill = container.querySelector('section.lane .pill.missing') as HTMLAnchorElement;
    expect(pill.getAttribute('href')).toMatch(/workshop=7&section=/);
    expect(pill.getAttribute('title')).toContain('LED Programming: ');
  });

  it('the track is hidden from assistive tech and its links are out of the tab order', () => {
    const { container } = render(OutreachTimeline, { timeline });
    for (const rows of container.querySelectorAll('.rows')) expect(rows.getAttribute('aria-hidden')).toBe('true');
    const pills = container.querySelectorAll('.rows a');
    expect(pills.length).toBeGreaterThan(0);
    for (const a of pills) expect(a.getAttribute('tabindex')).toBe('-1');
    expect(screen.getByRole('table', { name: 'LED Programming' })).toBeTruthy();
  });

  it('names hand-posted channels left to post', () => {
    render(OutreachTimeline, { timeline });
    const lane = screen.getByRole('region', { name: 'LED Programming' });
    expect(within(lane).getAllByText(/Left to post by hand: Instagram Reels/)).toHaveLength(2);
  });
});
