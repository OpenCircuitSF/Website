// Shared application state that is NOT navigation. Navigation is now a
// History API path router (web/src/lib/router.ts, #0014) -- `currentRoute`
// there is what App.svelte and every view read to know what's on screen.
// This file used to also hold a `currentView` store that App.svelte wrote to
// as its navigation mechanism; #0014 retired it (PRD §3.4, §7.2: real URLs
// are a hard requirement for pages that get shared and indexed). What
// remains here is genuinely transient UI state that outlives any one route.

import { writable } from 'svelte/store';
import type { User } from './types';

/** The authenticated user (from GET /api/me), or null when signed out. */
export const currentUser = writable<User | null>(null);

/**
 * The magic-link token parsed from the landing URL (/register/verify or
 * /recover/verify). App.svelte sets this on load when it detects one of those
 * paths; the register-verify and recover-verify views read it on mount.
 * Cleared after the ceremony completes or fails.
 */
export const pendingVerifyToken = writable<string | null>(null);
