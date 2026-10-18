// @vitest-environment jsdom
//
// #0527 criterion 11: mount the real Admin.svelte at a given hash and assert
// the screen. jsdom cannot show reload/back/forward/cmd-click — those claims
// rest on the hash-only popstate skip and fragment-link fall-through in
// router.ts (unit-tested) plus the real-browser check recorded in the issue's
// ## Verification. What a mount CAN prove: the hash decides the first screen,
// a hashchange updates it, the empty hash normalizes, unknown hashes show
// not-found without crashing, the deep-link audit filter applies, and a
// signed-out visitor is redirected to login with the deep link as ?next=.
import { render, cleanup, waitFor, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { currentUser } from '../lib/stores';
import type {
  AdminUser,
  Setting,
  Interest,
  SubscribersPage,
  Suppression,
  DashboardOverview,
  Campaign,
  AdminWorkshop,
} from '../lib/types';
import type { AuditPage } from '../lib/api';
import { getServerSettings } from '../lib/api';

const getOverview = vi.fn<() => Promise<DashboardOverview>>();
const getAdminVersion = vi.fn<() => Promise<{ version: string; commit_hash: string; commit_hash_short: string }>>();
const getSettings = vi.fn<() => Promise<{ settings: Setting[] }>>();
const listUsers = vi.fn<() => Promise<{ users: AdminUser[] }>>();
const listAudit = vi.fn<(...args: unknown[]) => Promise<AuditPage>>();
const listInterests = vi.fn<() => Promise<{ interests: Interest[] }>>();
const listSubscribers = vi.fn<(...args: unknown[]) => Promise<SubscribersPage>>();
const listSuppressions = vi.fn<() => Promise<{ suppressions: Suppression[]; count: number }>>();
const listCampaigns = vi.fn<() => Promise<{ campaigns: Campaign[] }>>();
const listAdminWorkshops = vi.fn<() => Promise<{ workshops: AdminWorkshop[] }>>();

const unsubscribe = vi.fn();
const subscribeEvent = vi.fn((..._args: unknown[]) => unsubscribe);
vi.mock('../lib/events', () => ({
  subscribeEvent: (...args: unknown[]) => subscribeEvent(...(args as [])),
}));

vi.mock('../lib/api', () => ({
  getOverview: () => getOverview(),
  getAdminVersion: () => getAdminVersion(),
  getSettings: () => getSettings(),
  updateSetting: vi.fn(),
  getOutreachConnections: vi.fn(() => Promise.resolve({ groups: [] })),
  getServerSettings: vi.fn(() => Promise.resolve({ groups: [] })),
  updateOutreachConnections: vi.fn(),
  testOutreachConnection: vi.fn(),
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
  importPreview: vi.fn(),
  importCommit: vi.fn(),
  revokeImport: vi.fn(),
  listCampaigns: () => listCampaigns(),
  createCampaign: vi.fn(),
  listAdminWorkshops: () => listAdminWorkshops(),
  createWorkshop: vi.fn(),
  getCampaign: vi.fn(),
  logout: vi.fn(),
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

// Imported AFTER the mock above so Admin.svelte's own '../lib/api' import
// resolves to the mocked module.
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

beforeEach(() => {
  vi.resetModules();
  getOverview.mockReset();
  getAdminVersion.mockReset();
  getSettings.mockReset();
  listUsers.mockReset();
  listAudit.mockReset();
  listInterests.mockReset();
  listSubscribers.mockReset();
  listSuppressions.mockReset();
  listCampaigns.mockReset();
  listAdminWorkshops.mockReset();

  getOverview.mockResolvedValue({
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
  });
  getAdminVersion.mockResolvedValue({ version: '0.1.2', commit_hash: 'a'.repeat(40), commit_hash_short: 'aaaaaaa' });
  getSettings.mockResolvedValue({ settings: [] });
  listUsers.mockResolvedValue({ users: [] });
  listAudit.mockResolvedValue({ audit_log: [], total: 0, page: 1, per_page: 50 });
  listInterests.mockResolvedValue({ interests: [] });
  listSubscribers.mockResolvedValue({
    subscribers: [],
    total: 0,
    page: 1,
    per_page: 50,
    status_counts: { pending: 0, active: 0, unsubscribed: 0, bounced: 0, complained: 0 },
  } as unknown as SubscribersPage);
  listSuppressions.mockResolvedValue({ suppressions: [], count: 0 });
  listCampaigns.mockResolvedValue({ campaigns: [] });
  listAdminWorkshops.mockResolvedValue({ workshops: [] });

  window.history.replaceState(null, '', '/');
  currentUser.set(adminUser());
});

afterEach(() => {
  cleanup();
});

describe('Admin.svelte hash routing (#0527)', () => {
  it('normalizes an empty hash to "#/" with replace, and shows Overview (criterion 5)', async () => {
    window.history.replaceState(null, '', '/');
    render(Admin);

    await waitFor(() => expect(screen.getByText('Overview', { selector: '.panel-header' })).toBeTruthy());
    expect(window.location.hash).toBe('#/');
    // replace, not push: one history entry.
    expect(window.history.length).toBe(1);
  });

  it('shows the screen the loaded hash names — #/settings (criteria 1 and 2)', async () => {
    window.history.replaceState(null, '', '/admin#/settings');
    render(Admin);

    await waitFor(() => expect(screen.getByText('Settings', { selector: '.panel-header' })).toBeTruthy());
    expect(screen.queryByText('Overview', { selector: '.panel-header' })).toBeNull();
    // Criterion 4: the active subtab is a real link with aria-current.
    const activeLink = screen.getByRole('link', { name: 'Settings' });
    expect(activeLink.getAttribute('aria-current')).toBe('page');
    expect(activeLink.getAttribute('href')).toBe('#/settings');
  });

  it('lands on the Outreach section from #/settings?section=outreach and lists every section (#0584)', async () => {
    const scrolled: string[] = [];
    const original = Element.prototype.scrollIntoView;
    Element.prototype.scrollIntoView = function (this: Element) {
      scrolled.push(this.id);
    };
    try {
      window.history.replaceState(null, '', '/admin#/settings?section=outreach');
      render(Admin);
      await waitFor(() => expect(scrolled).toContain('settings-outreach'));
      const jump = screen.getByRole('navigation', { name: 'Settings sections' });
      const hrefs = [...jump.querySelectorAll('a')].map((a) => a.getAttribute('href'));
      expect(hrefs).toEqual(['#/settings?section=general', '#/settings?section=server', '#/settings?section=outreach']);
      expect(document.getElementById('settings-outreach')?.textContent).toContain('Connections');
    } finally {
      Element.prototype.scrollIntoView = original;
    }
  });

  it('waits for the Server settings panel above it to load before landing on Outreach (#0584)', async () => {
    // A one-shot scroll fired while the panel above is still loading lands
    // short once that panel grows, so the deep link must hold until it settles.
    const scrolled: string[] = [];
    const original = Element.prototype.scrollIntoView;
    Element.prototype.scrollIntoView = function (this: Element) {
      scrolled.push(this.id);
    };
    let release: (v: { groups: [] }) => void = () => {};
    const pending = new Promise<{ groups: [] }>((r) => (release = r));
    vi.mocked(getServerSettings).mockImplementationOnce(() => pending as never);
    try {
      window.history.replaceState(null, '', '/admin#/settings?section=outreach');
      render(Admin);
      await waitFor(() => expect(getSettings).toHaveBeenCalled());
      await new Promise((r) => setTimeout(r, 20));
      expect(scrolled).not.toContain('settings-outreach');
      release({ groups: [] });
      await waitFor(() => expect(scrolled).toContain('settings-outreach'));
    } finally {
      Element.prototype.scrollIntoView = original;
    }
  });

  it('applies the audit campaign filter from a #/audit?campaign=42 deep link', async () => {
    window.history.replaceState(null, '', '/admin#/audit?campaign=42');
    render(Admin);

    await waitFor(() => {
      const targetType = screen.getByLabelText('Target type') as HTMLInputElement;
      expect(targetType.value).toBe('email_campaign');
    });
    const targetId = screen.getByLabelText('Target id') as HTMLInputElement;
    expect(targetId.value).toBe('42');
  });

  it('follows a hashchange to another screen (back/forward and link clicks both land here)', async () => {
    window.history.replaceState(null, '', '/admin#/');
    render(Admin);
    await waitFor(() => expect(screen.getByText('Overview', { selector: '.panel-header' })).toBeTruthy());

    window.history.pushState(null, '', '/admin#/settings');
    window.dispatchEvent(new Event('hashchange'));

    await waitFor(() => expect(screen.getByText('Settings', { selector: '.panel-header' })).toBeTruthy());
    expect(screen.queryByText('Overview', { selector: '.panel-header' })).toBeNull();
  });

  it('shows the not-found panel for an unknown hash, with no crash and no redirect (criterion 5)', async () => {
    window.history.replaceState(null, '', '/admin#/nope');
    render(Admin);

    await waitFor(() => expect(screen.getByText('Not found', { selector: '.panel-header' })).toBeTruthy());
    // The way back is a real link, not a loop.
    expect(screen.getByRole('link', { name: 'Back to Overview' }).getAttribute('href')).toBe('#/');
  });

  it('redirects a signed-out visitor to login with the deep link (hash included) as ?next= (criterion 6)', async () => {
    currentUser.set(null);
    window.history.replaceState(null, '', '/admin#/campaigns/42');
    render(Admin);

    await waitFor(() => {
      expect(window.location.pathname).toBe('/login');
      const next = new URLSearchParams(window.location.search).get('next') ?? '';
      expect(next.endsWith('#/campaigns/42')).toBe(true);
    });
  });
});
