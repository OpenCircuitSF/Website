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
