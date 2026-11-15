// @vitest-environment jsdom
// /guides groups the guides into their sets and filters to one with ?set=.
import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen, within } from '@testing-library/svelte';
import { currentRoute, parsePath } from '../lib/router';
import GuidesIndex from './GuidesIndex.svelte';

afterEach(() => {
  cleanup();
  currentRoute.set(parsePath('/', ''));
});

function show(search: string) {
  currentRoute.set(parsePath('/guides', search));
  return render(GuidesIndex);
}

describe('GuidesIndex sets', () => {
  it('shows every set under its own heading, with a filter at the top', () => {
    show('');
    expect(screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent)).toEqual([
      'LEDs',
      'Multimeter',
      'Soldering',
      'Workshop guides',
    ]);
    const nav = screen.getByRole('navigation', { name: 'Filter guides' });
    const links = within(nav).getAllByRole('link');
    expect(links.map((a) => a.getAttribute('href'))).toEqual([
      '/guides',
      '/guides?set=leds',
      '/guides?set=multimeter',
      '/guides?set=soldering',
    ]);
    expect(links[0].getAttribute('aria-current')).toBe('page');
  });

  it('?set=soldering shows only the soldering guides and hides the LEDs workshop', () => {
    show('?set=soldering');
    expect(screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent)).toEqual(['Soldering']);
    const cards = screen.getAllByRole('link').filter((a) => a.getAttribute('href')?.startsWith('/guides/'));
    expect(cards.length).toBeGreaterThan(0);
    expect(cards.every((a) => a.getAttribute('href')!.startsWith('/guides/soldering-'))).toBe(true);
    const nav = screen.getByRole('navigation', { name: 'Filter guides' });
    expect(within(nav).getByRole('link', { name: /Soldering/ }).getAttribute('aria-current')).toBe('page');
    expect(within(nav).getByRole('link', { name: /All/ }).getAttribute('aria-current')).toBeNull();
  });

  it('?set=leds keeps the Programming LEDs workshop guide', () => {
    show('?set=leds');
    expect(screen.getByRole('heading', { name: 'Workshop guides' })).toBeTruthy();
    expect(screen.queryByRole('heading', { name: 'Multimeter' })).toBeNull();
  });
});
