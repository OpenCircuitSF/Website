// Typed fetch wrapper for the Open Circuit SF API. All requests are same-origin
// (the Vite dev server proxies /api, /auth, /account, /admin to the Go service;
// in production the Go binary serves the SPA itself), send the session cookie
// via `credentials: 'include'`, and throw a typed ApiError on a non-2xx response.

import type {
  User,
  Credential,
  AuditEntry,
  AdminUser,
  Setting,
  Interest,
  Subscriber,
  SubscribersPage,
  Suppression,
  PublicInterest,
  ConfirmResponse,
  PreferencesResponse,
  Campaign,
  PreflightResponse,
  AudiencePreviewResponse,
  CampaignPreviewResponse,
  CampaignStatsResponse,
  WorkshopsListResponse,
} from './types';
import type { SubscribeRequestBody, PreferencesPatchBody } from './subscribe';
import type { UnsubscribeResult } from './unsubscribe';
import type {
  ServerCredentialAssertion,
  AssertionFinishPayload,
  ServerCredentialCreation,
  AttestationFinishPayload,
} from './webauthn';

/**
 * Thrown by every helper on a non-2xx response. Carries the HTTP status and, when
 * the body was JSON, its parsed shape (the API returns `{error: "..."}` on
 * failures) so callers can branch on `status` (e.g. 401 → show login) or read a
 * machine-readable `error` code.
 */
export class ApiError extends Error {
  readonly status: number;
  readonly body: unknown;

  constructor(status: number, message: string, body: unknown) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.body = body;
  }
}

/** Shape of the JSON error body the API returns on failures. */
interface ErrorBody {
  error?: string;
  message?: string;
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const init: RequestInit = {
    method,
    credentials: 'include',
    headers: { Accept: 'application/json' },
  };
  if (body !== undefined) {
    init.headers = { ...init.headers, 'Content-Type': 'application/json' };
    init.body = JSON.stringify(body);
  }

  const res = await fetch(path, init);

  // 204 No Content (and any empty body) parses to undefined.
  const text = await res.text();
  let parsed: unknown = undefined;
  if (text.length > 0) {
    try {
      parsed = JSON.parse(text);
    } catch {
      parsed = text;
    }
  }

  if (!res.ok) {
    const err = parsed as ErrorBody | undefined;
    const message = err?.error ?? err?.message ?? `HTTP ${res.status}`;
    throw new ApiError(res.status, message, parsed);
  }

  return parsed as T;
}

/** GET a JSON resource. */
export function apiGet<T>(path: string): Promise<T> {
  return request<T>('GET', path);
}

/** POST a JSON body and parse the JSON response. */
export function apiPost<T>(path: string, body?: unknown): Promise<T> {
  return request<T>('POST', path, body);
}

/** PATCH a JSON body and parse the JSON response. */
export function apiPatch<T>(path: string, body?: unknown): Promise<T> {
  return request<T>('PATCH', path, body);
}

/** DELETE a resource and parse any JSON response. */
export function apiDelete<T>(path: string): Promise<T> {
  return request<T>('DELETE', path);
}

// ── Endpoint helpers ────────────────────────────────────────────────────────
// Thin, typed wrappers over the routes the views call.

/** GET /api/me — current user profile; throws ApiError(401) when unauthenticated. */
export function getMe(): Promise<User> {
  return apiGet<User>('/api/me');
}

/** POST /auth/logout — invalidate the current session. */
export function logout(): Promise<void> {
  return apiPost<void>('/auth/logout');
}

/**
 * POST /auth/logout/all — "sign out everywhere": revokes every session for the
 * caller's account, including this one, on every device and client (browser,
 * iPhone app). It never touches enrolled passkeys — those are what the next
 * sign-in uses. Returns the number of sessions revoked.
 */
export function logoutAll(): Promise<{ message: string; revoked_count: number }> {
  return apiPost<{ message: string; revoked_count: number }>('/auth/logout/all');
}

// ── Auth ceremony (login + registration entry) ──────────────────────────────

/**
 * GET /auth/login/start — issue a WebAuthn assertion challenge. An optional
 * email narrows the prompt via `allowCredentials`; absent it the server issues a
 * discoverable (conditional-UI) challenge. The response is the
 * `CredentialAssertion` JSON (`{publicKey, mediation?}`) the browser glue
 * decodes; the shape is identical whether or not the email is registered, so it
 * never leaks account existence.
 */
