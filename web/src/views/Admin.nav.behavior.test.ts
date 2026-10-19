// @vitest-environment jsdom
//
// #0599: two nav rows. Row one is Overview + the Workshops / Newsletter /
// System sections; row two is only the active section's screens. jsdom applies
// no layout, so "both rows are one line at 390 px" is NOT proven here; that
// needs a real browser.
import { render, cleanup, screen, within } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { currentUser } from '../lib/stores';

vi.mock('../lib/events', () => ({ subscribeEvent: vi.fn(() => vi.fn()) }));
vi.mock('../lib/api', () => {
  const empty = () => Promise.reject(new Error('unused'));
  return new Proxy({ ApiError: class ApiError extends Error {} }, {
    get(t, k: string) {
      if (k in t) return (t as Record<string, unknown>)[k];
      if (k === 'then') return undefined;
      return vi.fn(empty);
    },
    has: () => true,
  });
});

const { default: Admin } = await import('./Admin.svelte');

afterEach(() => {
  cleanup();
  currentUser.set(null);
});

function signIn(hash: string) {
  currentUser.set({ id: 1, email: 'a@b.c', is_admin: true, active: true, created_at: '' } as never);
  window.location.hash = hash;
  render(Admin);
}
const rowOne = () => screen.getByRole('navigation', { name: 'Admin sections' });
const linkNames = (nav: HTMLElement) => Array.from(nav.querySelectorAll('a')).map((a) => a.textContent);

describe('Admin nav rows', () => {
  it('row one has the four links in order with landing hrefs', () => {
    signIn('#/');
    const links = Array.from(rowOne().querySelectorAll('a'));
    expect(links.map((a) => a.textContent)).toEqual(['Overview', 'Workshops', 'Newsletter', 'System']);
    expect(links.map((a) => a.getAttribute('href'))).toEqual(['#/', '#/workshops', '#/campaigns', '#/settings']);
  });

  it('row two shows only the active group screens, in order, with aria-current=page on the active one', () => {
    signIn('#/subscribers');
    const row2 = screen.getByRole('navigation', { name: 'Newsletter screens' });
    expect(linkNames(row2)).toEqual([
      'Campaigns', 'Subscribers', 'Pending confirmations', 'Deliverability', 'Suppressions', 'Interests',
    ]);
    const current = Array.from(row2.querySelectorAll('a[aria-current="page"]'));
    expect(current.map((a) => a.textContent)).toEqual(['Subscribers']);
    expect(screen.queryByRole('navigation', { name: 'Workshops screens' })).toBeNull();
  });

  it('System row two lists Settings, Home screen, Users, Audit log', () => {
    signIn('#/crt');
    expect(linkNames(screen.getByRole('navigation', { name: 'System screens' }))).toEqual([
      'Settings', 'Home screen', 'Users', 'Audit log',
    ]);
  });

  it('has no row two on #/', () => {
    signIn('#/');
    expect(screen.queryByRole('navigation', { name: /screens$/ })).toBeNull();
    expect(rowOne().querySelector('a[aria-current="page"]')?.textContent).toBe('Overview');
  });

  it('#/outreach?view=results lights Workshops and Outreach', () => {
    signIn('#/outreach?view=results');
    expect(rowOne().querySelector('a[aria-current]')?.textContent).toBe('Workshops');
    const row2 = screen.getByRole('navigation', { name: 'Workshops screens' });
    expect(row2.querySelector('a[aria-current="page"]')?.textContent).toBe('Outreach');
  });

  it('#/nope lights nothing', () => {
    signIn('#/nope');
    expect(rowOne().querySelector('[aria-current]')).toBeNull();
    expect(screen.queryByRole('navigation', { name: /screens$/ })).toBeNull();
  });

  it('the narrow-screen select is gone', () => {
    signIn('#/');
    expect(screen.queryByRole('combobox', { name: 'Admin section' })).toBeNull();
    expect(document.querySelector('.subtab-select')).toBeNull();
  });

  it('the Account link is in the header, and the Account/Admin tab row is gone', () => {
    signIn('#/');
    const header = document.querySelector('header.app-header') as HTMLElement;
    expect(within(header).getByRole('link', { name: 'Account' }).getAttribute('href')).toBe('/account');
    expect(within(header).queryByRole('navigation', { name: 'Primary' })).toBeNull();
    expect(screen.queryByText('Mailing list')).toBeNull();
  });
});
