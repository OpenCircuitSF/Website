// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render } from '@testing-library/svelte';
import TempUnitToggle from './TempUnitToggle.svelte';
import { resetTempUnitForTest, tempUnit } from './tempUnit.svelte';
import { TEMP_UNIT_KEY } from './temperature';

// Node's own (unconfigured) localStorage global shadows jsdom's, so stub a
// plain in-memory one, with a switch to make writes throw.
let store: Record<string, string> = {};
let throwOnSet = false;
const fakeStorage = {
  getItem: (k: string) => (k in store ? store[k] : null),
  setItem: (k: string, v: string) => {
    if (throwOnSet) throw new Error('quota');
    store[k] = String(v);
  },
};

describe('TempUnitToggle', () => {
  beforeEach(() => {
    store = {};
    throwOnSet = false;
    vi.stubGlobal('localStorage', fakeStorage);
    resetTempUnitForTest();
  });
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it('defaults to °F with °F checked', () => {
    const r = render(TempUnitToggle);
    expect(r.getByRole('radiogroup', { name: 'Temperature units' })).toBeTruthy();
    expect(r.getByRole('radio', { name: '°F' }).getAttribute('aria-checked')).toBe('true');
    expect(r.getByRole('radio', { name: '°C' }).getAttribute('aria-checked')).toBe('false');
  });

  it('choosing °C checks it and stores "C"', async () => {
    const r = render(TempUnitToggle);
    await fireEvent.click(r.getByRole('radio', { name: '°C' }));
    expect(r.getByRole('radio', { name: '°C' }).getAttribute('aria-checked')).toBe('true');
    expect(tempUnit.value).toBe('C');
    expect(store[TEMP_UNIT_KEY]).toBe('C');
  });

  it('a stored "C" is read back on load', async () => {
    store[TEMP_UNIT_KEY] = 'C';
    vi.resetModules();
    const m = await import('./tempUnit.svelte');
    expect(m.tempUnit.value).toBe('C');
  });

  it('a garbage stored value falls back to °F', async () => {
    store[TEMP_UNIT_KEY] = 'kelvin';
    vi.resetModules();
    const m = await import('./tempUnit.svelte');
    expect(m.tempUnit.value).toBe('F');
  });

  it('still switches when localStorage.setItem throws', async () => {
    throwOnSet = true;
    const r = render(TempUnitToggle);
    await fireEvent.click(r.getByRole('radio', { name: '°C' }));
    expect(r.getByRole('radio', { name: '°C' }).getAttribute('aria-checked')).toBe('true');
    expect(store[TEMP_UNIT_KEY]).toBeUndefined();
  });
});