export function loginStart(email?: string): Promise<ServerCredentialAssertion> {
  const trimmed = email?.trim();
  const path = trimmed
    ? `/auth/login/start?email=${encodeURIComponent(trimmed)}`
    : '/auth/login/start';
  return apiGet<ServerCredentialAssertion>(path);
}

/**
 * POST /auth/login/finish — submit the serialized assertion. On success the
 * server sets the session cookie and returns `{user_id}`; a deactivated account
 * yields ApiError(403) and any verification failure yields ApiError(401).
 */
export function loginFinish(
  assertion: AssertionFinishPayload,
): Promise<{ user_id: number }> {
  return apiPost<{ user_id: number }>('/auth/login/finish', assertion);
}

/**
 * POST /auth/register/start — submit an email to begin registration. The server
 * returns a generic `{message}` (200) whether or not the email is already
 * registered, or ApiError(403) when registration is closed.
 */
export function registerStart(email: string): Promise<{ message: string }> {
  return apiPost<{ message: string }>('/auth/register/start', { email });
}

/**
 * GET /auth/register/verify?token=… — exchange the magic-link token for
 * WebAuthn `PublicKeyCredentialCreationOptions`. Called by the register-verify
 * landing view after the SPA loads. Returns ApiError(400/401/410) for invalid
 * or expired tokens; ApiError(404) if the session challenge has expired.
 */
export function registerVerify(token: string): Promise<ServerCredentialCreation> {
  return apiGet<ServerCredentialCreation>(
    `/auth/register/verify?token=${encodeURIComponent(token)}`,
  );
}

/**
 * POST /auth/register/finish?token=…&device_name=… — submit the serialized
 * attestation to complete registration. The token and optional device_name are
 * query parameters (the handler reads them from the URL so the raw attestation
 * body is passed untouched to go-webauthn). On success the server sets the
 * session cookie and returns `{id, email, is_admin}`. Errors: 400 for
 * invalid/expired token or failed attestation.
 *
 * See: internal/handlers/auth.go (RegisterFinish).
 */
export function registerFinish(
  token: string,
  attestation: AttestationFinishPayload,
  deviceName?: string,
): Promise<{ id: number; email: string; is_admin: boolean }> {
  let path = `/auth/register/finish?token=${encodeURIComponent(token)}`;
  if (deviceName) {
    path += `&device_name=${encodeURIComponent(deviceName)}`;
  }
  return apiPost<{ id: number; email: string; is_admin: boolean }>(path, attestation);
}

/**
 * POST /auth/recover — submit an email to begin account recovery. The server
 * returns a generic `{message}` (200) whether or not the email is registered,
 * so this endpoint never leaks account existence. Called by the Login view's
 * recover sub-form.
 */
export function recoverStart(email: string): Promise<{ message: string }> {
  return apiPost<{ message: string }>('/auth/recover', { email });
}

/**
 * GET /auth/recover/verify?token=… — exchange the recovery magic-link token
 * for WebAuthn `PublicKeyCredentialCreationOptions`. Called by the
 * recover-verify landing view. The response shape is identical to
 * `registerVerify`; the helpers in `webauthn.ts` are reused verbatim.
 */
export function recoverVerify(token: string): Promise<ServerCredentialCreation> {
  return apiGet<ServerCredentialCreation>(
    `/auth/recover/verify?token=${encodeURIComponent(token)}`,
  );
}

/**
 * POST /auth/recover/finish?token=…&device_name=… — submit the serialized
 * attestation to complete account recovery. The token and optional device_name
 * are query parameters (the handler reads them from the URL so the raw
 * attestation body is passed untouched to go-webauthn). On success the server
 * adds the new credential to the EXISTING account, sets the session cookie,
 * and returns `{user_id}` (differs from `registerFinish` which returns
 * `{id, email, is_admin}`). Errors: 400 for invalid/expired token or failed
 * attestation.
 *
 * See: internal/handlers/auth.go (RecoverFinish), internal/auth/recovery.go
 * (FinishRecovery).
 */
