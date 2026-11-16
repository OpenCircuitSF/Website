// Pure helpers behind Settings → Event cadence and the Events page's "Next
// month" panel (#0678). The server's internal/cadence is the authority: its
// Validate() messages are mirrored by ruleFormErrors so the form can say what
// is wrong before it submits, and the server's 400 is only the backstop.

import type { CadenceRule, CadenceRulePayload, Coverage, CoverageLuma } from './types';

export const WEEKDAYS = ['sunday', 'monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday'] as const;

const MONTHS = [
  'January',
  'February',
  'March',
  'April',
  'May',
  'June',
  'July',
  'August',
  'September',
  'October',
  'November',
  'December',
];

/** "tuesday" → "Tuesday". */
export function weekdayLabel(day: string): string {
  return day ? day[0].toUpperCase() + day.slice(1) : '';
}

/** The add/edit form's state. perMonth is the raw input text. */
export interface CadenceRuleForm {
  name: string;
  eventTypeId: number | null;
  titlePrefix: string;
  frequency: 'weekly' | 'monthly';
  weekday: string;
  perMonth: string;
  venueId: number | null;
  active: boolean;
}

export function emptyRuleForm(): CadenceRuleForm {
  return {
    name: '',
    eventTypeId: null,
    titlePrefix: '',
    frequency: 'weekly',
    weekday: '',
    perMonth: '1',
    venueId: null,
    active: true,
  };
}

/** The form for editing an existing rule. */
export function formFromRule(rule: CadenceRule): CadenceRuleForm {
  return {
    name: rule.name,
    eventTypeId: rule.event_type?.id ?? null,
    titlePrefix: rule.title_prefix ?? '',
    frequency: rule.frequency,
    weekday: rule.weekday ?? '',
    perMonth: rule.per_month != null ? String(rule.per_month) : '1',
    venueId: rule.venue?.id ?? null,
    active: rule.active,
  };
}

/** "every Tuesday" or "2 a month", as the server's Rule.Describe says it. */
export function describeRule(rule: Pick<CadenceRule, 'frequency' | 'weekday' | 'per_month'>): string {
  if (rule.frequency === 'weekly') return rule.weekday ? `every ${weekdayLabel(rule.weekday)}` : 'weekly';
  return `${rule.per_month ?? 0} a month`;
}

/** What the rule matches: "Hack Night events", "titles starting “Programming LEDs”", or both. */
export function describeMatch(rule: CadenceRule): string {
  const parts: string[] = [];
  if (rule.event_type) parts.push(`${rule.event_type.name} events`);
  if (rule.title_prefix) parts.push(`titles starting “${rule.title_prefix}”`);
  return parts.join(' with ');
}

/**
 * The form's problems by field, with internal/cadence's Validate() messages.
 * Empty when the form can be submitted.
 */
export function ruleFormErrors(form: CadenceRuleForm): Record<string, string> {
  const errors: Record<string, string> = {};
  const name = form.name.trim();
  if (name === '') errors.name = 'is required';
  else if ([...name].length > 120) errors.name = 'must be 120 characters or fewer';
  const prefix = form.titlePrefix.trim();
  if ([...prefix].length > 120) errors.title_prefix = 'must be 120 characters or fewer';
  if (form.eventTypeId === null && prefix === '') errors.event_type_id = 'set an event type, a title prefix, or both';
  if (form.frequency === 'weekly') {
    if (!form.weekday) errors.weekday = 'a weekly rule needs a weekday';
    else if (!(WEEKDAYS as readonly string[]).includes(form.weekday)) errors.weekday = 'must be sunday through saturday';
  } else {
    const n = Number(form.perMonth.trim());
    if (!/^\d+$/.test(form.perMonth.trim()) || n < 1 || n > 31) errors.per_month = 'must be 1 to 31';
  }
  return errors;
}

const FIELD_LABELS: Record<string, string> = {
  name: 'Name',
  event_type_id: 'Match',
  title_prefix: 'Title prefix',
  weekday: 'Weekday',
  per_month: 'Events a month',
};

/** The first error as a sentence for the form, in the server's field order. */
export function firstRuleFormError(errors: Record<string, string>): string | null {
  for (const field of ['name', 'event_type_id', 'title_prefix', 'weekday', 'per_month']) {
    if (errors[field]) return `${FIELD_LABELS[field]}: ${errors[field]}.`;
  }
  return null;
}

/** The request body for a valid form. */
export function toRulePayload(form: CadenceRuleForm): CadenceRulePayload {
  const payload: CadenceRulePayload = { name: form.name.trim(), frequency: form.frequency, active: form.active };
  if (form.eventTypeId !== null) payload.event_type_id = form.eventTypeId;
  const prefix = form.titlePrefix.trim();
  if (prefix !== '') payload.title_prefix = prefix;
  if (form.frequency === 'weekly') payload.weekday = form.weekday;
  else payload.per_month = Number(form.perMonth.trim());
  if (form.venueId !== null) payload.venue_id = form.venueId;
  return payload;
}

/** "December 2026", from the report's month. */
export function nextMonthLabel(coverage: Pick<Coverage, 'month'>): string {
  const [y, m] = coverage.month.split('-').map(Number);
  return MONTHS[m - 1] ? `${MONTHS[m - 1]} ${y}` : coverage.month;
}

/** The panel's one-line answer: "All set for December", "2 missing, 1 still a draft". */
export function coverageHeadline(coverage: Coverage): string {
  if (coverage.rules.length === 0) return 'No cadence rules yet';
  const { missing, drafts } = coverage.summary;
  if (missing === 0 && drafts === 0) {
    return `All set for ${nextMonthLabel(coverage).split(' ')[0]}`;
  }
  const parts: string[] = [];
  if (missing > 0) parts.push(`${missing} missing`);
  if (drafts > 0) parts.push(drafts === 1 ? '1 still a draft' : `${drafts} still drafts`);
  return parts.join(', ');
}

/** "Tue, Dec 15" for a report date ("2026-12-15", already Pacific). */
export function formatCoverageDate(date: string): string {
  const d = new Date(`${date}T12:00:00Z`);
  if (Number.isNaN(d.getTime())) return date;
  return d.toLocaleDateString('en-US', { weekday: 'short', month: 'short', day: 'numeric', timeZone: 'UTC' });
}

/** "Tue, Dec 15, 6:00 PM" for an event start, in Pacific time. */
export function formatCoverageTime(startsAt: string): string {
  const d = new Date(startsAt);
  if (Number.isNaN(d.getTime())) return startsAt;
  return d.toLocaleString('en-US', {
    weekday: 'short',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
    timeZone: 'America/Los_Angeles',
  });
}

/** The panel's Luma line. */
export function lumaLine(l: CoverageLuma): string {
  if (l.status === 'off') return 'Luma check is off';
  if (l.status !== 'ok') return `Luma unavailable (${l.status})`;
  const parts: string[] = [];
  if (l.not_on_luma.length > 0) parts.push(`${l.not_on_luma.length} not on Luma`);
  if (l.differs.length > 0) parts.push(`${l.differs.length} differs`);
  if (l.only_on_luma.length > 0) parts.push(`${l.only_on_luma.length} only on Luma`);
  return parts.length > 0 ? `Luma: ${parts.join(', ')}` : 'Luma: everything matches';
}

/** The admin hash for an event. */
export function eventHash(id: number): string {
  return `#/workshops/${id}`;
}
