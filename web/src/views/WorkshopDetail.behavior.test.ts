// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import type { PublicWorkshop } from '../lib/types';
import { currentRoute, parsePath } from '../lib/router';

const getPublicWorkshop = vi.fn();
vi.mock('../lib/api', async (original) => ({
  ...(await original<typeof import('../lib/api')>()),
  getPublicWorkshop: (slug: string) => getPublicWorkshop(slug),
}));

const { default: WorkshopDetail } = await import('./WorkshopDetail.svelte');
const { ApiError } = await import('../lib/api');

const hackNight = (over: Partial<PublicWorkshop> = {}): PublicWorkshop => ({
  slug: 'friday',
  title: 'Friday build',
  status: 'published',
  interests: [],
  signup_url: 'https://example.com/signup',
  event_type: { slug: 'hack-night', name: 'Hack Night', sort_order: 20 },
  ...over,
});

function mount() {
  currentRoute.set(parsePath('/events/friday', ''));
  render(WorkshopDetail);
}

describe('WorkshopDetail (#0614)', () => {
  afterEach(() => {
    cleanup();
    getPublicWorkshop.mockReset();
    currentRoute.set(parsePath('/', ''));
  });

  it('shows the type name', async () => {
    getPublicWorkshop.mockResolvedValue(hackNight());
    mount();
    await screen.findByRole('heading', { name: 'Friday build' });
    expect(screen.getByText('Hack Night')).toBeTruthy();
  });

  it('never says workshop for a hack night', async () => {
    getPublicWorkshop.mockResolvedValue(hackNight());
    mount();
    await screen.findByRole('heading', { name: 'Friday build' });
    expect(document.body.textContent?.toLowerCase()).not.toContain('workshop');
    cleanup();

    getPublicWorkshop.mockResolvedValue(hackNight({ status: 'canceled' }));
    mount();
    await screen.findByText('This event has been canceled.');
    expect(document.body.textContent?.toLowerCase()).not.toContain('workshop');
    cleanup();

    getPublicWorkshop.mockRejectedValue(new ApiError(404, 'not found', null));
    mount();
    await screen.findByText('See all events');
    expect(document.body.textContent?.toLowerCase()).not.toContain('workshop');
  });
});