export function recoverFinish(
  token: string,
  attestation: AttestationFinishPayload,
  deviceName?: string,
): Promise<{ user_id: number }> {
  let path = `/auth/recover/finish?token=${encodeURIComponent(token)}`;
  if (deviceName) {
    path += `&device_name=${encodeURIComponent(deviceName)}`;
  }
  return apiPost<{ user_id: number }>(path, attestation);
}

// ── Account (passkeys) ──────────────────────────────────────────────────────

/** GET /account/credentials — the caller's registered passkeys. */
export function listCredentials(): Promise<Credential[]> {
  return apiGet<Credential[]>('/account/credentials');
}

/** PATCH /account/credentials/{id} — rename a passkey. */
export function renameCredential(id: number, deviceName: string): Promise<Credential> {
  return apiPatch<Credential>(`/account/credentials/${id}`, { device_name: deviceName });
}

/** DELETE /account/credentials/{id} — revoke a passkey. */
export function revokeCredential(id: number): Promise<{ message: string }> {
  return apiDelete<{ message: string }>(`/account/credentials/${id}`);
}

// ── Admin ───────────────────────────────────────────────────────────────────

/** GET /admin/users — all accounts (admin only). */
export function listUsers(): Promise<{ users: AdminUser[] }> {
  return apiGet<{ users: AdminUser[] }>('/admin/users');
}

/**
 * POST /admin/users/{id}/deactivate — deactivate a non-admin user (admin only).
 * `reason` is one of the six deactivation reason values; `note` is required by
 * the server when reason is `other`. Returns the updated user.
 */
export function deactivateUser(id: number, reason: string, note: string): Promise<AdminUser> {
  return apiPost<AdminUser>(`/admin/users/${id}/deactivate`, { reason, note });
}

/**
 * POST /admin/users/{id}/reactivate — reactivate a user (admin only). `note` is
 * optional and recorded in the account.reactivated audit metadata. Returns the
 * updated user.
 */
export function reactivateUser(id: number, note: string): Promise<AdminUser> {
  return apiPost<AdminUser>(`/admin/users/${id}/reactivate`, { note });
}

/** GET /admin/settings — runtime settings (admin only). */
export function getSettings(): Promise<{ settings: Setting[] }> {
  return apiGet<{ settings: Setting[] }>('/admin/settings');
}

/** PATCH /admin/settings — update a runtime setting (admin only). */
export function updateSetting(key: string, value: string): Promise<unknown> {
  return apiPatch<unknown>('/admin/settings', { key, value });
}

/** Envelope returned by GET /admin/audit. */
export interface AuditPage {
  audit_log: AuditEntry[];
  total: number;
  page: number;
  per_page: number;
}

/**
 * GET /admin/audit — paginated audit log (admin only), newest-first. `userId`
 * narrows via `?user_id=`. `targetType`/`targetId` narrow via `?target_type=`/
 * `?target_id=` (#0114) — the pair a campaign's demoted-banner and stats
 * screen deep-link with (lib/admin.ts's `campaignAuditFilter`) to reach rows
 * a user_id filter can't, like #0045's NULL-actor `send_refused` row. Passing
 * `targetId` without `targetType` mirrors the server's 400 (see
 * internal/handlers/audit.go's List) — callers should validate with
 * `parseTargetFilter` first rather than relying on the round trip to catch it.
 */
export function listAudit(
  page = 1,
  perPage = 50,
  userId?: number,
  targetType?: string,
  targetId?: number,
): Promise<AuditPage> {
  let path = `/admin/audit?page=${page}&per_page=${perPage}`;
  if (userId !== undefined) {
    path += `&user_id=${userId}`;
  }
  if (targetType !== undefined) {
    path += `&target_type=${encodeURIComponent(targetType)}`;
  }
  if (targetId !== undefined) {
    path += `&target_id=${targetId}`;
  }
  return apiGet<AuditPage>(path);
}

// ── Admin: interests taxonomy (#0024) ────────────────────────────────────────

/** GET /admin/interests — every interest, active and inactive, with a per-interest subscriber_count (admin only). */
export function listInterests(): Promise<{ interests: Interest[] }> {
  return apiGet<{ interests: Interest[] }>('/admin/interests');
}

