// The Outreach home timeline (#0646): each published event in the next 30
// days as a lane, with one marker per channel and section at the time the
// post went out, is scheduled, or is suggested. Pure, no DOM; the component
// only positions what this returns.
//
// The 30-day cut is the server's (GET /admin/outreach-timeline,
// `timelineWindow`): it returns only published, dated events starting within
// 30 days, so this module never filters by date and has no undated case.
// The status words come from boardCell and sectionLine unchanged, so the
// timeline says what the event's own page says.
import { defaultScheduleAt } from './outreach';
import { boardChannelLabel, boardRows, onChannels, weekdayTime, type StatusKind } from './outreachCards';
import { handChannelLabel } from './outreachHandCopy';
import { sectionLine, withHandNames } from './outreachSection';
import { eventCounts, outreachChip, type OutreachChip } from './outreachList';
import { adminOutreachWorkshopHash } from './adminRouter';
import { formatPacificWeekday, fromPacificInput, toPacificInput } from './pacificTime';
import type { OutreachTimeline, OutreachTimelineEvent } from './types';

const DAY = 24 * 60 * 60 * 1000;
/** The shortest axis, so a single close event is not stretched across the page. */
export const MIN_SPAN_DAYS = 14;

export type MarkerKind = 'posted' | 'scheduled' | 'drafted' | 'missing' | 'failed';
export interface TimelineTick { at: string; label: string; pct: number }
export interface TimelineAxis { startMs: number; endMs: number; ticks: TimelineTick[] }
export interface TimelineMarker {
  key: string;
  wave: string;
  waveLabel: string;
  channel: string | null;
  kind: MarkerKind;
  status: string;
  detail: string | null;
  at: string;
  /** The time is the section's suggestion (or today), not one the post has. */
  suggested: boolean;
  /** Before today: drawn in the Earlier column at pct 0. */
  earlier: boolean;
  pct: number;
  level: number;
  href: string;
  label: string;
}
export interface TimelineRow { channel: string | null; label: string; markers: TimelineMarker[] }
export interface TimelineGap { wave: string; label: string; text: string; handLeft: string[]; href: string }
export interface TimelineLane {
  event: OutreachTimelineEvent;
  started: boolean;
  eventPct: number | null;
  chip: OutreachChip;
  href: string;
  rows: TimelineRow[];
  gaps: TimelineGap[];
}

/** From now to a day after the last event, never shorter than MIN_SPAN_DAYS. */
export function timelineAxis(events: OutreachTimelineEvent[], now: Date): TimelineAxis {
  const startMs = now.getTime();
  const starts = events.map((e) => (e.workshop.starts_at ? Date.parse(e.workshop.starts_at) : NaN)).filter((t) => !Number.isNaN(t));
  const endMs = Math.max(startMs + MIN_SPAN_DAYS * DAY, ...starts.map((t) => t + DAY));
  const axis: TimelineAxis = { startMs, endMs, ticks: [{ at: now.toISOString(), label: 'Today', pct: 0 }] };
  // Step whole Pacific calendar days and keep the Mondays, so DST cannot shift a tick.
  const [y, m, d] = toPacificInput(now.toISOString()).slice(0, 10).split('-').map(Number);
  for (let i = 1; ; i++) {
    const day = new Date(Date.UTC(y, m - 1, d + i));
    const iso = fromPacificInput(`${day.toISOString().slice(0, 10)}T00:00`);
    if (iso === null) continue;
    const t = Date.parse(iso);
    if (t >= endMs) break;
    if (day.getUTCDay() === 1) axis.ticks.push({ at: iso, label: formatPacificWeekday(iso), pct: pctOf(t, axis) });
  }
  return axis;
}

function pctOf(t: number, axis: Pick<TimelineAxis, 'startMs' | 'endMs'>): number {
  return Math.min(100, ((t - axis.startMs) / (axis.endMs - axis.startMs)) * 100);
}

/** Where an instant sits on the axis, 0–100; null when it is before the start. */
export function timelinePct(iso: string, axis: TimelineAxis): number | null {
  const t = Date.parse(iso);
  if (Number.isNaN(t) || t < axis.startMs) return null;
  return pctOf(t, axis);
}

/** For pcts in ascending order, the lowest level whose last marker is at least minGap away. */
export function stackLevels(pcts: number[], minGap = 8): number[] {
  const last: number[] = [];
  return pcts.map((p) => {
    let level = last.findIndex((q) => p - q >= minGap);
    if (level === -1) level = last.length;
    last[level] = p;
    return level;
  });
}

const KIND: Record<StatusKind | 'off', MarkerKind | null> = {
  posted: 'posted',
  scheduled: 'scheduled',
  on_hold: 'scheduled',
  queued: 'scheduled',
  failed: 'failed',
  not_scheduled: 'drafted',
  manual: 'drafted',
  not_drafted: 'missing',
  off: null,
};

