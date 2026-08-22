// Pure helpers for the campaign compose screen's audience panel (#0047 §6):
// shaping the order-independent key GET .../campaigns/{id}/audience
// requests are dropped/kept by, and presenting the response (count, sample
// caption, warnings, error message).

import { ApiError } from './api';
import type { AudiencePreviewResponse } from './types';

/**
 * An order-independent key for one (mode, interestIds) audience selection —
 * mode plus the interest ids sorted and joined. Used to drop a stale
 * response: an operator ticking three boxes quickly must never see the
 * count for the first tick land after the third.
 */
export function audienceRequestKey(mode: string, interestIds: number[]): string {
  const sorted = [...interestIds].sort((a, b) => a - b);
  return `${mode}:${sorted.join(',')}`;
}

/**
 * Whether a response keyed by `responseKey` is stale against the CURRENT
 * selection's key — i.e. whether it should be dropped rather than rendered.
 */
export function isStaleAudience(responseKey: string, currentKey: string): boolean {
  return responseKey !== currentKey;
}

/** The headline recipient-count sentence for an audience preview response. */
export function describeAudience(preview: AudiencePreviewResponse): string {
  if (preview.count === 0) {
    return 'No recipients match this audience.';
  }
  if (preview.count === 1) {
    return '1 recipient';
  }
  return `${preview.count} recipients`;
}

/**
 * Whether the response's sample is a truncated view of the full audience
 * (strictly fewer sample rows than the total count) — not when they're
 * equal, i.e. the sample happens to show everyone.
 */
export function sampleIsTruncated(preview: AudiencePreviewResponse): boolean {
  return preview.sample.length < preview.count;
}

/**
 * Caption for the collapsed sample `<details>` element, e.g. "Showing N of
 * TOTAL". The server-side sample cap appears NOWHERE in this module — it is
 * derived entirely from `sample.length`, so if the server ever changes
 * campaignAudienceSampleLimit this caption stays true without a client-side
 * edit.
 */
export function sampleCaption(preview: AudiencePreviewResponse): string {
  return `Showing ${preview.sample.length} of ${preview.count}`;
}

/**
 * The operator-facing message for a failed audience request: an `ApiError`'s
 * server message verbatim (the empty-interest-set 409 or unknown-mode 409,
 * both from #0044), or one generic message for anything else (a network
 * failure). Rendered in the same place the recipient count would have been.
 */
export function audienceErrorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    return err.message;
  }
  return 'Could not load the audience count. Check your connection and try again.';
}
