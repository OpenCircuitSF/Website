// Pure wording and decisions for the workshop outreach page (#0568): link-report
// wording. No DOM, so it is all unit-testable; the Svelte
// components stay thin.
import type {
  OutreachLinkReport,
} from './types';

const FAILURE_REASONS: Record<string, string> = {
  rate_limited: 'ShortLinks is rate limiting',
  timeout: 'timed out',
  not_configured: 'ShortLinks is not configured',
  rejected: 'ShortLinks rejected the token',
  code_changed: 'the location code changed',
  failed: 'failed',
};

/** A link failure's fixed reason code in words; unknown codes read as "failed". */
export function linkFailureReasonLabel(reason: string): string {
  return FAILURE_REASONS[reason] ?? 'failed';
}

/** The warning for a link-creation report, or null when nothing failed. */
export function linkReportMessage(r: OutreachLinkReport | undefined | null): string | null {
  if (!r || r.failed.length === 0) return null;
  const n = r.failed.length;
  const list = r.failed.map((f) => `${f.label} (${linkFailureReasonLabel(f.reason)})`).join(', ');
  return `${n} ${n === 1 ? 'link' : 'links'} could not be created: ${list}. Everything else is saved. A missing link is made when it is needed: press Create link on a post, or Print on a flyer.`;
}

/** The warning for a hand-links pass, or null when nothing failed (#0623). */
export function handLinkReportMessage(r: OutreachLinkReport | undefined | null): string | null {
  if (!r || r.failed.length === 0) return null;
  const n = r.failed.length;
  const list = r.failed.map((f) => `${f.label} (${linkFailureReasonLabel(f.reason)})`).join(', ');
  return `${n} ${n === 1 ? 'ShortLink' : 'ShortLinks'} could not be created: ${list}. Everything else here still works, and opening this page again tries again.`;
}
