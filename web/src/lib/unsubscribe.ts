// Pure, framework-free helpers backing Unsubscribe.svelte (#0036, PRD §6.5
// path 1). Kept out of the .svelte file so the "no error state" guarantee is
// unit-testable without a DOM (CLAUDE.md §1) -- see unsubscribe.test.ts.
// Mirrors subscribe.ts's convention of putting the logic here and leaving
// the component a thin shell.
//
// #0034's POST /api/unsubscribe answers 200 for EVERY input: a fresh valid
// token, an unknown/expired/replayed one, a missing token, and a
// still-complained row all return the same {message, no_op} shape and never
// a non-2xx status (see internal/handlers/unsubscribe.go's package doc
// comment, "Every input answers 200, including garbage"). There is
// therefore no server-reported error branch for this view to render --
// resolveDoneMessage below has no "invalid token" case, on purpose. The one
// failure this module still has to cover is the request never reaching the
// server at all (offline, DNS, a dropped connection); that is folded into
// the same neutral done state with a generic fallback message rather than a
// distinct error UI PRD §6.5's design deliberately leaves no room for.

/** Shown only when POST /api/unsubscribe never produced a response at all
 * (a network failure, not anything the server said) -- the one case with no
 * server message to fall back to. Worded to match the server's own
 * "if this address was on our list" phrasing (unsubscribe.go's default
 * neutral message for a missing/unknown token) so the two read as the same
 * kind of statement to whoever sees them. */
export const UNSUBSCRIBE_FALLBACK_MESSAGE =
  'If this address was on our list, it has been unsubscribed.';

/** The shape POST /api/unsubscribe always returns, on every input, at 200 --
 * mirrors internal/handlers/unsubscribe.go's unsubscribeResponse. */
export interface UnsubscribeResult {
  message: string;
  no_op: boolean;
}

/**
 * Decide the copy for the done state after the confirm click. There is
 * deliberately no branch here for "invalid token" or "already used" --
 * every server response (a fresh valid token, an unknown one, a replay, or a
 * complained no-op) reaches this same function and produces the same *kind*
 * of state, differing only in the wording the server itself already chose
 * (see the module doc comment). `result` is null only when the request
 * never got a response at all, which is the one case folded onto a fixed
 * fallback string instead of a message the server never sent.
 */
export function resolveDoneMessage(result: UnsubscribeResult | null): string {
  return result?.message ?? UNSUBSCRIBE_FALLBACK_MESSAGE;
}

/**
 * Extract the token query param for the /unsubscribe view, treating an
 * absent or blank token the same as no token at all (never throws). This
 * view has nothing to validate client-side either way: unsubscribe.go's
 * Post handles a missing token as its own neutral 200 branch, so the
 * confirm button fires the same request regardless. Pure -- takes
 * URLSearchParams rather than reading the router store directly, so it is
 * testable without importing router.ts (matches subscribe.ts's
 * DOM-injectable convention).
 */
export function extractToken(query: URLSearchParams): string | null {
  const token = query.get('token');
  return token && token.trim() !== '' ? token : null;
}
