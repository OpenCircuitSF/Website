// @vitest-environment jsdom
//
// #0568: mounts OutreachLinks.svelte under jsdom with '../../lib/api' mocked.
// jsdom is not a browser (AGENTS.md §1): this proves the table renders what
// the payload says and wires the right calls, not layout or real clipboard
// permissions (navigator.clipboard is stubbed).
import { render, cleanup, waitFor, screen, fireEvent } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { OutreachLinkRow, OutreachLinksView } from '../../lib/types';

const getWorkshopOutreachLinks = vi.fn<(id: number) => Promise<OutreachLinksView>>();
const createWorkshopOutreachLinks = vi.fn<(id: number) => Promise<OutreachLinksView>>();
vi.mock('../../lib/api', () => ({
  getWorkshopOutreachLinks: (id: number) => getWorkshopOutreachLinks(id),
  createWorkshopOutreachLinks: (id: number) => createWorkshopOutreachLinks(id),
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

const { default: OutreachLinks } = await import('./OutreachLinks.svelte');

afterEach(() => {
  cleanup();
  getWorkshopOutreachLinks.mockReset();
  createWorkshopOutreachLinks.mockReset();
});

function row(over: Partial<OutreachLinkRow> = {}): OutreachLinkRow {
  return {
    kind: 'post', channel: 'bluesky', channel_label: 'Bluesky', wave: 'announce', wave_label: 'Announce',
    location_id: null, location_name: null, location_code: null,
    short_link_key: 'k1', short_url: 'https://go.example/k1',
    destination_url: 'https://site/workshops/w?utm_campaign=w&utm_content=announce&utm_medium=social&utm_source=bluesky',
    utm: { source: 'bluesky', medium: 'social', campaign: 'w', content: 'announce' }, clicks: 4, ...over,
  };
}

function view(over: Partial<OutreachLinksView> = {}): OutreachLinksView {
  return {
    available: true, unavailable_reason: '', outreach: { id: 1, campaign_slug: 'w' }, clicks_status: 'ok',
    missing: 0, links: [row()], report: null, ...over,
  };
}

describe('OutreachLinks', () => {
  it('renders the short URL, the destination with its UTM tags and the clicks', async () => {
    getWorkshopOutreachLinks.mockResolvedValue(view());
    render(OutreachLinks, { workshopId: 5, available: true });
    expect(await screen.findByText('https://go.example/k1')).toBeTruthy();
    expect(screen.getByText(/utm_source=bluesky/)).toBeTruthy();
    expect(screen.getByText('4')).toBeTruthy();
    expect(getWorkshopOutreachLinks).toHaveBeenCalledWith(5);
    expect(screen.queryByText('Create links')).toBeNull();
  });

  it('Copy writes the short URL', async () => {
    getWorkshopOutreachLinks.mockResolvedValue(view());
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    render(OutreachLinks, { workshopId: 5, available: true });
    await fireEvent.click(await screen.findByRole('button', { name: /^Copy short URL for Bluesky Announce/ }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('https://go.example/k1'));
    await waitFor(() => expect(screen.getByRole('status').textContent).toContain('Copied'));
  });

  it('shows missing rows without a Copy button', async () => {
    getWorkshopOutreachLinks.mockResolvedValue(
      view({ missing: 1, links: [row(), row({ wave: 'reminder', wave_label: 'Reminder', short_url: null, short_link_key: null, clicks: null })] }),
    );
    render(OutreachLinks, { workshopId: 5, available: true });
    expect(await screen.findByText('missing')).toBeTruthy();
    expect(screen.getAllByRole('button', { name: /^Copy short URL/ }).length).toBe(1);
    expect(screen.getByRole('button', { name: 'Create missing links' })).toBeTruthy();
  });

  it('Create links calls the API and names the failed link', async () => {
    const empty = view({ missing: 1, links: [row({ short_url: null, short_link_key: null, clicks: null })] });
    getWorkshopOutreachLinks.mockResolvedValue(empty);
    createWorkshopOutreachLinks.mockResolvedValue(
      view({
        missing: 1,
        links: [row({ short_url: null, short_link_key: null, clicks: null })],
        report: { created: 0, existing: 0, stopped: false, failed: [{ kind: 'post', channel: 'bluesky', wave: 'announce', label: 'Bluesky · Announce', reason: 'rate_limited' }] },
      }),
    );
    render(OutreachLinks, { workshopId: 5, available: true });
    await fireEvent.click(await screen.findByRole('button', { name: 'Create links' }));
    expect(createWorkshopOutreachLinks).toHaveBeenCalledWith(5);
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('Bluesky · Announce (ShortLinks is rate limiting)'));
  });

  it('points at Settings when ShortLinks is not connected and disables nothing it cannot', async () => {
    getWorkshopOutreachLinks.mockResolvedValue(view({ available: false, unavailable_reason: 'shortlinks_not_configured', links: [], outreach: null }));
    render(OutreachLinks, { workshopId: 5, available: false });
    const link = await screen.findByRole('link', { name: 'Open Settings' });
    expect(link.getAttribute('href')).toBe('#/settings?section=outreach');
  });

  it('refetches when reloadKey changes', async () => {
    getWorkshopOutreachLinks.mockResolvedValue(view());
    const { rerender } = render(OutreachLinks, { workshopId: 5, available: true, reloadKey: 0 });
    await waitFor(() => expect(getWorkshopOutreachLinks).toHaveBeenCalledTimes(1));
    await rerender({ workshopId: 5, available: true, reloadKey: 1 });
    await waitFor(() => expect(getWorkshopOutreachLinks).toHaveBeenCalledTimes(2));
  });
});
