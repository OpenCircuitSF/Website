// @vitest-environment jsdom
//
// #0586: the Outreach landing list, the Workshops Outreach column, the
// Outreach sub-nav, the editor summary's button label and the Overview's
// outreach needs-attention rows, mounted under jsdom with the API mocked.
// jsdom is not a browser (AGENTS.md §1): this proves DOM content, not layout.
import { render, cleanup, waitFor, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { AdminWorkshop, OutreachSummary, WorkshopOutreach, ConnectionGroup } from '../../lib/types';

const future = (d: number) => new Date(Date.now() + d * 86400000).toISOString();
function ws(id: number, over: Partial<AdminWorkshop> = {}): AdminWorkshop {
  return { event_type_id: 1, venue_id: null, event_type: { slug: 'workshop', name: 'Workshop', sort_order: 10 }, id, slug: `w${id}`, title: `Workshop ${id}`, status: 'published', starts_at: future(10 + id), interest_ids: [], created_at: '', updated_at: '', ...over };
}
function sum(id: number, counts: Record<string, number>): OutreachSummary {
  return { outreach_id: id, workshop_id: id, workshop_slug: '', workshop_title: '', workshop_status: 'published', campaign_slug: 'c', starts_at: null, counts, created_at: '' };
}

const listOutreach = vi.fn<() => Promise<{ outreach: OutreachSummary[] }>>();
const listAdminWorkshops = vi.fn<() => Promise<{ workshops: AdminWorkshop[] }>>();
const getWorkshopOutreach = vi.fn<(id: number) => Promise<WorkshopOutreach>>();
const getOutreachConnections = vi.fn<() => Promise<{ groups: ConnectionGroup[] }>>();
const getOverview = vi.fn();
const getOutreachTimeline = vi.fn(() => Promise.resolve({ now: '2026-11-06T18:00:00Z', events: [] as unknown[] }));
const lumaOK = { status: 'ok', read_at: '2026-11-07T23:41:00Z', last_good_at: '2026-11-07T23:41:00Z', events: [] as unknown[], leftovers: [] as unknown[] };
const getOutreachLuma = vi.fn(() => Promise.resolve(lumaOK as unknown));
vi.mock('../../lib/api', () => ({
  listOutreach: () => listOutreach(),
  getOutreachLuma: () => getOutreachLuma(),
  listAdminWorkshops: () => listAdminWorkshops(),
  getOutreachTimeline: () => getOutreachTimeline(),
  getWorkshopOutreach: (id: number) => getWorkshopOutreach(id),
  getOutreachConnections: () => getOutreachConnections(),
  getOverview: () => getOverview(),
  getAdminVersion: vi.fn(() => Promise.reject(new Error('unused'))),
  ApiError: class ApiError extends Error {},
}));
vi.mock('../../lib/events', () => ({ subscribeEvent: vi.fn(() => vi.fn()) }));

const { default: Outreach } = await import('./Outreach.svelte');
const { default: Workshops } = await import('./Workshops.svelte');
const { default: OutreachEditorSummary } = await import('./OutreachEditorSummary.svelte');
const { default: Dashboard } = await import('./Dashboard.svelte');

afterEach(() => {
  cleanup();
  for (const m of [listOutreach, listAdminWorkshops, getWorkshopOutreach, getOutreachConnections, getOverview]) m.mockReset();
});

describe('Outreach landing list', () => {
  it('lists published upcoming workshops with a chip and one button, and no old empty state', async () => {
    listAdminWorkshops.mockResolvedValue({
      workshops: [ws(1), ws(2), ws(3, { status: 'draft' }), ws(4, { starts_at: '2020-01-01T00:00:00Z' })],
    });
    listOutreach.mockResolvedValue({ outreach: [sum(2, { posted: 2, scheduled: 4 })] });
    render(Outreach);
    await screen.findByText('Workshop 1');
    expect(screen.getByText('Workshop 2')).toBeTruthy();
    expect(screen.queryByText('Workshop 3')).toBeNull();
    expect(screen.queryByText('Workshop 4')).toBeNull();
    expect(screen.getByText('Not started')).toBeTruthy();
    expect(screen.getByText('2 posted · 4 scheduled')).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Promote Workshop 1' }).getAttribute('href')).toContain('workshop=1');
    expect(screen.getByRole('link', { name: 'Open outreach for Workshop 2' }).getAttribute('href')).toContain('workshop=2');
    expect(document.body.textContent).not.toContain('No event has outreach yet');
  });

  it('lists past workshops that had outreach under Past, and not those that did not (#0593)', async () => {
    listAdminWorkshops.mockResolvedValue({
      workshops: [
        ws(1),
        ws(4, { title: 'Old with outreach', starts_at: '2020-01-01T00:00:00Z' }),
        ws(5, { title: 'Old without', starts_at: '2020-02-01T00:00:00Z' }),
      ],
    });
    listOutreach.mockResolvedValue({ outreach: [sum(4, { posted: 3 })] });
    render(Outreach);
    await screen.findByText('Old with outreach');
    expect(screen.getByText('Past', { selector: '.panel-header' })).toBeTruthy();
    expect(screen.queryByText('Old without')).toBeNull();
    expect(screen.getByRole('link', { name: 'Open outreach for Old with outreach' }).getAttribute('href')).toContain('workshop=4');
    expect(screen.getByRole('link', { name: 'Results for Old with outreach' })).toBeTruthy();
  });

  it('shows the timeline above the unchanged list (#0646)', async () => {
    listAdminWorkshops.mockResolvedValue({ workshops: [ws(1)] });
    listOutreach.mockResolvedValue({ outreach: [] });
    getOutreachTimeline.mockResolvedValueOnce({
      now: new Date().toISOString(),
      events: [{
        workshop: { id: 1, slug: 'w1', title: 'Workshop 1', status: 'published', starts_at: future(11) },
        outreach: null, channels: [], waves: [{ name: 'announce', label: 'Announce', suggested_at: null }], posts: [], off_channels: [],
      }],
    });
    render(Outreach);
    const timeline = await screen.findByText('Next 30 days', { selector: '.panel-header' });
    const list = screen.getByText('Outreach', { selector: '.panel-header' });
    expect(timeline.compareDocumentPosition(list) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.getByRole('table')).toBeTruthy();
    expect(screen.getAllByRole('link', { name: 'Promote Workshop 1' })).toHaveLength(2);
  });

  it('shows Retry when the timeline fails to load (#0646)', async () => {
    listAdminWorkshops.mockResolvedValue({ workshops: [ws(1)] });
    listOutreach.mockResolvedValue({ outreach: [] });
    getOutreachTimeline.mockRejectedValueOnce(new Error('down'));
    render(Outreach);
    expect(await screen.findByRole('button', { name: 'Retry' })).toBeTruthy();
  });

  it('labels the sub-nav Posts, Flyer spots (the saved locations), Flyers, Results', async () => {
    listAdminWorkshops.mockResolvedValue({ workshops: [] });
    listOutreach.mockResolvedValue({ outreach: [] });
    render(Outreach);
    const nav = screen.getByRole('navigation', { name: 'Outreach sections' });
    expect(Array.from(nav.querySelectorAll('a')).map((a) => a.textContent)).toEqual(['Posts', 'Flyer spots', 'Flyers', 'Results']);
    expect(screen.getByRole('link', { name: 'Flyer spots' }).getAttribute('href')).toContain('locations');
  });
});

describe('Workshops list Outreach column', () => {
  it('shows the same chip per row', async () => {
    listAdminWorkshops.mockResolvedValue({ workshops: [ws(1), ws(2)] });
    listOutreach.mockResolvedValue({ outreach: [sum(2, { posted: 1 })] });
    render(Workshops, { props: { workshopId: null, onOpenWorkshop: vi.fn(), onBackToList: vi.fn(), onAnnounced: vi.fn() } });
    await screen.findByText('Workshop 1');
    expect(screen.getByRole('columnheader', { name: 'Outreach' })).toBeTruthy();
    await waitFor(() => expect(screen.getByText('1 posted')).toBeTruthy());
    expect(screen.getByText('Not started')).toBeTruthy();
  });
});

describe('OutreachEditorSummary button', () => {
  const base = {
    available: true, unavailable_reason: '', channels: [], waves: [],
    workshop: { id: 5, slug: 's', title: 'S', status: 'published', starts_at: future(5) },
  };
  it('reads Promote this workshop when nothing exists', async () => {
    getWorkshopOutreach.mockResolvedValue({ ...base, outreach: null, posts: [] } as unknown as WorkshopOutreach);
    render(OutreachEditorSummary, { props: { workshopId: 5 } });
    await waitFor(() => expect(screen.getByRole('link', { name: 'Promote this event' })).toBeTruthy());
  });
  it('reads Open outreach · N posted once it does', async () => {
    getWorkshopOutreach.mockResolvedValue({
      ...base,
      outreach: { id: 9, workshop_id: 5, campaign_slug: 's', created_at: '' },
      posts: [{ status: 'posted' }, { status: 'posted' }, { status: 'draft' }],
    } as unknown as WorkshopOutreach);
    render(OutreachEditorSummary, { props: { workshopId: 5 } });
    await waitFor(() => expect(screen.getByRole('link', { name: 'Open outreach · 2 posted' })).toBeTruthy());
  });
});

describe('Overview needs-attention box', () => {
  it('reports disconnected services and unannounced upcoming workshops', async () => {
    getOverview.mockResolvedValue({
      subscribers: { counts: { pending: 0, active: 0, unsubscribed: 0, bounced: 0, complained: 0 }, growth_30d: { confirmed_30d: 0, imported_30d: 0, unsubscribed_30d: 0, net_30d: 0 } },
      interests: [], recent_campaigns: [],
      warnings: { complaint_rate_review: false, complaint_rate_high: false, complaint_sample_size: 0, physical_address_unset: false, ses_sandbox_active: false, inbound_mail_unavailable: false, outbound_queue_abandoned: false },
    });
    getOutreachConnections.mockResolvedValue({ groups: [{ name: 'bluesky', configured: false, error: '', fields: [] }] });
    listAdminWorkshops.mockResolvedValue({ workshops: [ws(1), ws(2)] });
    listOutreach.mockResolvedValue({ outreach: [] });
    render(Dashboard, { props: { onOpenCampaign: vi.fn() } });
    await screen.findByText('Not connected: Bluesky. Connect in Settings → Outreach.');
    expect(screen.getByText('2 upcoming events not yet announced.')).toBeTruthy();
  });

  it('keeps the unannounced count when the connections call fails, via Promise.allSettled (#0593)', async () => {
    getOverview.mockResolvedValue({
      subscribers: { counts: { pending: 0, active: 0, unsubscribed: 0, bounced: 0, complained: 0 }, growth_30d: { confirmed_30d: 0, imported_30d: 0, unsubscribed_30d: 0, net_30d: 0 } },
      interests: [], recent_campaigns: [],
      warnings: { complaint_rate_review: false, complaint_rate_high: false, complaint_sample_size: 0, physical_address_unset: false, ses_sandbox_active: false, inbound_mail_unavailable: false, outbound_queue_abandoned: false },
    });
    getOutreachConnections.mockRejectedValue(new Error('down'));
    listAdminWorkshops.mockResolvedValue({ workshops: [ws(1), ws(2)] });
    listOutreach.mockResolvedValue({ outreach: [] });
    render(Dashboard, { props: { onOpenCampaign: vi.fn() } });
    await screen.findByText('2 upcoming events not yet announced.');
    expect(screen.queryByText(/Not connected/)).toBeNull();
  });
});

describe('Outreach landing list: Luma (#0648)', () => {
  it("shows each upcoming event's Luma state with the differing fields and the Luma link", async () => {
    listAdminWorkshops.mockResolvedValue({ workshops: [ws(1), ws(2, { title: 'Hack Night' }), ws(3)] });
    listOutreach.mockResolvedValue({ outreach: [] });
    getOutreachLuma.mockResolvedValueOnce({
      ...lumaOK,
      events: [
        { workshop_id: 1, state: 'differs', luma_url: 'https://luma.com/abc', luma_title: 'Intro to Workshop 1', diffs: [{ field: 'start', label: 'Start', site: '6:30 PM', luma: '6:00 PM' }] },
        { workshop_id: 2, state: 'on_luma', luma_url: 'https://luma.com/hack', luma_title: 'Hack Night', diffs: [] },
        { workshop_id: 3, state: 'not_on_luma', luma_url: null, luma_title: null, diffs: [] },
      ],
    });
    render(Outreach);
    expect(await screen.findByText('Differs')).toBeTruthy();
    expect(screen.getByRole('columnheader', { name: 'Luma' })).toBeTruthy();
    expect(screen.getByText('Start: site 6:30 PM, Luma 6:00 PM')).toBeTruthy();
    expect(screen.getByText('Luma title: Intro to Workshop 1')).toBeTruthy();
    expect(screen.getByText('On Luma')).toBeTruthy();
    expect(screen.getByText('Not on Luma')).toBeTruthy();
    expect(screen.queryByText('Luma title: Hack Night')).toBeNull();
    const links = screen.getAllByRole('link', { name: 'Open on Luma' });
    expect(links.map((a) => a.getAttribute('href'))).toEqual(['https://luma.com/abc', 'https://luma.com/hack']);
    expect(links[0].getAttribute('target')).toBe('_blank');
    expect(links[0].getAttribute('rel')).toBe('noopener noreferrer');
    expect(screen.getByText(/^Compared with Luma's calendar, read /)).toBeTruthy();
  });

  it('lists Luma events that match no event under On Luma only', async () => {
    listAdminWorkshops.mockResolvedValue({ workshops: [ws(1)] });
    listOutreach.mockResolvedValue({ outreach: [] });
    getOutreachLuma.mockResolvedValueOnce({
      ...lumaOK,
      events: [{ workshop_id: 1, state: 'on_luma', luma_url: 'https://luma.com/a', luma_title: 'Workshop 1', diffs: [] }],
      leftovers: [{ title: 'Hack Night', starts_at: '2026-11-17T03:00:00Z', luma_url: 'https://luma.com/3y6a2mnm' }],
    });
    render(Outreach);
    expect(await screen.findByText('On Luma only')).toBeTruthy();
    const item = document.querySelector('.luma-leftovers li');
    expect(item?.textContent).toContain('· Hack Night · Open on Luma');
    expect(item?.querySelector('a')?.getAttribute('href')).toBe('https://luma.com/3y6a2mnm');
  });

  it('shows two identical Luma-only events as two rows without breaking the list', async () => {
    listAdminWorkshops.mockResolvedValue({ workshops: [ws(1)] });
    listOutreach.mockResolvedValue({ outreach: [] });
    const dup = { title: 'Hack Night', starts_at: '2026-11-17T03:00:00Z', luma_url: 'https://luma.com/3y6a2mnm' };
    getOutreachLuma.mockResolvedValueOnce({ ...lumaOK, events: [], leftovers: [dup, { ...dup }] });
    render(Outreach);
    expect(await screen.findByText('On Luma only')).toBeTruthy();
    expect(document.querySelectorAll('.luma-leftovers li')).toHaveLength(2);
    expect(screen.getByText('Workshop 1')).toBeTruthy();
  });

  it('keeps the list when the Luma check fails', async () => {
    listAdminWorkshops.mockResolvedValue({ workshops: [ws(1)] });
    listOutreach.mockResolvedValue({ outreach: [] });
    getOutreachLuma.mockRejectedValueOnce(new Error('boom'));
    render(Outreach);
    expect(await screen.findByText("Couldn't check Luma.")).toBeTruthy();
    expect(screen.getByText('Workshop 1')).toBeTruthy();
    expect(screen.getByText('Not started')).toBeTruthy();
  });
});
