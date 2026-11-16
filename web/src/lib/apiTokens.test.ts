import { describe, expect, it } from 'vitest';
import { formatDateTime } from './admin';
import {
  AGENT_PRESET,
  KNOWN_SCOPES,
  expiresLabel,
  expiryFromChoice,
  isExpired,
  lastUsedLabel,
  orderScopes,
  scopeLabel,
  tokenStatusLabel,
  validateTokenName,
} from './apiTokens';

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
    expect(tokenStatusLabel({ revoked_at: null, expires_at: null })).toBe('Active');
    const at = '2026-11-08T20:00:00Z';
    expect(tokenStatusLabel({ revoked_at: at, expires_at: null })).toBe(`Revoked ${formatDateTime(at)}`);
  });
});

describe('apiTokens v2 (#0674)', () => {
  const now = new Date('2026-11-15T12:00:00Z');
  it('knows the seven scopes the server knows', () => {
    expect(KNOWN_SCOPES).toEqual([
      'engagement:read',
      'guides:read',
      'guide_posts:read',
      'guide_posts:write',
      'events:read',
      'events:write',
      'venues:read',
    ]);
  });
  it('the Agent preset is every guide and event scope, not engagement', () => {
    expect(AGENT_PRESET).not.toContain('engagement:read');
    expect(AGENT_PRESET).toContain('guide_posts:write');
    expect(AGENT_PRESET).toContain('events:write');
  });
  it('labels write scopes', () => {
    expect(scopeLabel('events:write')).toBe('Write events');
  });
  it('orders and de-duplicates chosen scopes', () => {
    expect(orderScopes(['events:write', 'guides:read', 'events:write', 'bogus'])).toEqual(['guides:read', 'events:write']);
  });
  it('turns a preset into now + N days', () => {
    expect(expiryFromChoice('7', '', now)).toEqual({ expiresAt: '2026-11-22T12:00:00.000Z' });
    expect(expiryFromChoice('365', '', now)).toEqual({ expiresAt: '2027-11-15T12:00:00.000Z' });
  });
  it('never is null', () => {
    expect(expiryFromChoice('never', '', now)).toEqual({ expiresAt: null });
  });
  it('a custom date is end of day local, and must be in the future', () => {
    expect(expiryFromChoice('custom', '2027-01-02', now)).toEqual({
      expiresAt: new Date(2027, 0, 2, 23, 59, 59).toISOString(),
    });
    expect(expiryFromChoice('custom', '', now)).toEqual({ error: 'Pick an expiry date.' });
    expect(expiryFromChoice('custom', '2020-01-01', now)).toEqual({ error: 'Pick an expiry date in the future.' });
  });
  it('marks expiry and shows Expired in the status', () => {
    const past = '2026-11-15T11:00:00Z';
    expect(isExpired({ expires_at: past }, now)).toBe(true);
    expect(isExpired({ expires_at: '2026-11-15T12:00:00Z' }, now)).toBe(true);
    expect(isExpired({ expires_at: '2026-11-16T12:00:00Z' }, now)).toBe(false);
    expect(isExpired({ expires_at: null }, now)).toBe(false);
    expect(tokenStatusLabel({ revoked_at: null, expires_at: past }, now)).toBe(`Expired ${formatDateTime(past)}`);
    expect(tokenStatusLabel({ revoked_at: past, expires_at: past }, now)).toBe(`Revoked ${formatDateTime(past)}`);
    expect(expiresLabel({ expires_at: null })).toBe('Never');
    expect(expiresLabel({ expires_at: past })).toBe(formatDateTime(past));
  });
});
