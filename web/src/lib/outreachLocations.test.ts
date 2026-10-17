import { describe, expect, it } from 'vitest';
import {
  canDelete,
  canEditCode,
  formIsValid,
  locationPatch,
  locationStatusLabel,
  normalizeCodeInput,
  suggestCode,
  uniqueCode,
  validateCode,
  validateLocationForm,
} from './outreachLocations';
import type { OutreachLocation } from './types';

function loc(over: Partial<OutreachLocation> = {}): OutreachLocation {
  return {
    id: 1,
    name: 'Mission Library',
    code: 'mission',
    address: '300 Main',
    notes: 'north board',
    active: true,
    code_locked: false,
    code_locked_at: null,
    created_at: '2026-10-16T00:00:00Z',
    updated_at: null,
    ...over,
  };
}

describe('validateCode (same case list as internal/outreach locations_test.go)', () => {
  it.each(['ab', 'a1', 'mission-1', 'abcdefghijklmnop', 'a-b-c'])('accepts %s', (c) => {
    expect(validateCode(c)).toBeNull();
  });
  it.each(['a', 'abcdefghijklmnopq', 'Ab', '-ab', 'ab-', 'a--b', 'a_b', 'a b', 'é', ''])('rejects %j', (c) => {
    expect(validateCode(c)).not.toBeNull();
  });
  it('gives distinct messages', () => {
    const msgs = new Set([validateCode(''), validateCode('a'), validateCode('a'.repeat(17)), validateCode('A!')]);
    expect(msgs.size).toBe(4);
  });
});

describe('normalizeCodeInput', () => {
  it('lowercases and turns spaces and underscores into dashes', () => {
    expect(normalizeCodeInput('Mission Branch')).toBe('mission-branch');
    expect(normalizeCodeInput('a_b  c')).toBe('a-b-c');
  });
  it('does not truncate', () => {
    expect(normalizeCodeInput('x'.repeat(20))).toHaveLength(20);
  });
});

describe('suggestCode', () => {
  it.each([
    ['Mission Public Library', 'mission-public'],
    ['Café Réveille', 'cafe-reveille'],
    ['Bernal Heights Neighborhood Center', 'bernal-heights'],
    ['A', ''],
    ['!!!', ''],
    ['Noe Valley — Martha & Bros', 'noe-valley'],
    ['Supercalifragilisticexpialidocious', 'supercalifragili'],
    ['Tom & Jerry', 'tom-and-jerry'],
  ])('%s -> %j', (name, want) => {
    expect(suggestCode(name)).toBe(want);
  });
  it('always yields a valid code or empty', () => {
    for (const n of ['Mission Public Library', 'Café', 'x y', 'Hello World Wide Web Site']) {
      const s = suggestCode(n);
      expect(s === '' || validateCode(s) === null).toBe(true);
    }
  });
});

describe('uniqueCode', () => {
  it('returns the base when free', () => {
    expect(uniqueCode('mission', new Set())).toBe('mission');
  });
  it('appends the next free number', () => {
    expect(uniqueCode('mission', new Set(['mission']))).toBe('mission-2');
    expect(uniqueCode('mission', new Set(['mission', 'mission-2']))).toBe('mission-3');
  });
  it('shortens a 16-character base to stay within the limit', () => {
    const base = 'abcdefghijklmnop';
    const out = uniqueCode(base, new Set([base]));
    expect(out).toHaveLength(16);
    expect(out.endsWith('-2')).toBe(true);
    expect(validateCode(out)).toBeNull();
  });
  it('passes an empty base through', () => {
    expect(uniqueCode('', new Set(['']))).toBe('');
  });
});

describe('validateLocationForm', () => {
  const ok = { name: 'A', code: 'ab', address: '', notes: '' };
  it('accepts a minimal form', () => {
    expect(formIsValid(validateLocationForm(ok))).toBe(true);
  });
  it('enforces the limits', () => {
    expect(validateLocationForm({ ...ok, name: '  ' }).name).toBeTruthy();
    expect(validateLocationForm({ ...ok, name: 'n'.repeat(101) }).name).toBeTruthy();
    expect(validateLocationForm({ ...ok, name: 'n'.repeat(100) }).name).toBeUndefined();
    expect(validateLocationForm({ ...ok, code: 'A' }).code).toBeTruthy();
    expect(validateLocationForm({ ...ok, address: 'a'.repeat(301) }).address).toBeTruthy();
    expect(validateLocationForm({ ...ok, address: 'a'.repeat(300) }).address).toBeUndefined();
    expect(validateLocationForm({ ...ok, notes: 'a'.repeat(2001) }).notes).toBeTruthy();
    expect(validateLocationForm({ ...ok, notes: 'a'.repeat(2000) }).notes).toBeUndefined();
  });
});

describe('locationPatch', () => {
  const form = (o: Partial<{ name: string; code: string; address: string; notes: string }> = {}) => ({
    name: 'Mission Library',
    code: 'mission',
    address: '300 Main',
    notes: 'north board',
    ...o,
  });
  it('is null when nothing changed (whitespace aside)', () => {
    expect(locationPatch(loc(), form({ name: ' Mission Library ' }))).toBeNull();
  });
  it('carries only changed fields', () => {
    expect(locationPatch(loc(), form({ notes: 'new' }))).toEqual({ notes: 'new' });
    expect(locationPatch(loc(), form({ name: 'X', code: 'x1' }))).toEqual({ name: 'X', code: 'x1' });
  });
  it('omits an unchanged code on a locked location', () => {
    const locked = loc({ code_locked: true });
    expect(locationPatch(locked, form({ name: 'Renamed' }))).toEqual({ name: 'Renamed' });
  });
});

describe('small helpers', () => {
  it('labels status', () => {
    expect(locationStatusLabel(loc())).toBe('Active');
    expect(locationStatusLabel(loc({ active: false }))).toBe('Inactive');
  });
  it('delete and code editing follow the lock', () => {
    expect(canDelete(loc())).toBe(true);
    expect(canDelete(loc({ code_locked: true }))).toBe(false);
    expect(canEditCode(null)).toBe(true);
    expect(canEditCode(loc())).toBe(true);
    expect(canEditCode(loc({ code_locked: true }))).toBe(false);
  });
});
