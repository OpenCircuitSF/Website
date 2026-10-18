// Pure logic for the Outreach connections area of admin Settings. Kept out of
// the component so it is unit-testable without a DOM.

import type { ConnectionField, ConnectionGroup, ConnectionSource } from './types';

export const GROUP_LABELS: Record<ConnectionGroup['name'], string> = {
  shortlinks: 'ShortLinks',
  bluesky: 'Bluesky',
  mastodon: 'Mastodon',
};

export const FIELD_LABELS: Record<string, { label: string; placeholder: string }> = {
  shortlinks_base_url: { label: 'Base URL', placeholder: 'https://go.opencircuitsf.com' },
  shortlinks_api_token: { label: 'API token', placeholder: '' },
  bluesky_handle: { label: 'Handle', placeholder: 'opencircuitsf.bsky.social' },
  bluesky_app_password: { label: 'App password', placeholder: '' },
  bluesky_pds_url: { label: 'PDS URL', placeholder: 'https://bsky.social' },
  mastodon_base_url: { label: 'Instance URL', placeholder: 'https://mastodon.social' },
  mastodon_token: { label: 'Access token', placeholder: '' },
};

export const PRECEDENCE_NOTE =
  'A value saved here takes effect immediately and overrides the matching environment variable. Clear a saved value to fall back to the environment variable.';

/** Where a field's effective value comes from, in words. */
export function sourceLabel(f: { source: ConnectionSource; env_var: string }): string {
  switch (f.source) {
    case 'database':
      return 'Saved in Settings';
    case 'environment':
      return `From environment variable ${f.env_var}`;
    case 'default':
      return 'Default';
    default:
      return 'Not set';
  }
}

/** A secret field's status line: set or not, and from where. Never the value. */
export function secretStatus(f: ConnectionField): string {
  if (f.secret && !f.set) return 'Not set';
  if (f.source === 'database') return 'Set — saved in Settings';
  if (f.source === 'environment') return `Set — from environment variable ${f.env_var}`;
  return 'Set';
}

/** The starting draft for a group: saved non-secret values only, secrets always blank. */
export function initialDraft(group: ConnectionGroup): Record<string, string> {
  const draft: Record<string, string> = {};
  for (const f of group.fields) {
    draft[f.key] = !f.secret && f.source === 'database' ? f.value : '';
  }
  return draft;
}

/**
 * The PATCH values for a group's edited draft. A non-secret field is sent when
 * its trimmed draft differs from the starting one; blank means clear, and is
 * only sent when there is a saved value to clear. A secret is sent only when
 * something was typed.
 */
export function buildPatch(group: ConnectionGroup, draft: Record<string, string>): Record<string, string> {
  const start = initialDraft(group);
  const patch: Record<string, string> = {};
  for (const f of group.fields) {
    const typed = (draft[f.key] ?? '').trim();
    if (f.secret) {
      if (typed !== '') patch[f.key] = typed;
      continue;
    }
    if (typed === (start[f.key] ?? '')) continue;
    if (typed === '' && f.source !== 'database') continue;
    patch[f.key] = typed;
  }
  return patch;
}

/** A client-side hint mirroring the server's URL rule. The server stays authoritative. */
export function validateServiceUrl(s: string): string | null {
  const v = s.trim();
  if (v === '') return null;
  const bad = 'Must be an https URL (http is allowed only for localhost and 127.0.0.1).';
  if (v.length > 2048 || v.includes('#')) return bad;
  let u: URL;
  try {
    u = new URL(v);
  } catch {
    return bad;
  }
  if (u.username !== '' || u.password !== '' || u.search !== '' || v.endsWith('?')) return bad;
  if (u.protocol === 'https:') return null;
  if (u.protocol === 'http:' && (u.hostname === 'localhost' || u.hostname === '127.0.0.1')) return null;
  return bad;
}
