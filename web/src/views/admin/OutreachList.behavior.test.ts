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
  return { id, slug: `w${id}`, title: `Workshop ${id}`, status: 'published', starts_at: future(10 + id), interest_ids: [], created_at: '', updated_at: '', ...over };
}
function sum(id: number, counts: Record<string, number>): OutreachSummary {
  return { outreach_id: id, workshop_id: id, workshop_slug: '', workshop_title: '', workshop_status: 'published', campaign_slug: 'c', starts_at: null, counts, created_at: '' };
}

const listOutreach = vi.fn<() => Promise<{ outreach: OutreachSummary[] }>>();
const listAdminWorkshops = vi.fn<() => Promise<{ workshops: AdminWorkshop[] }>>();
const getWorkshopOutreach = vi.fn<(id: number) => Promise<WorkshopOutreach>>();
const getOutreachConnections = vi.fn<() => Promise<{ groups: ConnectionGroup[] }>>();
const getOverview = vi.fn();
vi.mock('../../lib/api', () => ({
  listOutreach: () => listOutreach(),
  listAdminWorkshops: () => listAdminWorkshops(),
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
    expect(screen.getByRole('link', { name: 'Promote' }).getAttribute('href')).toContain('workshop=1');
    expect(screen.getByRole('link', { name: 'Open' }).getAttribute('href')).toContain('workshop=2');
    expect(document.body.textContent).not.toContain('No workshop has outreach yet');
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
    await waitFor(() => expect(screen.getByRole('link', { name: 'Promote this workshop' })).toBeTruthy());
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
    expect(screen.getByText('2 upcoming workshops not yet announced.')).toBeTruthy();
  });
});
