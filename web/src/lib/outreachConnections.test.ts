import { describe, expect, it } from 'vitest';
import {
  buildPatch,
  initialDraft,
  secretStatus,
  sourceLabel,
  validateServiceUrl,
} from './outreachConnections';
import type { ConnectionGroup } from './types';

function mastodon(over: { url?: 'database' | 'environment' | 'unset'; token?: 'database' | 'environment' | 'unset' } = {}): ConnectionGroup {
  const url = over.url ?? 'database';
  const token = over.token ?? 'environment';
  return {
    name: 'mastodon',
    configured: true,
    error: '',
    fields: [
      { key: 'mastodon_base_url', env_var: 'MASTODON_BASE_URL', secret: false, source: url, value: url === 'unset' ? '' : 'https://m.example' },
      { key: 'mastodon_token', env_var: 'MASTODON_TOKEN', secret: true, source: token, set: token !== 'unset' },
    ],
  };
}

describe('buildPatch', () => {
  it('sends nothing for an untouched draft', () => {
    const g = mastodon();
    expect(buildPatch(g, initialDraft(g))).toEqual({});
  });
  it('includes a typed secret and omits a blank one', () => {
    const g = mastodon();
    const d = initialDraft(g);
    d.mastodon_token = '  tok-fake  ';
    expect(buildPatch(g, d)).toEqual({ mastodon_token: 'tok-fake' });
    d.mastodon_token = '   ';
    expect(buildPatch(g, d)).toEqual({});
  });
  it('clears a database-sourced URL when it is emptied', () => {
    const g = mastodon();
    const d = initialDraft(g);
    d.mastodon_base_url = '';
    expect(buildPatch(g, d)).toEqual({ mastodon_base_url: '' });
  });
  it('sends nothing when an environment-sourced field is emptied', () => {
    const g = mastodon({ url: 'environment' });
    const d = initialDraft(g);
    expect(d.mastodon_base_url).toBe('');
    expect(buildPatch(g, d)).toEqual({});
  });
  it('sends a newly typed URL for an environment-sourced field', () => {
    const g = mastodon({ url: 'environment' });
    const d = initialDraft(g);
    d.mastodon_base_url = ' https://other.example ';
    expect(buildPatch(g, d)).toEqual({ mastodon_base_url: 'https://other.example' });
  });
});

describe('labels', () => {
  it('words the source of a field', () => {
    const [url, token] = mastodon({ url: 'environment' }).fields;
    expect(sourceLabel(url)).toBe('From environment variable MASTODON_BASE_URL');
    expect(sourceLabel(mastodon().fields[0])).toBe('Saved in Settings');
    expect(secretStatus(token)).toBe('Set — from environment variable MASTODON_TOKEN');
    expect(secretStatus(mastodon({ token: 'database' }).fields[1])).toBe('Set — saved in Settings');
    expect(secretStatus(mastodon({ token: 'unset' }).fields[1])).toBe('Not set');
    expect(sourceLabel(mastodon({ url: 'unset' }).fields[0])).toBe('Not set');
  });
});

describe('validateServiceUrl', () => {
  it('accepts what the server accepts', () => {
    for (const s of ['', 'https://x.org', 'https://x.org/path/', 'http://localhost:5173', 'http://127.0.0.1:8080']) {
      expect(validateServiceUrl(s), s).toBeNull();
    }
  });
  it('rejects what the server rejects', () => {
    const long = 'https://x.org/' + 'a'.repeat(2049);
    for (const s of [
      'http://example.com',
      'http://localhost.evil.com',
      'ftp://x.org',
      'https://',
      'https://u:p@x.org',
      'https://x.org/?q=1',
      'https://x.org/#f',
      long,
      'not a url',
    ]) {
      expect(validateServiceUrl(s), s.slice(0, 40)).not.toBeNull();
    }
  });
});
