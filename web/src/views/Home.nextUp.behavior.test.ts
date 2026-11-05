// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';

const listPublicWorkshops = vi.fn();
vi.mock('../lib/api', async (original) => ({
  ...(await original<typeof import('../lib/api')>()),
  listPublicWorkshops: () => listPublicWorkshops(),
}));

const { default: Home } = await import('./Home.svelte');

describe('Home next up (#0614)', () => {
  beforeEach(() => {
    listPublicWorkshops.mockResolvedValue({
      upcoming: [
        {
          slug: 'friday',
          title: 'Friday build',
          status: 'published',
          interests: [],
          event_type: { slug: 'hack-night', name: 'Hack Night', sort_order: 20 },
        },
      ],
      past: [],
    });
  });
  afterEach(cleanup);

  it('shows a Hack Night in next up with its type', async () => {
    render(Home);
    await screen.findByRole('link', { name: 'Friday build' });
    expect(screen.getByText('Hack Night')).toBeTruthy();
  });

  it('links to /events', () => {
    render(Home);
    expect(screen.getByRole('link', { name: 'See all events' }).getAttribute('href')).toBe('/events');
  });

  it('keeps the headline', () => {
    render(Home);
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Hands-on electronics workshops');
  });
});
