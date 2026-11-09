import { describe, expect, it } from 'vitest';
import { formatDateTime } from './admin';
import { lastUsedLabel, scopeLabel, tokenStatusLabel, validateTokenName } from './apiTokens';

describe('apiTokens (#0652)', () => {
  it('labels engagement:read as Read engagement', () => {
    expect(scopeLabel('engagement:read')).toBe('Read engagement');
  });
  it('passes an unknown scope through', () => {
    expect(scopeLabel('other:read')).toBe('other:read');
  });
  it('rejects an empty or whitespace name', () => {
    expect(validateTokenName('')).toEqual({ error: 'Enter a name for the token.' });
    expect(validateTokenName('   ')).toEqual({ error: 'Enter a name for the token.' });
  });
  it('rejects a name over 100 characters', () => {
    expect(validateTokenName('a'.repeat(101))).toEqual({ error: 'Use 100 characters or fewer.' });
    expect(validateTokenName('a'.repeat(100))).toEqual({ name: 'a'.repeat(100) });
  });
  it('trims a valid name', () => {
    expect(validateTokenName('  Home Assistant ')).toEqual({ name: 'Home Assistant' });
  });
  it('shows Never for a token never used', () => {
    expect(lastUsedLabel({ last_used_at: null })).toBe('Never');
    const at = '2026-11-08T20:00:00Z';
    expect(lastUsedLabel({ last_used_at: at })).toBe(formatDateTime(at));
  });
  it('shows Active or Revoked with the date', () => {
    expect(tokenStatusLabel({ revoked_at: null })).toBe('Active');
    const at = '2026-11-08T20:00:00Z';
    expect(tokenStatusLabel({ revoked_at: at })).toBe(`Revoked ${formatDateTime(at)}`);
  });
});