function stack(markers: TimelineMarker[]): TimelineMarker[] {
  const sorted = [...markers].sort((a, b) => a.pct - b.pct || a.key.localeCompare(b.key));
  for (const group of [sorted.filter((m) => m.earlier), sorted.filter((m) => !m.earlier)]) {
    stackLevels(group.map((m) => m.pct)).forEach((level, i) => (group[i].level = level));
  }
  return sorted;
}

/** What each section still needs, in the event page's own words. */
export function sectionGaps(ev: OutreachTimelineEvent, now: Date): TimelineGap[] {
  if (ev.outreach === null) {
    return [{ wave: '', label: 'Outreach', text: 'Outreach not started', handLeft: [], href: adminOutreachWorkshopHash(ev.workshop.id) }];
  }
  const hand = onChannels(ev).filter((c) => !c.automatic);
  return ev.waves.map((w) => {
    const handLeft = hand
      .filter((c) => !ev.posts.some((p) => p.channel === c.name && p.wave === w.name && p.status === 'posted'))
      .map(handChannelLabel);
    return {
      wave: w.name,
      label: w.label,
      text: withHandNames(sectionLine(ev, w.name, now).text, handLeft),
      handLeft,
      href: adminOutreachWorkshopHash(ev.workshop.id, w.name),
    };
  });
}

function lane(ev: OutreachTimelineEvent, axis: TimelineAxis, now: Date): TimelineLane {
  const title = ev.workshop.title;
  const place = (at: string): { pct: number; earlier: boolean } => {
    const pct = timelinePct(at, axis);
    return pct === null ? { pct: 0, earlier: true } : { pct, earlier: false };
  };
  const suggestion = (wave: string): { at: string; suggested: boolean } => {
    const at = defaultScheduleAt(wave, ev.waves, now);
    return { at: at ?? now.toISOString(), suggested: at !== null };
  };
  const label = (channel: string | null, waveLabel: string, status: string, detail: string | null, at: string, suggested: boolean) =>
    `${title}: ${channel ? channel + ' ' : ''}${waveLabel}, ${status}${detail ? ' ' + detail : ''}${suggested ? `, suggested ${weekdayTime(at)}` : ''}`;

  let rows: TimelineRow[];
  if (ev.outreach === null) {
    rows = [{
      channel: null,
      label: 'Suggested',
      markers: stack(ev.waves.map((w) => {
        const s = suggestion(w.name);
        return {
          key: `none-${w.name}`, wave: w.name, waveLabel: w.label, channel: null, kind: 'missing', status: 'Not written', detail: null,
          at: s.at, suggested: s.suggested, ...place(s.at), level: 0,
          href: adminOutreachWorkshopHash(ev.workshop.id, w.name), label: label(null, w.label, 'Not written', null, s.at, s.suggested),
        };
      })),
    }];
  } else {
    const board = boardRows(ev);
    const off = (name: string) => (ev.off_channels ?? []).some((o) => o.name === name);
    rows = ev.channels.map((c) => {
      const rowLabel = boardChannelLabel(handChannelLabel(c), off(c.name));
      const markers: TimelineMarker[] = [];
      for (const br of board) {
        const cell = br.cells.find((x) => x.channel === c.name);
        const kind = cell ? KIND[cell.kind] : null;
        if (!cell || kind === null) continue;
        const post = ev.posts.find((p) => p.channel === c.name && p.wave === br.wave) ?? null;
        let at: string;
        let suggested = false;
        if (kind === 'posted') at = post?.posted_at ?? post?.updated_at ?? now.toISOString();
        else if (kind === 'scheduled' || kind === 'failed') at = post?.scheduled_at ?? now.toISOString();
        else ({ at, suggested } = suggestion(br.wave));
        markers.push({
          key: `${c.name}-${br.wave}`, wave: br.wave, waveLabel: br.label, channel: c.name, kind, status: cell.status, detail: cell.detail,
          at, suggested, ...place(at), level: 0,
          href: adminOutreachWorkshopHash(ev.workshop.id, br.wave), label: label(rowLabel, br.label, cell.status, cell.detail, at, suggested),
        });
      }
      return { channel: c.name, label: rowLabel, markers: stack(markers) };
    }).filter((r) => r.markers.length > 0);
  }
  return {
    event: ev,
    started: ev.outreach !== null,
    eventPct: ev.workshop.starts_at ? timelinePct(ev.workshop.starts_at, axis) : null,
    chip: outreachChip(ev.outreach ? { counts: eventCounts(ev.posts) } : undefined),
    href: adminOutreachWorkshopHash(ev.workshop.id),
    rows,
    gaps: sectionGaps(ev, now),
  };
}

/** The axis and one lane per event, in the server's (start) order. */
export function timelineLanes(t: OutreachTimeline, now: Date): { axis: TimelineAxis; lanes: TimelineLane[] } {
  const axis = timelineAxis(t.events, now);
  return { axis, lanes: t.events.map((ev) => lane(ev, axis, now)) };
}
