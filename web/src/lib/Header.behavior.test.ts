// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import Header from './Header.svelte';

describe('Header (#0614)', () => {
  afterEach(cleanup);

  it('links Events to /events', () => {
    render(Header);
    expect(screen.getAllByRole('link', { name: 'Events' })[0].getAttribute('href')).toBe('/events');
    expect(screen.queryByRole('link', { name: 'Workshops' })).toBeNull();
  });
});