/**
 * POST /admin/interests — create a new interest (admin only). `slug` must be
 * lowercase and hyphenated and is immutable once created; `sortOrder`
 * defaults to 0 when omitted.
 */
export function createInterest(
  slug: string,
  name: string,
  description: string,
  sortOrder?: number,
): Promise<Interest> {
  return apiPost<Interest>('/admin/interests', {
    slug,
    name,
    description: description || undefined,
    sort_order: sortOrder,
  });
}

/**
 * PATCH /admin/interests/{id} — update an existing interest's name,
 * description, sort_order, and/or active flag (admin only). Only the fields
 * present in `fields` are changed; there is deliberately no `slug` field —
 * the server rejects a body that carries one. Setting `active: false` is how
 * the admin screen deactivates an interest (hides it from the signup form
 * while preserving subscriber_interests history); `active: true` reactivates
 * it.
 */
export function updateInterest(
  id: number,
  fields: { name?: string; description?: string; sort_order?: number; active?: boolean },
): Promise<Interest> {
  return apiPatch<Interest>(`/admin/interests/${id}`, fields);
}

/**
 * DELETE /admin/interests/{id} — permanently remove an interest (admin
 * only). The server refuses with a 409 ApiError when any subscriber is
 * associated; the caller should catch that and suggest deactivating instead.
 */
export function deleteInterest(id: number): Promise<{ message: string }> {
  return apiDelete<{ message: string }>(`/admin/interests/${id}`);
}

// ── Admin: subscribers screen (#0032) ────────────────────────────────────────

/** Filters/paging accepted by GET /admin/subscribers. */
export interface ListSubscribersParams {
  status?: string;
  interestId?: number;
  q?: string;
  page?: number;
  perPage?: number;
}

/**
 * GET /admin/subscribers — paginated, filterable, searchable (admin only).
 * `counts` in the response reflects the WHOLE table, not just the current
 * filter/page, so the header block stays stable while an admin filters.
 */
export function listSubscribers(params: ListSubscribersParams = {}): Promise<SubscribersPage> {
  const sp = new URLSearchParams();
  if (params.status) sp.set('status', params.status);
  if (params.interestId) sp.set('interest_id', String(params.interestId));
  if (params.q) sp.set('q', params.q);
  sp.set('page', String(params.page ?? 1));
  sp.set('per_page', String(params.perPage ?? 25));
  return apiGet<SubscribersPage>(`/admin/subscribers?${sp.toString()}`);
}

/**
 * GET /admin/subscribers/{id} — detail view: consent evidence (signup IP,
 * user agent, both timestamps), selected interests, and email event history
 * (admin only). `email_events` is always `[]` until #0038 lands.
 */
export function getSubscriber(id: number): Promise<Subscriber> {
  return apiGet<Subscriber>(`/admin/subscribers/${id}`);
}

/** Response shape shared by suppress and clear-complaint. */
export interface SubscriberActionResult {
  subscriber: Subscriber;
  message: string;
}

/**
 * POST /admin/subscribers/{id}/suppress — manual suppress with a required
 * note (admin only). Calls the store's Unsubscribe (source: admin) AND, as
 * of #0033, writes a real suppressions-table row (reason: manual) — a future
 * resignup by this address is blocked at #0026's send gate, not just marked
 * unsubscribed. `no_op` is true when the target had already complained,
 * since Unsubscribe is a guaranteed no-op on a complained row (CLAUDE.md
 * §9) — the caller must show that rather than imply the subscriber's status
 * changed; it is independent of `suppression_added` (the suppressions-table
 * write still happens even when `no_op` is true).
 */
export function suppressSubscriber(
  id: number,
  note: string,
): Promise<SubscriberActionResult & { no_op: boolean; suppression_added: boolean }> {
  return apiPost<SubscriberActionResult & { no_op: boolean; suppression_added: boolean }>(
    `/admin/subscribers/${id}/suppress`,
    { note },
  );
}

/**
 * POST /admin/subscribers/{id}/clear-complaint — the sole sanctioned exit
 * from `complained` (admin only). The server answers 409 (ApiError) if the
 * target isn't currently complained; the caller should only offer this
 * action on complained rows in the first place. Resulting status is
 * `unsubscribed`, not `active` — clearing a complaint does not by itself
 * re-establish double opt-in consent. Removes the `complaint`
 * suppressions-table row ONLY (#0100) — any other reason (for example a
 * hard bounce) survives and keeps the address blocked at #0026's send gate;
 * `message` names any surviving reason.
 */
