// Shared application state. The SPA has no client-side router yet: navigation
// is a write to the `currentView` store, and App.svelte renders the matching
// view. #0014 replaces this wholesale with a History API path router.

import { writable } from 'svelte/store';
import type { User } from './types';

/** The set of top-level views App.svelte can render. */
export type View =
  | 'login'
  | 'account'
  | 'admin'
  | 'register-verify'  // magic-link registration landing
  | 'recover-verify';  // magic-link recovery landing

/**
 * The active view. Default is "login"; App.svelte flips it to "account" once
 * GET /api/me confirms a session. Navigation between sections is a store write.
 */
export const currentView = writable<View>('login');

/** The authenticated user (from GET /api/me), or null when signed out. */
export const currentUser = writable<User | null>(null);

/**
 * The magic-link token parsed from the landing URL (/register/verify or
 * /recover/verify). App.svelte sets this on load when it detects one of those
 * paths; the register-verify and recover-verify views read it on mount.
 * Cleared after the ceremony completes or fails.
 */
export const pendingVerifyToken = writable<string | null>(null);
