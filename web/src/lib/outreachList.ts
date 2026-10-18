// Pure helpers for the Outreach landing list, the Workshops Outreach column
// and the Overview's outreach needs-attention items (#0586). No DOM, so they
// are unit-testable directly.

import type { AdminWorkshop, ConnectionGroup, OutreachSummary } from './types';
import { GROUP_LABELS } from './outreachConnections';

export interface OutreachChip {
  label: string;
  /** badge colour class suffix, matching the existing .badge-* vocabulary */
  tone: 'muted' | 'success';
}

/** Published workshops that are upcoming, soonest first. "Upcoming" is the
 * public listing's rule (workshops.ListVisible): starts_at at or after now,
 * or no starts_at at all (date TBD), which sorts after every dated one. The
 * server offers outreach for any published workshop, so an undated one must
 * be reachable from this list too. */
export function upcomingPublished(workshops: AdminWorkshop[], now: Date = new Date()): AdminWorkshop[] {
  const at = (w: AdminWorkshop) => (w.starts_at ? Date.parse(w.starts_at) : Number.POSITIVE_INFINITY);
  return workshops
    .filter((w) => w.status === 'published' && at(w) >= now.getTime())
    .sort((a, b) => (at(a) === at(b) ? a.id - b.id : at(a) < at(b) ? -1 : 1));
}

/** Index summaries by workshop id. */
export function summariesByWorkshop(rows: OutreachSummary[]): Map<number, OutreachSummary> {
  return new Map(rows.map((r) => [r.workshop_id, r]));
}

function count(s: OutreachSummary, status: string): number {
  return s.counts[status] ?? 0;
}

/** The status chip for one workshop's outreach: "Not started", "Set up", or posted/scheduled counts. */
export function outreachChip(summary: OutreachSummary | undefined): OutreachChip {
  if (!summary) return { label: 'Not started', tone: 'muted' };
  const posted = count(summary, 'posted');
  const scheduled = count(summary, 'scheduled');
  const draft = count(summary, 'draft');
  const parts: string[] = [];
  if (posted > 0) parts.push(`${posted} posted`);
  if (scheduled > 0) parts.push(`${scheduled} scheduled`);
  if (parts.length > 0) return { label: parts.join(' · '), tone: 'success' };
  if (draft > 0) return { label: `${draft} drafted`, tone: 'muted' };
  return { label: 'Set up · no posts', tone: 'muted' };
}

/** The one button on an Outreach list row. */
export function outreachRowButtonLabel(summary: OutreachSummary | undefined): string {
  return summary ? 'Open' : 'Promote';
}

/** The workshop editor panel's link text: "Promote this workshop" or "Open outreach · N posted". */
export function editorOutreachLinkLabel(started: boolean, posted: number): string {
  return started ? `Open outreach · ${posted} posted` : 'Promote this workshop';
}

/** Upcoming published workshops with nothing posted or scheduled yet. */
export function unannouncedUpcoming(
  workshops: AdminWorkshop[],
  summaries: OutreachSummary[],
  now: Date = new Date(),
): AdminWorkshop[] {
  const by = summariesByWorkshop(summaries);
  return upcomingPublished(workshops, now).filter((w) => {
    const s = by.get(w.id);
    return !s || count(s, 'posted') + count(s, 'scheduled') === 0;
  });
}

export interface OutreachAttentionItem {
  key: string;
  message: string;
  /** hash the item links to, when there is somewhere to act */
  href: string;
}

/**
 * Needs-attention rows for outreach: disconnected services and unannounced
 * upcoming workshops. `enabled` is the channels switched on in Settings
 * (#0588); a network switched off there is not reported as disconnected.
 * ShortLinks is not a channel and is always reported. Absent, every group is.
 */
export function outreachAttentionItems(
  groups: ConnectionGroup[],
  workshops: AdminWorkshop[],
  summaries: OutreachSummary[],
  now: Date = new Date(),
  enabled?: string[],
): OutreachAttentionItem[] {
  const items: OutreachAttentionItem[] = [];
  const wanted = (g: ConnectionGroup) => g.name === 'shortlinks' || !enabled || enabled.includes(g.name);
  const missing = groups.filter((g) => !g.configured && wanted(g)).map((g) => GROUP_LABELS[g.name] ?? g.name);
  if (missing.length > 0) {
    items.push({
      key: 'outreach-connections',
      message: `Not connected: ${missing.join(', ')}. Connect in Settings → Outreach.`,
      href: '#/settings?section=outreach',
    });
  }
  const n = unannouncedUpcoming(workshops, summaries, now).length;
  if (n > 0) {
    items.push({
      key: 'outreach-unannounced',
      message: `${n} upcoming workshop${n === 1 ? '' : 's'} not yet announced.`,
      href: '#/outreach',
    });
  }
  return items;
}