export function clearSubscriberComplaint(id: number): Promise<SubscriberActionResult> {
  return apiPost<SubscriberActionResult>(`/admin/subscribers/${id}/clear-complaint`);
}

/**
 * POST /admin/subscribers — manual add (admin only). Still requires double
 * opt-in: a brand-new address lands `pending` with a confirmation email
 * queued, never `active` directly. `interests` are slugs, matching the
 * public signup form; `note` is optional operator context recorded in the
 * audit log.
 */
export function createSubscriber(
  email: string,
  interests: string[],
  note: string,
): Promise<SubscriberActionResult> {
  return apiPost<SubscriberActionResult>('/admin/subscribers', {
    email,
    interests,
    note: note || undefined,
  });
}

// ── Admin suppression-list screen (#0100) ────────────────────────────────────

/**
 * GET /admin/suppressions — every suppression row, joined to the
 * subscriber's status when one exists (`subscriber_status: null` for an
 * orphaned row — hard deletion, #0060, or a suppression added before any
 * signup). No pagination or filtering; see the server's own doc comment for
 * why.
 */
export function listSuppressions(): Promise<{ suppressions: Suppression[]; count: number }> {
  return apiGet<{ suppressions: Suppression[]; count: number }>('/admin/suppressions');
}

/** Response shape for POST /admin/suppressions/remove. */
export interface RemoveSuppressionResult {
  removed: Suppression;
  remaining: string[];
  message: string;
}

/**
 * POST /admin/suppressions/remove — reverse one (email, reason) suppression
 * with a required justification note (admin only, #0100). Removal NEVER
 * changes a subscriber's status — an address that was `unsubscribed` stays
 * `unsubscribed`; it returns only through double opt-in. The server answers
 * 409 (ApiError) when `reason` is `"complaint"` and a subscribers row still
 * exists for the address — use `clearSubscriberComplaint` instead in that
 * case (`suppressionRemovalBlocked` in lib/admin.ts pre-disables the button
 * so the UI never has to discover this after a failed request), and 404 when
 * no matching (email, reason) row exists.
 */
export function removeSuppression(
  email: string,
  reason: string,
  note: string,
): Promise<RemoveSuppressionResult> {
  return apiPost<RemoveSuppressionResult>('/admin/suppressions/remove', { email, reason, note });
}

// ── Admin: campaign compose (#0041/#0044/#0045/#0046/#0047) ─────────────────

/** The fields POST /admin/campaigns and PATCH /admin/campaigns/{id} accept. */
export interface CampaignDraftFields {
  name?: string;
  subject?: string;
  preheader?: string;
  body_md?: string;
  audience_mode?: string;
  interest_ids?: number[];
}

/** GET /admin/campaigns — every campaign, newest first (admin only). */
export function listCampaigns(): Promise<{ campaigns: Campaign[] }> {
  return apiGet<{ campaigns: Campaign[] }>('/admin/campaigns');
}

/** GET /admin/campaigns/{id} — one campaign (admin only). */
export function getCampaign(id: number): Promise<Campaign> {
  return apiGet<Campaign>(`/admin/campaigns/${id}`);
}

/**
 * POST /admin/campaigns — create a new draft campaign (admin only). `name`,
 * `subject`, and `body_md` are required by the server; `audience_mode`
 * defaults to `any_of` when omitted.
 */
export function createCampaign(fields: CampaignDraftFields): Promise<Campaign> {
  return apiPost<Campaign>('/admin/campaigns', fields);
}

/**
 * PATCH /admin/campaigns/{id} — content-only edit (admin only). Every field
 * is optional; only the ones present change. There is deliberately no
 * `status` field — the server never changes status via this route (see
 * `sendCampaign`/`cancelCampaign`). The server answers 409 unless the
 * campaign is currently `draft` or `scheduled` (`canEditCampaign`).
 */
export function updateCampaign(id: number, fields: CampaignDraftFields): Promise<Campaign> {
  return apiPatch<Campaign>(`/admin/campaigns/${id}`, fields);
}

