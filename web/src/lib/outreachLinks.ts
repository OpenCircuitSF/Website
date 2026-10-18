// Pure wording and decisions for the workshop outreach page (#0568): the links
// table, Create links, Draft all and Schedule all. No DOM, so it is all
// unit-testable; the Svelte components stay thin.
import type {
  OutreachDraftAllResult,
  OutreachLinkReport,
  OutreachLinkRow,
  OutreachLinksView,
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
  return `${n} ${n === 1 ? 'link' : 'links'} could not be created: ${list}. Everything else is saved — use Create missing links to try again.`;
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

export type LinksEmptyKind = 'not_connected' | 'not_published' | 'no_links' | 'some_missing' | null;

/** What the links table says above itself, and which next step it points at. */
export function linksEmptyState(v: OutreachLinksView): { kind: LinksEmptyKind; message: string } {
  if (v.unavailable_reason === 'shortlinks_not_configured') {
    return { kind: 'not_connected', message: 'ShortLinks is not connected. Connect it in Settings to create links.' };
  }
  if (v.unavailable_reason === 'workshop_not_published') {
    return { kind: 'not_published', message: 'This workshop is not published. Publish it in the workshop editor to create links.' };
  }
  if (v.outreach && v.links.length > 0 && v.links.every((l) => l.short_url === null)) {
    return { kind: 'no_links', message: 'No links yet. Create links to make a short link for every channel, every post and every flyer location.' };
  }
  if (v.missing > 0) {
    return {
      kind: 'some_missing',
      message: `${v.missing} ${v.missing === 1 ? 'link is' : 'links are'} missing. Create missing links to finish the set.`,
    };
  }
  return { kind: null, message: '' };
}

/** "Bluesky" or "Flyer · Mission Library". */
export function linkRowLabel(row: OutreachLinkRow): string {
  return row.kind === 'flyer' ? `Flyer · ${row.location_name ?? row.location_code ?? ''}` : row.channel_label;
}

/** A row's click count, or an em dash when none is known. */
export function linkClicksLabel(row: OutreachLinkRow, status: string): string {
  if (status !== 'ok' || row.clicks === null) return '—';
  return String(row.clicks);
}

/** The one-line result of Draft all. */
export function draftAllMessage(r: OutreachDraftAllResult): string {
  const n = r.created.length;
  if (n === 0) return `Nothing to draft: all ${r.skipped} cells already have a post.`;
  let msg = `Drafted ${n} ${n === 1 ? 'post' : 'posts'}`;
  if (r.skipped > 0) msg += `; ${r.skipped} existing ${r.skipped === 1 ? 'post was' : 'posts were'} left alone`;
  msg += '.';
  if (r.without_link > 0) {
    msg += ` ${r.without_link} ${r.without_link === 1 ? 'draft has' : 'drafts have'} no link yet — create the missing links first.`;
  }
  return msg;
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
    return { ok: false, reason: 'Publish this workshop in the editor to schedule posts.' };
  }
  if (!view.channels.some((c) => c.automatic && c.configured)) {
    return { ok: false, reason: 'Bluesky and Mastodon are not connected. Connect one in Settings to schedule posts.' };
  }
  return { ok: true, reason: null };
}
