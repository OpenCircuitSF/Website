// API token helpers for the Settings → API tokens section (#0652). Pure
// logic, kept out of ApiTokens.svelte so it is testable without a DOM.
import { formatDateTime } from './admin';
import type { ApiToken } from './types';

/** The only scope a token can carry today. */
export const SCOPE_ENGAGEMENT_READ = 'engagement:read';

/** A readable label for a scope; an unknown scope is shown as itself. */
export function scopeLabel(scope: string): string {
  return scope === SCOPE_ENGAGEMENT_READ ? 'Read engagement' : scope;
}

/** Trims and checks a token name. */
export function validateTokenName(raw: string): { name: string } | { error: string } {
  const name = raw.trim();
  if (name === '') return { error: 'Enter a name for the token.' };
  if ([...name].length > 100) return { error: 'Use 100 characters or fewer.' };
  return { name };
}

/** The Last used cell. */
export function lastUsedLabel(t: Pick<ApiToken, 'last_used_at'>): string {
  return t.last_used_at === null ? 'Never' : formatDateTime(t.last_used_at);
}

/** The Status cell. */
export function tokenStatusLabel(t: Pick<ApiToken, 'revoked_at'>): string {
  return t.revoked_at === null ? 'Active' : `Revoked ${formatDateTime(t.revoked_at)}`;
}
