// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import WorkshopCard from './WorkshopCard.svelte';

describe('WorkshopCard (#0614)', () => {
  afterEach(cleanup);

  it('shows the type name and links to /events', () => {
    render(WorkshopCard, {
      workshop: {
        slug: 'night-1',
        title: 'Soldering night',
        status: 'published',
        interests: [],
        event_type: { slug: 'hack-night', name: 'Hack Night', sort_order: 20 },
      },
    });
    expect(screen.getByText('Hack Night')).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Soldering night' }).getAttribute('href')).toBe('/events/night-1');
  });
});
