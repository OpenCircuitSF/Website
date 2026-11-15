// @vitest-environment jsdom
// Guides share with one button that copies the page link (user request,
// 2026-11-14): the pages unfurl, so the link is all a reader needs.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render } from '@testing-library/svelte';
import { tick } from 'svelte';
import ShareBar from './ShareBar.svelte';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function mockClipboard(writeText: (s: string) => Promise<void>) {
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
}

describe('ShareBar', () => {
  it('has a single Share button and nothing platform-specific', () => {
    const r = render(ShareBar, { path: '/guides/baud-rate' });
    expect(r.getAllByRole('button').map((b) => b.textContent)).toEqual(['Share']);
    expect(r.container.querySelector('a')).toBeNull();
  });

  it('copies the absolute page link', async () => {
    const writeText = vi.fn(async () => {});
    mockClipboard(writeText);
    const r = render(ShareBar, { path: '/guides/baud-rate' });
    await fireEvent.click(r.getByRole('button', { name: 'Share' }));
    await tick();
    expect(writeText).toHaveBeenCalledWith(`${window.location.origin}/guides/baud-rate`);
    expect(r.getByRole('button', { name: 'Link copied ✓' })).toBeTruthy();
    expect(r.getByRole('status').textContent).toBe('Link copied');
  });

  it('shows the link to copy by hand when the clipboard is refused', async () => {
    mockClipboard(async () => {
      throw new Error('denied');
    });
    const r = render(ShareBar, { path: '/workshops/programming-leds' });
    await fireEvent.click(r.getByRole('button', { name: 'Share' }));
    await tick();
    const input = r.getByRole('textbox', { name: 'Link to this guide' }) as HTMLInputElement;
    expect(input.value).toBe(`${window.location.origin}/workshops/programming-leds`);
  });
});
