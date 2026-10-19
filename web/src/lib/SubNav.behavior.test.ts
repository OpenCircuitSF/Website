// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup } from '@testing-library/svelte';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import SubNav from './SubNav.svelte';

const items = [
  { href: '#/a', label: 'Alpha', current: true },
  { href: '#/b', label: 'Beta' }
];

describe('SubNav (#0600)', () => {
  afterEach(cleanup);

  it('renders a labelled nav of links with aria-current on the current one only', () => {
    render(SubNav, { label: 'Demo sections', items });
    const nav = screen.getByRole('navigation', { name: 'Demo sections' });
    const links = nav.querySelectorAll('a');
    expect(Array.from(links).map((a) => a.textContent)).toEqual(['Alpha', 'Beta']);
    expect(links[0].getAttribute('href')).toBe('#/a');
    expect(links[0].getAttribute('aria-current')).toBe('page');
    expect(links[1].hasAttribute('aria-current')).toBe(false);
  });

  it('calls an item onclick and carries the scroll-fade class', async () => {
    const onclick = vi.fn();
    render(SubNav, { label: 'Demo sections', items: [{ href: '#/b', label: 'Beta', onclick }] });
    await fireEvent.click(screen.getByRole('link', { name: 'Beta' }));
    expect(onclick).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('navigation').classList.contains('scroll-fade-x')).toBe(true);
  });

  it('the shared fade rule is pure CSS with a local-attachment cover', () => {
    const css = readFileSync(path.join(path.dirname(fileURLToPath(import.meta.url)), '../app.css'), 'utf-8');
    const start = css.indexOf('.scroll-fade-x {');
    expect(start).toBeGreaterThan(-1);
    const rule = css.slice(start, css.indexOf('}', start));
    expect(rule).toMatch(/var\(--bg-page\).*no-repeat local/);
    expect(rule).toMatch(/no-repeat scroll;/);
  });
});
