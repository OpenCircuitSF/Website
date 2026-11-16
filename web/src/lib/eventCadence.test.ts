// #0678: the pure helpers behind Settings → Event cadence and the "Next month" panel.
import { describe, expect, it } from 'vitest';
import {
  coverageHeadline,
  describeMatch,
  describeRule,
  emptyRuleForm,
  firstRuleFormError,
  formFromRule,
  formatCoverageDate,
  lumaLine,
  nextMonthLabel,
  ruleFormErrors,
  toRulePayload,
  type CadenceRuleForm,
} from './eventCadence';
import type { CadenceRule, Coverage, CoverageLuma } from './types';

function rule(over: Partial<CadenceRule> = {}): CadenceRule {
  return {
    id: 1,
    name: 'Hack night',
    description: 'every Tuesday',
    event_type: { id: 2, slug: 'hack-night', name: 'Hack Night' },
    title_prefix: null,
    frequency: 'weekly',
    weekday: 'tuesday',
    per_month: null,
    venue: null,
    active: true,
    created_at: '2026-11-15T00:00:00-08:00',
    updated_at: '2026-11-15T00:00:00-08:00',
    ...over,
  };
}

function form(over: Partial<CadenceRuleForm> = {}): CadenceRuleForm {
  return { ...emptyRuleForm(), name: 'Hack night', eventTypeId: 2, weekday: 'tuesday', ...over };
}

const luma = (over: Partial<CoverageLuma> = {}): CoverageLuma => ({
  status: 'ok',
  read_at: null,
  last_good_at: null,
  not_on_luma: [],
  differs: [],
  only_on_luma: [],
  note: null,
  ...over,
});

function coverage(over: Partial<Coverage> = {}): Coverage {
  return {
    month: '2026-12',
    label: 'December 2026',
    zone: 'America/Los_Angeles',
    from: '2026-12-01T00:00:00-08:00',
    to: '2027-01-01T00:00:00-08:00',
    rules: [],
    unmatched_events: [],
    luma: luma(),
    summary: { rules: 0, missing: 0, drafts: 0, unmatched: 0 },
    ...over,
  };
}

const ev = { id: 9, slug: 's', title: 'T', status: 'draft', starts_at: '2026-12-15T18:00:00-08:00', admin_url: '', public_url: '' };

describe('describeRule and describeMatch', () => {
  it('words the frequency like the server', () => {
    expect(describeRule(rule())).toBe('every Tuesday');
    expect(describeRule(rule({ frequency: 'monthly', weekday: null, per_month: 2 }))).toBe('2 a month');
  });
  it('words the match', () => {
    expect(describeMatch(rule())).toBe('Hack Night events');
    expect(describeMatch(rule({ event_type: null, title_prefix: 'Programming LEDs' }))).toBe(
      'titles starting “Programming LEDs”',
    );
    expect(describeMatch(rule({ title_prefix: 'Intro' }))).toBe('Hack Night events with titles starting “Intro”');
  });
});

describe('ruleFormErrors', () => {
  it('accepts a valid weekly and monthly form', () => {
    expect(ruleFormErrors(form())).toEqual({});
    expect(ruleFormErrors(form({ eventTypeId: null, titlePrefix: 'LEDs', frequency: 'monthly', perMonth: '1' }))).toEqual({});
  });
  it('mirrors the server messages by field', () => {
    expect(ruleFormErrors(form({ name: '  ' })).name).toBe('is required');
    expect(ruleFormErrors(form({ eventTypeId: null, titlePrefix: ' ' })).event_type_id).toBe(
      'set an event type, a title prefix, or both',
    );
    expect(ruleFormErrors(form({ weekday: '' })).weekday).toBe('a weekly rule needs a weekday');
    expect(ruleFormErrors(form({ frequency: 'monthly', perMonth: '0' })).per_month).toBe('must be 1 to 31');
    expect(ruleFormErrors(form({ frequency: 'monthly', perMonth: '32' })).per_month).toBe('must be 1 to 31');
    expect(ruleFormErrors(form({ frequency: 'monthly', perMonth: '1.5' })).per_month).toBe('must be 1 to 31');
    expect(ruleFormErrors(form({ name: 'x'.repeat(121) })).name).toBe('must be 120 characters or fewer');
  });
  it('reports the first problem as a sentence', () => {
    expect(firstRuleFormError(ruleFormErrors(form({ name: '', weekday: '' })))).toBe('Name: is required.');
    expect(firstRuleFormError({})).toBeNull();
  });
});

describe('toRulePayload and formFromRule', () => {
  it('sends only the fields the frequency uses', () => {
    expect(toRulePayload(form({ name: ' Hack night ', perMonth: '3', venueId: 4 }))).toEqual({
      name: 'Hack night',
      frequency: 'weekly',
      weekday: 'tuesday',
      event_type_id: 2,
      venue_id: 4,
      active: true,
    });
    expect(
      toRulePayload(form({ eventTypeId: null, titlePrefix: ' LEDs ', frequency: 'monthly', perMonth: '2', active: false })),
    ).toEqual({ name: 'Hack night', frequency: 'monthly', title_prefix: 'LEDs', per_month: 2, active: false });
  });
  it('round-trips a rule into the form', () => {
    const r = rule({ venue: { id: 4, name: 'Shop' }, title_prefix: 'Hack' });
    expect(toRulePayload(formFromRule(r))).toEqual({
      name: 'Hack night',
      frequency: 'weekly',
      weekday: 'tuesday',
      event_type_id: 2,
      title_prefix: 'Hack',
      venue_id: 4,
      active: true,
    });
  });
});

describe('coverage headline', () => {
  it('names the month', () => {
    expect(nextMonthLabel(coverage())).toBe('December 2026');
  });
  it('covers no rules, all set, missing and drafts', () => {
    expect(coverageHeadline(coverage())).toBe('No cadence rules yet');
    const rc = {
      rule: rule(),
      expected: { dates: [], count: 0 },
      events: [],
      canceled: [],
      drafts: [],
      missing_dates: [],
      draft_only_dates: [],
      missing: 0,
      state: 'ok' as const,
    };
    expect(coverageHeadline(coverage({ rules: [rc], summary: { rules: 1, missing: 0, drafts: 0, unmatched: 0 } }))).toBe(
      'All set for December',
    );
    expect(coverageHeadline(coverage({ rules: [rc], summary: { rules: 1, missing: 2, drafts: 1, unmatched: 0 } }))).toBe(
      '2 missing, 1 still a draft',
    );
    expect(coverageHeadline(coverage({ rules: [rc], summary: { rules: 1, missing: 0, drafts: 3, unmatched: 0 } }))).toBe(
      '3 still drafts',
    );
  });
});

describe('dates and the Luma line', () => {
  it('formats a report date without shifting it', () => {
    expect(formatCoverageDate('2026-12-15')).toBe('Tue, Dec 15');
    expect(formatCoverageDate('2026-11-01')).toBe('Sun, Nov 1');
  });
  it('summarises the Luma part', () => {
    expect(lumaLine(luma())).toBe('Luma: everything matches');
    expect(lumaLine(luma({ not_on_luma: [ev], differs: [{ event: ev, luma_url: null, luma_title: null, diffs: [] }] }))).toBe(
      'Luma: 1 not on Luma, 1 differs',
    );
    expect(lumaLine(luma({ status: 'unreachable' }))).toBe('Luma unavailable (unreachable)');
    expect(lumaLine(luma({ status: 'off' }))).toBe('Luma check is off');
  });
});
