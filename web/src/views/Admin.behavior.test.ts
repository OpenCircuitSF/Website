// @vitest-environment jsdom
//
// #0219: mounts Admin.svelte with @testing-library/svelte under jsdom and
// asserts what #0059's CSV export endpoint had been missing since it landed
// — a real discoverable link in the admin UI — actually renders with the
// right `href`, including the admin's current status/interest/query filter.
// This is deliberately a jsdom-mounted, behavioural test rather than only a
// unit test of subscribersExportHref() (lib/admin.test.ts already has that):
// the acceptance criterion this issue was filed against asks specifically
// for a test that "assert[s] the link's href (and that filters appear in
// it) rather than only that an element renders" — i.e. that the pure
// function is actually WIRED to the live filter state on screen, not just
// correct in isolation.
//
// Admin.svelte's own onMount fires six loaders unconditionally
// (getSettings, listUsers, listAudit, listInterests, listSubscribers,
// listSuppressions) regardless of which section tab is showing, so all six
// are mocked here (following CampaignSendDialog.behavior.test.ts and
// Dashboard.behavior.test.ts's precedent of mocking '../../lib/api' so the
// test controls the payload directly rather than hitting a real network).
// The remaining named imports from '../lib/api' (updateSetting,
// deactivateUser, reactivateUser, createInterest, updateInterest,
// deleteInterest, getSubscriber, suppressSubscriber,
// clearSubscriberComplaint, createSubscriber, removeSuppression, logout)
// are never invoked by anything this test does (no button that calls them
// is clicked), so they are stubbed as plain vi.fn()s only so the mock
// factory's shape matches Admin.svelte's import list -- their return values
// are never read.
import { render, cleanup, waitFor, fireEvent, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { currentUser } from '../lib/stores';
import type {
  AdminUser,
  Setting,
  Interest,
  SubscribersPage,
  Suppression,
  DashboardOverview,
} from '../lib/types';
import type { AuditPage } from '../lib/api';

// Admin.svelte's default section ('overview') mounts Dashboard.svelte, which
// makes its own GET /admin/overview call independently of anything this test
// exercises -- mocked to a static empty payload purely so mount doesn't
// reject or throw; no test here asserts on it.
const getOverview = vi.fn<() => Promise<DashboardOverview>>();

const getSettings = vi.fn<() => Promise<{ settings: Setting[] }>>();
const listUsers = vi.fn<() => Promise<{ users: AdminUser[] }>>();
const listAudit = vi.fn<(...args: unknown[]) => Promise<AuditPage>>();
const listInterests = vi.fn<() => Promise<{ interests: Interest[] }>>();
const listSubscribers = vi.fn<(...args: unknown[]) => Promise<SubscribersPage>>();
const listSuppressions = vi.fn<() => Promise<{ suppressions: Suppression[]; count: number }>>();

// Admin.svelte's default section is 'overview', which mounts Dashboard.svelte
// underneath it -- so its own subscribeEvent('campaign.progress', ...) call
// runs even though this test only cares about the Subscribers section.
// Mocked for the same reason Dashboard.behavior.test.ts mocks it: plain
// jsdom has no EventSource, and events.ts's defaultFactory would otherwise
// throw on mount.
const unsubscribe = vi.fn();
const subscribeEvent = vi.fn((..._args: unknown[]) => unsubscribe);
vi.mock('../lib/events', () => ({
  subscribeEvent: (...args: unknown[]) => subscribeEvent(...(args as [])),
}));

vi.mock('../lib/api', () => ({
  getOverview: () => getOverview(),
  getSettings: () => getSettings(),
  updateSetting: vi.fn(),
  listUsers: () => listUsers(),
  deactivateUser: vi.fn(),
  reactivateUser: vi.fn(),
  listAudit: (...args: unknown[]) => listAudit(...args),
  listInterests: () => listInterests(),
  createInterest: vi.fn(),
  updateInterest: vi.fn(),
  deleteInterest: vi.fn(),
  listSubscribers: (...args: unknown[]) => listSubscribers(...args),
  getSubscriber: vi.fn(),
  suppressSubscriber: vi.fn(),
  clearSubscriberComplaint: vi.fn(),
  createSubscriber: vi.fn(),
  listSuppressions: () => listSuppressions(),
  removeSuppression: vi.fn(),
  logout: vi.fn(),
  ApiError: class ApiError extends Error {},
}));

// Imported AFTER the mock above so Admin.svelte's own '../lib/api' import
// resolves to the mocked module (Vitest hoists vi.mock calls, but the
// dynamic import keeps the dependency explicit).
const { default: Admin } = await import('./Admin.svelte');

function adminUser(): AdminUser {
  return {
    id: 1,
    email: 'admin@example.com',
    is_admin: true,
    active: true,
    created_at: '2026-01-01T00:00:00Z',
  };
}

function emptyAuditPage(): AuditPage {
  return { audit_log: [], total: 0, page: 1, per_page: 50 };
}

function emptySubscribersPage(): SubscribersPage {
  return {
    subscribers: [],
    total: 0,
    page: 1,
    per_page: 25,
    counts: { pending: 0, active: 0, unsubscribed: 0, bounced: 0, complained: 0 },
  };
}

const ROBOTICS_INTEREST: Interest = {
  id: 5,
  slug: 'robotics',
  name: 'Robotics',
  sort_order: 0,
  active: true,
  subscriber_count: 10,
  created_at: '2026-01-01T00:00:00Z',
};

afterEach(() => {
  cleanup();
  currentUser.set(null);
  getOverview.mockReset();
  getSettings.mockReset();
  listUsers.mockReset();
  listAudit.mockReset();
  listInterests.mockReset();
  listSubscribers.mockReset();
  listSuppressions.mockReset();
  subscribeEvent.mockClear();
  unsubscribe.mockClear();
});

function emptyOverview(): DashboardOverview {
  return {
    subscribers: {
      counts: { pending: 0, active: 0, unsubscribed: 0, bounced: 0, complained: 0 },
      growth_30d: { confirmed_30d: 0, imported_30d: 0, unsubscribed_30d: 0, net_30d: 0 },
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

async function renderSubscribersSection(): Promise<void> {
  getOverview.mockResolvedValue(emptyOverview());
  getSettings.mockResolvedValue({ settings: [] });
  listUsers.mockResolvedValue({ users: [] });
  listAudit.mockResolvedValue(emptyAuditPage());
  listInterests.mockResolvedValue({ interests: [ROBOTICS_INTEREST] });
  listSubscribers.mockResolvedValue(emptySubscribersPage());
  listSuppressions.mockResolvedValue({ suppressions: [], count: 0 });

  currentUser.set(adminUser());
  render(Admin);

  const subscribersTab = await screen.findByRole('button', { name: 'Subscribers' });
  await fireEvent.click(subscribersTab);
  await waitFor(() => expect(listSubscribers).toHaveBeenCalled());
}

describe('Admin.svelte subscribers section: CSV export link (#0219)', () => {
  it('renders a real <a href download> to the export endpoint, not a button (a real navigation, per this issue\'s criterion 3)', async () => {
    await renderSubscribersSection();

    const link = await screen.findByRole('link', { name: 'Export CSV' });
    expect(link.tagName).toBe('A');
    expect(link.getAttribute('href')).toBe('/admin/subscribers/export');
    // `download` present (any value, including empty) is what the SPA
    // router's own shouldIntercept() treats as "let the browser handle
    // this" (src/lib/router.ts) rather than hijacking it into a client-side
    // pushState navigation -- without it, clicking this link would never
    // reach the server at all.
    expect(link.hasAttribute('download')).toBe(true);
  });

  it('carries the status filter into the href as soon as it is selected, with no Apply click needed', async () => {
    await renderSubscribersSection();

    const statusSelect = screen.getByLabelText('Status') as HTMLSelectElement;
    await fireEvent.change(statusSelect, { target: { value: 'active' } });

    const link = await screen.findByRole('link', { name: 'Export CSV' });
    expect(link.getAttribute('href')).toBe('/admin/subscribers/export?status=active');
  });

  it('carries the interest filter into the href', async () => {
    await renderSubscribersSection();

    const interestSelect = screen.getByLabelText('Interest') as HTMLSelectElement;
    await fireEvent.change(interestSelect, { target: { value: String(ROBOTICS_INTEREST.id) } });

    const link = await screen.findByRole('link', { name: 'Export CSV' });
    expect(link.getAttribute('href')).toBe('/admin/subscribers/export?interest_id=5');
  });

  it('carries the query filter into the href only after Apply is clicked (mirrors what loadSubscribers itself sends)', async () => {
    await renderSubscribersSection();

    const queryInput = screen.getByLabelText('Email contains') as HTMLInputElement;
    await fireEvent.input(queryInput, { target: { value: 'example.com' } });

    // Not applied yet -- the raw, not-yet-submitted query must not leak into
    // the export href, matching loadSubscribers' own subsQueryApplied/
    // subsQueryRaw split.
    let link = await screen.findByRole('link', { name: 'Export CSV' });
    expect(link.getAttribute('href')).toBe('/admin/subscribers/export');

    const applyButton = screen.getByRole('button', { name: 'Apply' });
    await fireEvent.click(applyButton);

    link = await screen.findByRole('link', { name: 'Export CSV' });
    expect(link.getAttribute('href')).toBe('/admin/subscribers/export?q=example.com');
  });

  it('carries all three filters together in the href', async () => {
    await renderSubscribersSection();

    const statusSelect = screen.getByLabelText('Status') as HTMLSelectElement;
    await fireEvent.change(statusSelect, { target: { value: 'pending' } });
    const interestSelect = screen.getByLabelText('Interest') as HTMLSelectElement;
    await fireEvent.change(interestSelect, { target: { value: String(ROBOTICS_INTEREST.id) } });
    const queryInput = screen.getByLabelText('Email contains') as HTMLInputElement;
    await fireEvent.input(queryInput, { target: { value: 'example.com' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Apply' }));

    const link = await screen.findByRole('link', { name: 'Export CSV' });
    expect(link.getAttribute('href')).toBe(
      '/admin/subscribers/export?status=pending&interest_id=5&q=example.com',
    );
  });
});
