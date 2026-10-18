// @vitest-environment jsdom
//
// #0586: the admin nav renders Mailing list / Workshops / System groups and a
// select that replaces them on narrow screens. jsdom applies no layout, so the
// "no more than two rows at 390 px" claim is NOT proven here: the select is
// present in the DOM and CSS shows it at <=480px, which a real browser must confirm.
import { render, cleanup, screen, within, fireEvent } from '@testing-library/svelte';
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

describe('Admin nav groups', () => {
  it('renders the three groups with their sections, and a select for narrow screens', async () => {
    currentUser.set({ id: 1, email: 'a@b.c', is_admin: true, active: true, created_at: '' } as never);
    window.location.hash = '#/';
    render(Admin);
    const groups: Record<string, string[]> = {
      'Mailing list': ['Subscribers', 'Pending', 'Suppressions', 'Deliverability', 'Campaigns', 'Interests'],
      Workshops: ['Workshops', 'Outreach'],
      System: ['Settings', 'Users', 'Audit log', 'CRT session'],
    };
    for (const [label, names] of Object.entries(groups)) {
      const g = within(document.querySelector('.subtab-links') as HTMLElement).getByRole('group', { name: label });
      expect(Array.from(g.querySelectorAll('a')).map((a) => a.textContent)).toEqual(names);
    }
    const select = screen.getByRole('combobox', { name: 'Admin section' }) as HTMLSelectElement;
    expect(select.querySelectorAll('optgroup').length).toBe(3);
    expect(select.value).toBe('overview');
  });

  it('navigates when the narrow-screen select changes (keyboard or pointer both fire change)', async () => {
    currentUser.set({ id: 1, email: 'a@b.c', is_admin: true, active: true, created_at: '' } as never);
    window.location.hash = '#/';
    render(Admin);
    const select = screen.getByRole('combobox', { name: 'Admin section' }) as HTMLSelectElement;
    await fireEvent.change(select, { target: { value: 'outreach' } });
    expect(window.location.hash).toBe('#/outreach');
    await fireEvent.change(select, { target: { value: 'overview' } });
    expect(window.location.hash).toBe('#/');
  });
});
