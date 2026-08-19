// Pure, framework-free helpers backing SubscribeForm.svelte, PreferenceCenter.svelte,
// and ConfirmSubscription.svelte (#0029-#0031). Kept out of the .svelte files so the
// response-invariance guarantee, the UTM capture parsing, and the payload shaping are
// unit-testable without a DOM — see subscribe.test.ts. Mirrors admin.ts's
// "pure logic lives in a .ts module" convention.

/** The three UTM fields the wire contract carries (PRD §6.3). Always present in a
 * built request, defaulting to '' when nothing was captured. */
export interface UtmParams {
  utm_source: string;
  utm_medium: string;
  utm_campaign: string;
}

const EMPTY_UTM: UtmParams = { utm_source: '', utm_medium: '', utm_campaign: '' };

/** The sessionStorage key UTM attribution is cached under between the landing
 * pageview and the eventual subscribe submission (PRD §6.3: "SPA stores utm_* in
 * sessionStorage on first paint"). */
export const UTM_STORAGE_KEY = 'oc_utm';

/**
 * Parse utm_source/utm_medium/utm_campaign off a query string. Pure — no
 * storage access. A field absent from `search` is simply absent from the
 * result (not set to ''), so captureUtmParams below can tell "no UTM params on
 * this pageview" apart from "explicitly cleared".
 */
export function parseUtmParams(search: string): Partial<UtmParams> {
  const sp = new URLSearchParams(search);
  const out: Partial<UtmParams> = {};
  const source = sp.get('utm_source');
  const medium = sp.get('utm_medium');
  const campaign = sp.get('utm_campaign');
  if (source) out.utm_source = source;
  if (medium) out.utm_medium = medium;
  if (campaign) out.utm_campaign = campaign;
  return out;
}

/** The minimal Storage surface these helpers need from window.sessionStorage.
 * DOM-injectable (theme.ts's convention) so captureUtmParams/loadUtmParams are
 * unit-testable without jsdom. */
export interface UtmStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

/**
 * Capture UTM params from a landing URL's query string into storage, for a
 * subscribe submission that may happen many pageviews later. A pageview with
 * NO utm_* params leaves any earlier-captured attribution untouched — a
 * visitor clicking through from an Instagram link, then browsing to another
 * page with no query string, should not lose that attribution before they
 * get around to subscribing.
 */
export function captureUtmParams(search: string, storage: UtmStorage): void {
  const parsed = parseUtmParams(search);
  if (Object.keys(parsed).length === 0) return;
  storage.setItem(UTM_STORAGE_KEY, JSON.stringify(parsed));
}

/** Read back whatever UTM attribution captureUtmParams last stored, defaulting
 * every field to '' (never undefined) so buildSubscribeRequest's output always
 * has the shape the wire contract expects. Malformed/absent storage reads as
 * "nothing captured" rather than throwing. */
export function loadUtmParams(storage: UtmStorage): UtmParams {
  const raw = storage.getItem(UTM_STORAGE_KEY);
  if (!raw) return { ...EMPTY_UTM };
  try {
    const parsed = JSON.parse(raw) as Partial<UtmParams>;
    return {
      utm_source: parsed.utm_source ?? '',
      utm_medium: parsed.utm_medium ?? '',
      utm_campaign: parsed.utm_campaign ?? '',
    };
  } catch {
    return { ...EMPTY_UTM };
  }
}

/** The POST /api/subscribe wire body (internal/handlers/subscribe.go's
 * subscribeRequest). */
export interface SubscribeRequestBody {
  email: string;
  interests: string[];
  website: string;
  rendered_at: number;
  utm_source: string;
  utm_medium: string;
  utm_campaign: string;
}

/** The fields SubscribeForm.svelte's local state supplies to build a request. */
export interface SubscribePayloadInput {
  email: string;
  interests: string[];
  /** The honeypot field's current value — always '' from a real visitor. */
  website: string;
  /** Date.now() captured when the form mounted, unix milliseconds, for the
   * server's timing gate (PRD §6.3). */
  renderedAt: number;
  utm: UtmParams;
}

/** Shape a SubscribeRequestBody from form state. Pure — trims the email, passes
 * everything else through unchanged. */
export function buildSubscribeRequest(input: SubscribePayloadInput): SubscribeRequestBody {
  return {
    email: input.email.trim(),
    interests: input.interests,
    website: input.website,
    rendered_at: input.renderedAt,
    utm_source: input.utm.utm_source,
    utm_medium: input.utm.utm_medium,
    utm_campaign: input.utm.utm_campaign,
  };
}

/** Where a successful POST /api/subscribe sends the visitor. */
export const SUBSCRIBE_SUCCESS_ROUTE = '/subscribe/thanks';

/**
 * Decide the post-submit navigation target for a successful subscribe
 * response. Deliberately ignores `_response` entirely and returns a fixed
 * constant: #0026's uniform 202 guarantees every successful branch (new
 * signup, existing active/pending/unsubscribed subscriber, a suppressed
 * address) returns a byte-identical body, and reading that body here would be
 * the one way this component could reintroduce a distinction the server just
 * eliminated. See subscribe.test.ts's response-invariance assertion.
 */
