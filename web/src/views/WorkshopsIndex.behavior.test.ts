// @vitest-environment jsdom
//
// #0614: the type filter, mounted with the API mocked. jsdom is not a browser
// (AGENTS.md §1): this proves DOM content, not layout.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen, waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';
import type { PublicWorkshop } from '../lib/types';
import { currentRoute, parsePath } from '../lib/router';

const listPublicWorkshops = vi.fn();
vi.mock('../lib/api', async (original) => ({
  ...(await original<typeof import('../lib/api')>()),
  listPublicWorkshops: () => listPublicWorkshops(),
}));

const { default: WorkshopsIndex } = await import('./WorkshopsIndex.svelte');

const listing = (slug: string, title: string, typeSlug: string, name: string, sort_order: number): PublicWorkshop => ({
  slug,
  title,
  status: 'published',
  interests: [],
  event_type: { slug: typeSlug, name, sort_order },
});

async function mountAt(search: string) {
  currentRoute.set(parsePath('/events', search));
  render(WorkshopsIndex);
  await screen.findByRole('navigation', { name: 'Filter by type' });
}

describe('WorkshopsIndex type filter (#0614)', () => {
  beforeEach(() => {
    listPublicWorkshops.mockReset();
    listPublicWorkshops.mockResolvedValue({
      upcoming: [listing('h1', 'Friday build', 'hack-night', 'Hack Night', 20), listing('w1', 'Solder basics', 'workshop', 'Workshop', 10)],
      past: [listing('h2', 'Old build', 'hack-night', 'Hack Night', 20)],
    });
  });
  afterEach(() => {
    cleanup();
    currentRoute.set(parsePath('/', ''));
  });

  it('shows every type with no filter', async () => {
    await mountAt('');
    for (const title of ['Friday build', 'Solder basics', 'Old build']) expect(screen.getByText(title)).toBeTruthy();
    const nav = screen.getByRole('navigation', { name: 'Filter by type' });
    expect(Array.from(nav.querySelectorAll('a')).map((a) => a.textContent)).toEqual(['All', 'Workshop', 'Hack Night']);
  });

  it('narrows to the type in the URL', async () => {
    await mountAt('?type=workshop');
    expect(screen.getByText('Solder basics')).toBeTruthy();
    expect(screen.queryByText('Friday build')).toBeNull();
    expect(screen.getByText('No past events of this type.')).toBeTruthy();
  });

  it('changes with the URL without refetching', async () => {
    await mountAt('');
    currentRoute.set(parsePath('/events', '?type=hack-night'));
    await tick();
    await waitFor(() => expect(screen.queryByText('Solder basics')).toBeNull());
    expect(screen.getByText('Friday build')).toBeTruthy();
    expect(listPublicWorkshops).toHaveBeenCalledTimes(1);
  });

  it('shows the empty state for an unknown type and does not echo it', async () => {
    await mountAt('?type=nonsense-value');
    expect(screen.getByText(/No events of that type\./)).toBeTruthy();
    expect(screen.getByRole('link', { name: 'See all events' }).getAttribute('href')).toBe('/events');
    expect(screen.queryByRole('heading', { name: 'Upcoming' })).toBeNull();
    expect(document.body.textContent).not.toContain('nonsense-value');
  });

  it('marks the selected filter', async () => {
    await mountAt('?type=hack-night');
    const nav = screen.getByRole('navigation', { name: 'Filter by type' });
    const current = Array.from(nav.querySelectorAll('a[aria-current="true"]'));
    expect(current.map((a) => a.textContent)).toEqual(['Hack Night']);
    expect(current[0].getAttribute('href')).toBe('/events?type=hack-night');
  });
});