/**
 * GET /admin/campaigns/{id}/audience — the live recipient count for the
 * campaign's CURRENTLY SAVED audience_mode/interest_ids (admin only). Never
 * materializes anything (#0044's own contract) — safe to call on every
 * checkbox change. Throws ApiError(409) for an empty interest set under
 * any_of/all_of or an unknown mode; lib/audience.ts's `audienceErrorMessage`
 * reads that verbatim.
 */
export function getCampaignAudience(id: number): Promise<AudiencePreviewResponse> {
  return apiGet<AudiencePreviewResponse>(`/admin/campaigns/${id}/audience`);
}

/**
 * GET /admin/campaigns/{id}/preflight — a read-only, non-mutating evaluation
 * of #0045's send gate against the campaign's STORED row (admin only, #0047).
 * Never sends anything, never transitions the campaign, writes no audit row.
 */
export function getCampaignPreflight(id: number): Promise<PreflightResponse> {
  return apiGet<PreflightResponse>(`/admin/campaigns/${id}/preflight`);
}

/**
 * POST /admin/campaigns/{id}/preview — renders the campaign's STORED row
 * without sending anything (admin only, #0046). Accepts no body: the server
 * structurally ignores any override, so the preview always matches what a
 * send would render — see #0047's Notes on why the editor autosaves before
 * calling this.
 */
export function previewCampaign(id: number): Promise<CampaignPreviewResponse> {
  return apiPost<CampaignPreviewResponse>(`/admin/campaigns/${id}/preview`);
}

/**
 * POST /admin/campaigns/{id}/test — sends one real test message to the
 * REQUESTING ADMIN'S OWN address (admin only, #0046). Accepts no body and no
 * destination — the endpoint never takes a client-supplied recipient (see
 * `#0046`'s carried-in note, `admin_campaign_preview.go`'s package doc
 * comment). Returns the updated Campaign (`test_sent_at` now set).
 */
export function testSendCampaign(id: number): Promise<Campaign> {
  return apiPost<Campaign>(`/admin/campaigns/${id}/test`);
}

/**
 * POST /admin/campaigns/{id}/send — the operator-triggered `draft|failed` →
 * `scheduled` transition (admin only, #0041/#0047). `confirmCount` is the
 * recipient count the operator typed to confirm — the server independently
 * re-verifies it against the authoritative audience count and 409s on a
 * mismatch, so a client-side check alone is never trusted. Throws
 * ApiError(409) with body `{"unmet":[...]}` when the advisory preflight gate
 * fails (see lib/preflight.ts's `parseUnmet`, which reads this exact shape
 * off `ApiError.body`), or a plain `{"error":"..."}` ApiError for any other
 * 409 (illegal transition, confirm_count mismatch, bad audience state).
 */
export function sendCampaign(id: number, confirmCount: number): Promise<Campaign> {
  return apiPost<Campaign>(`/admin/campaigns/${id}/send`, { confirm_count: confirmCount });
}

/**
 * POST /admin/campaigns/{id}/cancel — stops a `scheduled` or `sending`
 * campaign (admin only, #0041). Never recalls an already-sent message; see
 * lib/campaigns.ts's `cancelCopy` for the operator-facing distinction.
 */
export function cancelCampaign(id: number): Promise<Campaign> {
  return apiPost<Campaign>(`/admin/campaigns/${id}/cancel`);
}

/**
 * GET /admin/campaigns/{id}/stats — the per-campaign outcome screen (admin
 * only, #0049): counts by send status, bounce/complaint counts reconciled
 * from email_events, and failed sends with their error messages. Read-only;
 * writes no audit row (matches getCampaignPreflight/getCampaignAudience).
 */
export function getCampaignStats(id: number): Promise<CampaignStatsResponse> {
  return apiGet<CampaignStatsResponse>(`/admin/campaigns/${id}/stats`);
}

// ── Public mailing-list journey: signup, confirm, preferences (#0029-#0031) ──

/**
 * GET /api/interests — the public, unauthenticated active interest taxonomy
 * (#0029). Source for SubscribeForm's and PreferenceCenter's checkbox grids;
 * NEVER the admin /admin/interests route, which requires a session and
 * includes deactivated interests a public form must not offer (#0024's
 * carried-in review finding).
 */
