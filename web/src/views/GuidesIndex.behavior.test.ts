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
      'ESP32-C3',
      'Multimeter',
      'Soldering',
      'Components',
      'Raspberry Pi',
      'Workshop guides',
    ]);
    const nav = screen.getByRole('navigation', { name: 'Filter guides' });
    const links = within(nav).getAllByRole('link');
    expect(links.map((a) => a.getAttribute('href'))).toEqual([
      '/guides',
      '/guides?set=leds',
      '/guides?set=esp32',
      '/guides?set=multimeter',
      '/guides?set=soldering',
      '/guides?set=components',
      '/guides?set=pi',
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

describe('GuidesIndex card images (#0710)', () => {
  it('every guide card shows its own image, lazy, sized and decorative', () => {
    const { container } = show('');
    const cards = [...container.querySelectorAll<HTMLAnchorElement>('a.card')];
    expect(cards.length).toBeGreaterThan(0);
    for (const card of cards) {
      const imgs = card.querySelectorAll('img');
      expect(imgs).toHaveLength(1);
      const img = imgs[0];
      const slug = card.getAttribute('href')!.split('/').pop();
      expect(img.getAttribute('src')).toBe(`/guides/${slug}/thumb.jpg`);
      expect(img.getAttribute('alt')).toBe('');
      expect(img.getAttribute('width')).toBe('640');
      expect(img.getAttribute('height')).toBe('336');
      expect(img.getAttribute('loading')).toBe('lazy');
    }
  });

  it('the workshop guide card shows programming-leds image', () => {
    const { container } = show('');
    const card = container.querySelector('a[href="/workshops/programming-leds"]')!;
    expect(card.querySelector('img')!.getAttribute('src')).toBe('/guides/programming-leds/thumb.jpg');
  });

  it('no card renders the LED strip', () => {
    const { container } = show('');
    expect(container.querySelector('.cards .mini, .workshops .mini')).toBeNull();
  });
});