export function onSubscribeSuccess(_response: unknown): string {
  return SUBSCRIBE_SUCCESS_ROUTE;
}

/**
 * Toggle a slug's membership in a selection set, returning a NEW Set (never
 * mutating the input) so callers can safely reassign Svelte $state after
 * calling this. Shared by SubscribeForm's and PreferenceCenter's checkbox
 * grids.
 */
export function toggleSlug(selected: ReadonlySet<string>, slug: string): Set<string> {
  const next = new Set(selected);
  if (next.has(slug)) {
    next.delete(slug);
  } else {
    next.add(slug);
  }
  return next;
}

/** The PATCH /api/preferences wire body (internal/handlers/preferences.go's
 * patchPreferencesRequest). */
export interface PreferencesPatchBody {
  token: string;
  interests?: string[];
  unsubscribe?: boolean;
}

/** Build the "save my interest selection" PATCH body. */
export function buildSaveInterestsPatch(token: string, selected: Iterable<string>): PreferencesPatchBody {
  return { token, interests: Array.from(selected) };
}

/** Build the "Unsubscribe from everything" PATCH body — the explicit, separate
 * action #0031 requires alongside interest editing (never inferred from an
 * empty interest selection; see preferences.go's package doc comment). */
export function buildUnsubscribeEverythingPatch(token: string): PreferencesPatchBody {
  return { token, unsubscribe: true };
}

/**
 * Copy for PreferenceCenter.svelte's non-active panel (#0031 review finding
 * 1): shown in place of the interest editor whenever GET /api/preferences
 * (or a stale in-session status after a PATCH) reports anything other than
 * `active`, so the page never implies someone is receiving mail they are
 * not. Matches the status strings internal/subscribers/store.go's Status*
 * constants produce on the wire (pending | active | unsubscribed | bounced |
 * complained) — `active` itself is never passed in (the caller checks
 * `status === 'active'` first and renders the normal editor instead), kept
 * here anyway as a safe fallback rather than an unreachable-branch throw.
 *
 * #0090: `complained` reuses the same honest copy
 * internal/handlers/preferences.go's patchUnsubscribe already returns for a
 * complained row's no-op PATCH (see COMPLAINED_UNSUBSCRIBE_NO_OP_MESSAGE
 * below) rather than the vague "isn't currently subscribed" text every
 * other non-active status gets. The vague text used to invite exactly the
 * dead end #0090 closes: "Subscribe again" led to the public form, which
 * gives #0026's uniform 202 for a confirmation email existingSignup's
 * StatusComplained branch never sends. The honest copy explains why and
 * names the only real path (contact us) instead.
 */
export function inactiveStatusMessage(status: string): string {
  switch (status) {
    case 'unsubscribed':
      return "You're not currently subscribed. You can resubscribe anytime.";
    case 'complained':
      return COMPLAINED_NO_RESUBSCRIBE_MESSAGE;
    case 'bounced':
      return "We haven't been able to deliver to this address, so it's not currently subscribed.";
    case 'pending':
      return 'Your subscription is still waiting on email confirmation. Check your inbox for the confirmation link, or subscribe again below.';
    case 'active':
      return "You're subscribed.";
    default:
      return "You're not currently subscribed.";
  }
}

/**
 * The honest copy for a `complained` subscriber, shared by
 * inactiveStatusMessage above (#0090) and matching, word for word, the
 * no-op message internal/handlers/preferences.go's patchUnsubscribe already
 * returns for a PATCH {unsubscribe: true} against an already-complained row
 * (see that file's `message` assignment in patchUnsubscribe). Keeping one
 * exported constant rather than two independently-written strings is what
 * keeps the "same copy" guarantee mechanical rather than a thing that
 * quietly drifts the next time either string is edited.
 */
export const COMPLAINED_NO_RESUBSCRIBE_MESSAGE =
  "This address is marked as having complained about a previous email, and complained addresses " +
  "can't be unsubscribed or resubscribed from this page. Contact us if you believe this is a mistake.";

/**
 * Statuses for which PreferenceCenter.svelte's non-active panel offers the
 * "Subscribe again" affordance (#0090). `pending`, `unsubscribed` and
 * `bounced` can all legitimately leave that status by submitting the public
 * form again — `active` never reaches this panel at all (isActive routes it
 * to the editor instead). `complained` is deliberately excluded: per
 * CLAUDE.md §9, "complained subscribers never auto-resubscribe" — only an
 * admin clears that state — and the public POST /api/subscribe form cannot
 * do it either (existingSignup's StatusComplained branch sends no
 * confirmation email), so the button would offer a path that goes nowhere
 * while #0026's uniform 202 keeps that failure invisible to the visitor.
 */
export function showSubscribeAgainAffordance(status: string): boolean {
  return status !== 'complained';
}
