// API token helpers for the Settings → API tokens section (#0652, #0674).
// Pure logic, kept out of ApiTokens.svelte so it is testable without a DOM.
import { formatDateTime } from './admin';
import type { ApiToken } from './types';

/** Every scope a token may carry; mirrors apitokens.KnownScopes in Go. */
export const SCOPE_ENGAGEMENT_READ = 'engagement:read';
export const SCOPE_GUIDES_READ = 'guides:read';
export const SCOPE_GUIDE_POSTS_READ = 'guide_posts:read';
export const SCOPE_GUIDE_POSTS_WRITE = 'guide_posts:write';
export const SCOPE_EVENTS_READ = 'events:read';
export const SCOPE_EVENTS_WRITE = 'events:write';
export const SCOPE_VENUES_READ = 'venues:read';

export interface ScopeGroup {
  resource: string;
  scopes: { scope: string; label: string }[];
}

/** The create form's checkboxes, grouped by resource. */
export const SCOPE_GROUPS: ScopeGroup[] = [
  { resource: 'Engagement', scopes: [{ scope: SCOPE_ENGAGEMENT_READ, label: 'Read engagement' }] },
  { resource: 'Guides', scopes: [{ scope: SCOPE_GUIDES_READ, label: 'Read guides' }] },
  {
    resource: 'Guide posts',
    scopes: [
      { scope: SCOPE_GUIDE_POSTS_READ, label: 'Read guide posts' },
      { scope: SCOPE_GUIDE_POSTS_WRITE, label: 'Write guide posts' },
    ],
  },
  {
    resource: 'Events',
    scopes: [
      { scope: SCOPE_EVENTS_READ, label: 'Read events' },
      { scope: SCOPE_EVENTS_WRITE, label: 'Write events' },
    ],
  },
  { resource: 'Venues', scopes: [{ scope: SCOPE_VENUES_READ, label: 'Read venues' }] },
];

/** Every known scope, in SCOPE_GROUPS order. */
export const KNOWN_SCOPES: string[] = SCOPE_GROUPS.flatMap((g) => g.scopes.map((s) => s.scope));

/** "Agent (all guide and event scopes)": what an MCP client needs. */
export const AGENT_PRESET: string[] = [
  SCOPE_GUIDES_READ,
  SCOPE_GUIDE_POSTS_READ,
  SCOPE_GUIDE_POSTS_WRITE,
  SCOPE_EVENTS_READ,
  SCOPE_EVENTS_WRITE,
  SCOPE_VENUES_READ,
];

/** A readable label for a scope; an unknown scope is shown as itself. */
export function scopeLabel(scope: string): string {
  for (const g of SCOPE_GROUPS) {
    const hit = g.scopes.find((s) => s.scope === scope);
    if (hit) return hit.label;
  }
  return scope;
}

/** Puts the chosen scopes in KNOWN_SCOPES order, without duplicates. */
export function orderScopes(chosen: Iterable<string>): string[] {
  const set = new Set(chosen);
  return KNOWN_SCOPES.filter((s) => set.has(s));
}

/** The expiry select's options; 90 days is the default. */
export type ExpiryChoice = '7' | '30' | '90' | '365' | 'never' | 'custom';
export const DEFAULT_EXPIRY: ExpiryChoice = '90';
export const EXPIRY_OPTIONS: { value: ExpiryChoice; label: string }[] = [
  { value: '7', label: '7 days' },
  { value: '30', label: '30 days' },
  { value: '90', label: '90 days' },
  { value: '365', label: '1 year' },
  { value: 'never', label: 'Never' },
  { value: 'custom', label: 'Custom date' },
];

/**
 * The expires_at to send for a choice: an ISO timestamp, or null for never.
 * A custom date (YYYY-MM-DD) expires at the end of that day in the browser's
 * time zone, and must be after now.
 */
export function expiryFromChoice(
  choice: ExpiryChoice,
  customDate: string,
  now: Date,
): { expiresAt: string | null } | { error: string } {
  if (choice === 'never') return { expiresAt: null };
  if (choice === 'custom') {
    const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(customDate);
    if (!m) return { error: 'Pick an expiry date.' };
    const at = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]), 23, 59, 59);
    if (Number.isNaN(at.getTime()) || at.getTime() <= now.getTime()) {
      return { error: 'Pick an expiry date in the future.' };
    }
    return { expiresAt: at.toISOString() };
  }
  return { expiresAt: new Date(now.getTime() + Number(choice) * 86_400_000).toISOString() };
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

/** Whether a token's expiry has passed (the server refuses it from then on). */
export function isExpired(t: Pick<ApiToken, 'expires_at'>, now: Date = new Date()): boolean {
  return t.expires_at != null && new Date(t.expires_at).getTime() <= now.getTime();
}

/** The Expires cell. */
export function expiresLabel(t: Pick<ApiToken, 'expires_at'>): string {
  return t.expires_at == null ? 'Never' : formatDateTime(t.expires_at);
}

/** The Status cell: revoked wins over expired. */
export function tokenStatusLabel(t: Pick<ApiToken, 'revoked_at' | 'expires_at'>, now: Date = new Date()): string {
  if (t.revoked_at !== null) return `Revoked ${formatDateTime(t.revoked_at)}`;
  if (isExpired(t, now)) return `Expired ${formatDateTime(t.expires_at as string)}`;
  return 'Active';
}