export function listPublicInterests(): Promise<{ interests: PublicInterest[] }> {
  return apiGet<{ interests: PublicInterest[] }>('/api/interests');
}

/**
 * POST /api/subscribe — double opt-in signup (#0026, #0029). The success body
 * is deliberately NOT typed/read by callers beyond a bare 2xx check — see
 * lib/subscribe.ts's onSubscribeSuccess and its doc comment for why: #0026's
 * uniform 202 means every successful branch returns an identical body, and
 * reading it here would be the one place this component could reintroduce a
 * distinction the server just eliminated.
 */
export function subscribe(body: SubscribeRequestBody): Promise<unknown> {
  return apiPost<unknown>('/api/subscribe', body);
}

/**
 * POST /api/subscribe/confirm — completes double opt-in (#0030). Throws
 * ApiError(400) for an unknown, expired, replayed, or complained-locked
 * token; the SPA (ConfirmSubscription.svelte) renders one friendly,
 * non-technical state for all of those rather than asserting which one
 * occurred, since the server itself cannot always tell them apart (see
 * confirm.go's package doc comment).
 */
export function confirmSubscription(token: string): Promise<ConfirmResponse> {
  return apiPost<ConfirmResponse>('/api/subscribe/confirm', { token });
}

/**
 * GET /api/preferences?token=... — the preference center's read (#0031).
 * Email always comes back masked; throws ApiError(404) for an invalid,
 * unknown, or rotated token, which PreferenceCenter.svelte renders as a
 * neutral "this link doesn't work, subscribe again" state rather than the
 * generic NotFound view.
 */
export function getPreferences(token: string): Promise<PreferencesResponse> {
  return apiGet<PreferencesResponse>(`/api/preferences?token=${encodeURIComponent(token)}`);
}

/**
 * PATCH /api/preferences — replaces the interest set, OR (when the body
 * carries `unsubscribe: true`) performs the separate, explicitly-labelled
 * "Unsubscribe from everything" action (#0031). See lib/subscribe.ts's
 * buildSaveInterestsPatch / buildUnsubscribeEverythingPatch for the two
 * request shapes this accepts.
 */
export function patchPreferences(body: PreferencesPatchBody): Promise<PreferencesResponse> {
  return apiPatch<PreferencesResponse>('/api/preferences', body);
}

// ── Public workshops (#0051, #0053) ─────────────────────────────────────────

/**
 * GET /api/workshops — the public, unauthenticated workshop index (#0051,
 * #0053): published workshops split into upcoming and past, relative to the
 * server's clock. Source for WorkshopsIndex.svelte's two sections and
 * Home.svelte's "next up" cards (lib/workshops.ts's `nextWorkshops`).
 * Draft and canceled workshops never appear here — see the handler's own
 * doc comment (internal/handlers/public_workshops.go) for why that is
 * narrower than the detail route.
 */
export function listPublicWorkshops(): Promise<WorkshopsListResponse> {
  return apiGet<WorkshopsListResponse>('/api/workshops');
}

/**
 * POST /api/unsubscribe?token=... — the browser-driven confirm click on
 * /unsubscribe (#0034, #0036, PRD §6.5 path 1). This is the SAME route the
 * List-Unsubscribe header points a mail provider's automated POST at; a
 * person clicking the confirm button on the SPA's /unsubscribe view is the
 * other caller. Always resolves at 200 -- the server never distinguishes a
 * valid, unknown, expired, replayed, or already-complained token by status
 * code, only by the `message`/`no_op` fields in the body (see
 * internal/handlers/unsubscribe.go's package doc comment) -- so there is no
 * ApiError branch for a caller to catch here beyond an actual network
 * failure. `token` is passed through even when null/absent: the server's
 * missing-token branch answers the same neutral 200, so this view has
 * nothing to validate client-side (see lib/unsubscribe.ts's extractToken).
 */
export function unsubscribeOneClick(token: string | null): Promise<UnsubscribeResult> {
  const path = token ? `/api/unsubscribe?token=${encodeURIComponent(token)}` : '/api/unsubscribe';
  return apiPost<UnsubscribeResult>(path);
}
