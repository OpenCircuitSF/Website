// @vitest-environment jsdom
//
// #0061/#0094: mounts Dashboard.svelte (the /admin landing screen) with
// @testing-library/svelte under jsdom and asserts what it actually renders
// for a realistic GET /admin/overview payload, including the empty state a
// fresh install shows on day one (zero of everything). Follows
// CampaignSendDialog.behavior.test.ts's precedent as this repo's first
// mounted-component test with network/SSE dependencies: '../../lib/api' and
// '../../lib/events' are mocked so this test controls the payload directly
// and never constructs a real EventSource (undefined under plain jsdom,
// which would otherwise throw on mount — see events.ts's defaultFactory).
import { render, cleanup, waitFor, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { DashboardOverview } from '../../lib/types';

const getOverview = vi.fn<() => Promise<DashboardOverview>>();
vi.mock('../../lib/api', () => ({
  getOverview: (...args: unknown[]) => getOverview(...(args as [])),
  ApiError: class ApiError extends Error {},
}));

const unsubscribe = vi.fn();
const subscribeEvent = vi.fn((..._args: unknown[]) => unsubscribe);
vi.mock('../../lib/events', () => ({
  subscribeEvent: (...args: unknown[]) => subscribeEvent(...(args as [])),
}));

// Imported AFTER the mocks above so Dashboard.svelte's own imports resolve
// to the mocked modules (Vitest hoists vi.mock calls, but the dynamic
// import below keeps the dependency explicit and readable).
const { default: Dashboard } = await import('./Dashboard.svelte');

afterEach(() => {
  cleanup();
  getOverview.mockReset();
  subscribeEvent.mockClear();
  unsubscribe.mockClear();
});

function emptyOverview(): DashboardOverview {
  return {
    subscribers: {
      counts: { pending: 0, active: 0, unsubscribed: 0, bounced: 0, complained: 0 },
      growth_30d: { confirmed_30d: 0, unsubscribed_30d: 0, net_30d: 0 },
    },
    interests: [],
    recent_campaigns: [],
    warnings: {
      complaint_rate_review: false,
      complaint_rate_high: false,
      complaint_sample_size: 0,
      physical_address_unset: false,
      ses_sandbox_active: false,
      inbound_mail_unavailable: true,
      outbound_queue_abandoned: false,
    },
  };
}

function populatedOverview(): DashboardOverview {
  return {
    subscribers: {
      counts: { pending: 3, active: 120, unsubscribed: 5, bounced: 2, complained: 1 },
      growth_30d: { confirmed_30d: 15, unsubscribed_30d: 4, net_30d: 11 },
    },
    interests: [
      { id: 1, slug: 'home-automation', name: 'Home Automation', subscriber_count: 42 },
      { id: 2, slug: 'robotics', name: 'Robotics', subscriber_count: 10 },
    ],
    recent_campaigns: [
      { id: 9, name: 'August announcement', status: 'sent', completed_at: '2026-08-20T12:00:00Z' },
      { id: 10, name: 'September teaser', status: 'sending', started_at: '2026-08-24T09:00:00Z' },
    ],
    sending_campaign: { id: 10, name: 'September teaser', status: 'sending', started_at: '2026-08-24T09:00:00Z' },
    warnings: {
      complaint_rate_review: true,
      complaint_rate_high: true,
      complaint_rate_pct: 0.42,
      complaint_sample_size: 200,
      physical_address_unset: true,
      ses_sandbox_active: true,
      inbound_mail_unavailable: true,
      outbound_queue_abandoned: false,
    },
  };
}

describe('Dashboard — empty state', () => {
  it('renders zero counts, empty-list copy, and no sending block for a fresh install', async () => {
    getOverview.mockResolvedValue(emptyOverview());
    render(Dashboard, { props: { onOpenCampaign: vi.fn() } });

    await waitFor(() => expect(screen.getByText('No interests defined yet.')).toBeTruthy());
    expect(screen.getByText('No campaigns yet.')).toBeTruthy();

    // Every subscriber-status badge shows 0.
    for (const label of ['Active', 'Pending', 'Unsubscribed', 'Bounced', 'Complained']) {
      const badge = screen.getByText(label);
      const row = badge.closest('.status-count');
      expect(row?.textContent).toContain('0');
    }

    // Net growth is a bare, unsigned "0".
    expect(screen.getByText('0', { selector: '.growth-net' })).toBeTruthy();

    // The one warning a fresh install always carries (#0058 unbuilt) shows
    // as an informational note, not an alert.
    const inboundNote = screen.getByText(/Inbound mailto: unsubscribe processing is not built yet/);
    expect(inboundNote.closest('li')?.classList.contains('alert')).toBe(false);

    // No sending campaign panel at all when none is in flight.
    expect(screen.queryByText('Currently sending')).toBeNull();
  });
});

describe('Dashboard — populated payload', () => {
  it('renders counts, growth, interests, recent campaigns, and alert warnings', async () => {
    getOverview.mockResolvedValue(populatedOverview());
    render(Dashboard, { props: { onOpenCampaign: vi.fn() } });

    await waitFor(() => expect(screen.getByText('Home Automation')).toBeTruthy());

    expect(screen.getByText('+11')).toBeTruthy(); // net_30d formatted with a leading +
    expect(screen.getByText(/15 joined, 4 left/)).toBeTruthy();

    expect(screen.getByText('Robotics')).toBeTruthy();
    expect(screen.getByText('42')).toBeTruthy();

    expect(screen.getByText('August announcement')).toBeTruthy();
    expect(screen.getAllByText('September teaser').length).toBeGreaterThan(0); // appears both in the sending block and the recent list

    const addressWarning = screen.getByText(/No physical mailing address is set/);
    expect(addressWarning.closest('li')?.classList.contains('alert')).toBe(true);

    // Both complaint-rate bands render as their own alert row (#0227: the
    // amber AWS-review warning and the red Gmail/Yahoo warning are
    // independent, not one escalating row).
    const reviewWarning = screen.getByText(/AWS's 0.1% account-wide threshold/);
    expect(reviewWarning.closest('li')?.classList.contains('alert')).toBe(true);

    const highWarning = screen.getByText(/Gmail\/Yahoo's published 0.3% bulk-sender limit/);
    expect(highWarning.closest('li')?.classList.contains('alert')).toBe(true);

    const sandboxWarning = screen.getByText(/configured for SES sandbox mode/);
    expect(sandboxWarning.closest('li')?.classList.contains('alert')).toBe(false);
  });

  it('calls onOpenCampaign with the clicked campaign id', async () => {
    getOverview.mockResolvedValue(populatedOverview());
    const onOpenCampaign = vi.fn();
    render(Dashboard, { props: { onOpenCampaign } });

    const link = await waitFor(() => screen.getByText('August announcement'));
    link.click();
    expect(onOpenCampaign).toHaveBeenCalledWith(9);
  });
});

describe('Dashboard — SSE lifecycle', () => {
  it('subscribes to campaign.progress on mount and unsubscribes on unmount', async () => {
    getOverview.mockResolvedValue(emptyOverview());
    const { unmount } = render(Dashboard, { props: { onOpenCampaign: vi.fn() } });

    await waitFor(() => expect(subscribeEvent).toHaveBeenCalledTimes(1));
    expect(subscribeEvent.mock.calls[0][0]).toBe('campaign.progress');

    unmount();
    expect(unsubscribe).toHaveBeenCalledTimes(1);
  });
});

describe('Dashboard — load error', () => {
  it('shows a retry button on failure and recovers on retry', async () => {
    getOverview.mockRejectedValueOnce(new Error('network down'));
    render(Dashboard, { props: { onOpenCampaign: vi.fn() } });

    const retry = await waitFor(() => screen.getByText('Retry'));
    getOverview.mockResolvedValueOnce(emptyOverview());
    retry.click();

    await waitFor(() => expect(screen.getByText('No campaigns yet.')).toBeTruthy());
  });
});
