// @vitest-environment jsdom
//
// #0678: the Events page's "Next month" panel, mounted alone and inside
// Workshops.svelte with '../../lib/api' mocked.
import { render, cleanup, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { AdminWorkshop, Coverage } from '../../lib/types';

const getEventCoverage = vi.fn();
const listAdminWorkshops = vi.fn();
const listOutreach = vi.fn();
const listEventTypes = vi.fn();
const listVenues = vi.fn();

vi.mock('../../lib/api', () => ({
  getEventCoverage: (...a: unknown[]) => getEventCoverage(...a),
  listAdminWorkshops: (...a: unknown[]) => listAdminWorkshops(...a),
  listOutreach: (...a: unknown[]) => listOutreach(...a),
  listEventTypes: (...a: unknown[]) => listEventTypes(...a),
  listVenues: (...a: unknown[]) => listVenues(...a),
  createWorkshop: vi.fn(),
  ApiError: class ApiError extends Error {},
}));

const { default: EventCoveragePanel } = await import('./EventCoveragePanel.svelte');
const { default: Workshops } = await import('./Workshops.svelte');

afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});

const draft = {
  id: 41,
  slug: 'hack-night-3',
  title: 'Hack night',
  status: 'draft',
  starts_at: '2026-12-15T18:00:00-08:00',
  admin_url: 'https://example.test/admin#/workshops/41',
  public_url: 'https://example.test/events/hack-night-3',
};

function coverage(over: Partial<Coverage> = {}): Coverage {
  return {
    month: '2026-12',
    label: 'December 2026',
    zone: 'America/Los_Angeles',
    from: '2026-12-01T00:00:00-08:00',
    to: '2027-01-01T00:00:00-08:00',
    rules: [
      {
        rule: {
          id: 1,
          name: 'Hack night',
          description: 'every Tuesday',
          event_type: { id: 2, slug: 'hack-night', name: 'Hack Night' },
          title_prefix: null,
          frequency: 'weekly',
          weekday: 'tuesday',
          per_month: null,
          venue: null,
          active: true,
          created_at: '',
          updated_at: '',
        },
        expected: { dates: ['2026-12-01', '2026-12-08', '2026-12-15', '2026-12-22', '2026-12-29'], count: 5 },
        events: [draft],
        canceled: [],
        drafts: [draft],
        missing_dates: ['2026-12-22', '2026-12-29'],
        draft_only_dates: ['2026-12-15'],
        missing: 2,
        state: 'missing',
      },
    ],
    unmatched_events: [],
    luma: { status: 'ok', read_at: null, last_good_at: null, not_on_luma: [draft], differs: [], only_on_luma: [], note: null },
    summary: { rules: 1, missing: 2, drafts: 1, unmatched: 0 },
    ...over,
  };
}

describe('EventCoveragePanel', () => {
  it('shows the headline, missing dates and draft links', async () => {
    getEventCoverage.mockResolvedValue(coverage());
    render(EventCoveragePanel);
    await screen.findByText('2 missing, 1 still a draft');
    expect(screen.getByText('Next month: December 2026')).toBeTruthy();
    expect(document.body.textContent).toContain('Missing: Tue, Dec 22, Tue, Dec 29');
    const link = screen.getByRole('link', { name: /Hack night/ });
    expect(link.getAttribute('href')).toBe('#/workshops/41');
    expect(document.body.textContent).toContain('Luma: 1 not on Luma');
    expect(getEventCoverage).toHaveBeenCalledWith();
  });

  it('says Luma is unavailable', async () => {
    getEventCoverage.mockResolvedValue(
      coverage({ luma: { status: 'unreachable', read_at: null, last_good_at: null, not_on_luma: [], differs: [], only_on_luma: [], note: null } }),
    );
    render(EventCoveragePanel);
    await screen.findByText('Luma unavailable (unreachable)');
  });

  it('links to Settings when there are no rules', async () => {
    getEventCoverage.mockResolvedValue(coverage({ rules: [], summary: { rules: 0, missing: 0, drafts: 0, unmatched: 0 } }));
    render(EventCoveragePanel);
    await screen.findByText('No cadence rules yet');
    expect(screen.getByRole('link', { name: 'Add cadence rules' }).getAttribute('href')).toBe('#/settings?section=cadence');
  });

  it('a failed fetch leaves the Events list rendered', async () => {
    getEventCoverage.mockRejectedValue(new Error('boom'));
    listAdminWorkshops.mockResolvedValue({
      workshops: [
        {
          id: 7,
          slug: 'soldering',
          title: 'Soldering',
          status: 'draft',
          interest_ids: [],
          event_type_id: 2,
          event_type: { slug: 'hack-night', name: 'Hack Night', sort_order: 10 },
          venue_id: null,
          created_at: '2026-11-04T00:00:00Z',
          updated_at: '2026-11-04T00:00:00Z',
        } as AdminWorkshop,
      ],
    });
    listOutreach.mockResolvedValue({ outreach: [] });
    listEventTypes.mockResolvedValue({ event_types: [] });
    listVenues.mockResolvedValue({ venues: [] });
    render(Workshops, { workshopId: null, onOpenWorkshop: vi.fn(), onBackToList: vi.fn(), onAnnounced: vi.fn() });
    await screen.findByRole('button', { name: 'Soldering' });
    await waitFor(() => expect(document.body.textContent).toContain('Coverage unavailable'));
  });
});
