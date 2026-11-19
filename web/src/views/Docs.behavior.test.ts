// @vitest-environment jsdom
//
// #0735: the Docs view, mounted. jsdom is not a browser (AGENTS.md §1): this
// proves DOM content and attributes, not layout or phone-width wrapping.
import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render } from '@testing-library/svelte';
import { currentRoute, parsePath } from '../lib/router';
import Docs from './Docs.svelte';
import newsletter from '../content/docs/newsletter.json';

afterEach(() => {
  cleanup();
  currentRoute.set(parsePath('/', ''));
});

describe('Docs view (#0735)', () => {
  it('renders one h1 from the JSON and marks the current page in the docs nav', () => {
    currentRoute.set(parsePath('/docs/newsletter', ''));
    const { container } = render(Docs);
    const h1s = container.querySelectorAll('h1');
    expect(h1s).toHaveLength(1);
    expect(h1s[0].textContent?.trim()).toBe(newsletter.h1);
    const links = [...container.querySelectorAll('nav[aria-label="Docs"] a')];
    expect(links.length).toBeGreaterThanOrEqual(4);
    for (const a of links) {
      const current = a.getAttribute('href') === '/docs/newsletter';
      expect(a.getAttribute('aria-current')).toBe(current ? 'page' : null);
    }
  });

  it('makes every code block keyboard-scrollable and named', () => {
    currentRoute.set(parsePath('/docs/feeds', ''));
    const { container } = render(Docs);
    const pres = [...container.querySelectorAll('pre')];
    expect(pres.length).toBeGreaterThan(0);
    for (const pre of pres) {
      expect(pre.getAttribute('tabindex')).toBe('0');
      expect(pre.getAttribute('aria-label')?.trim()).toBeTruthy();
    }
  });

  it('uses h1 then h2 only', () => {
    for (const p of ['/docs', '/docs/using-the-site', '/docs/newsletter', '/docs/feeds']) {
      currentRoute.set(parsePath(p, ''));
      const { container } = render(Docs);
      const tags = [...container.querySelectorAll('h1,h2,h3,h4,h5,h6')].map((h) => h.tagName);
      expect(tags[0]).toBe('H1');
      expect(tags.slice(1).every((t) => t === 'H2')).toBe(true);
      cleanup();
    }
  });
});
