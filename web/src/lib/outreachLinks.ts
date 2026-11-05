// Pure wording and decisions for the workshop outreach page (#0568): link-report
// and Schedule all wording. No DOM, so it is all unit-testable; the Svelte
// components stay thin.
import { onChannels } from './outreachCards';
import type {
  OutreachLinkReport,
  OutreachScheduleAllResult,
  WorkshopOutreach,
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

const SKIP_REASONS: Record<string, string> = {
  time_passed: 'time has passed',
  no_suggested_time: 'no suggested time — schedule Announce by hand',
  not_connected: 'not connected',
  not_drafted: 'not drafted',
  no_link: 'no link yet',
  invalid: 'the post cannot be sent as written',
  too_far: 'too far in the future',
  changed: 'changed since the preview',
};

/** A schedule-all skip reason in words. */
export function scheduleSkipReasonLabel(reason: string): string {
  return SKIP_REASONS[reason] ?? reason;
}

/** The one-line result of a confirmed Schedule all. */
export function scheduleAllMessage(r: OutreachScheduleAllResult): string {
  const n = r.to_schedule.length;
  const changed = r.skipped.filter((s) => s.reason === 'changed').length;
  let msg = n === 0 ? 'Nothing was scheduled.' : `Scheduled ${n} ${n === 1 ? 'post' : 'posts'}.`;
  if (changed > 0) msg += ` ${changed} ${changed === 1 ? 'post' : 'posts'} changed since the preview and ${changed === 1 ? 'was' : 'were'} not scheduled.`;
  return msg;
}

/** Whether Schedule all can do anything at all, and if not, why. */
export function canScheduleAll(view: WorkshopOutreach): { ok: boolean; reason: string | null } {
  if (view.workshop.status !== 'published') {
    return { ok: false, reason: 'Publish this event in the editor to schedule posts.' };
  }
  if (!onChannels(view).some((c) => c.automatic && c.configured)) {
    const anyOff = (view.off_channels ?? []).some((c) => c.automatic);
    return { ok: false, reason: anyOff ? 'Neither Bluesky nor Mastodon can post right now. Turn one on and connect it in Settings to schedule posts.' : 'Bluesky and Mastodon are not connected. Connect one in Settings to schedule posts.' };
  }
  return { ok: true, reason: null };
}
