import { describe, expect, it } from 'vitest';
import { asksBeforeLeaving } from './outreachLeave';

describe('asksBeforeLeaving (#0632)', () => {
  const here = { href: 'https://x.test/admin#/outreach?view=workshop&workshop=5', origin: 'https://x.test' };
  const click = { button: 0, metaKey: false, ctrlKey: false, shiftKey: false, altKey: false, defaultPrevented: false };
  const link = (href: string, over: Partial<{ target: string; hasDownload: boolean }> = {}) => ({ href, target: '', hasDownload: false, ...over });

  it('asks on a plain left click to another admin route', () => {
    expect(asksBeforeLeaving(click, link('#/workshops/5'), here)).toBe(true);
  });
  it('asks on a different path on the same site', () => {
    expect(asksBeforeLeaving(click, link('https://x.test/workshops'), here)).toBe(true);
  });
  it('does not ask for the same hash, an in-page anchor, or another site', () => {
    expect(asksBeforeLeaving(click, link('#/outreach?view=workshop&workshop=5'), here)).toBe(false);
    expect(asksBeforeLeaving(click, link('#main-content'), here)).toBe(false);
    expect(asksBeforeLeaving(click, link('https://elsewhere.test/'), here)).toBe(false);
  });
  it('does not ask for a modified or middle click, a new tab, a download, or a handled click', () => {
    const to = link('#/workshops/5');
    expect(asksBeforeLeaving({ ...click, metaKey: true }, to, here)).toBe(false);
    expect(asksBeforeLeaving({ ...click, button: 1 }, to, here)).toBe(false);
    expect(asksBeforeLeaving({ ...click, defaultPrevented: true }, to, here)).toBe(false);
    expect(asksBeforeLeaving(click, link('#/workshops/5', { target: '_blank' }), here)).toBe(false);
    expect(asksBeforeLeaving(click, link('#/workshops/5', { hasDownload: true }), here)).toBe(false);
  });
});
