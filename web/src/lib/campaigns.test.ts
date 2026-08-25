// Unit tests for the campaign compose screen's pure logic: status/mode
// vocabulary, transition-offer gates, CRUD-save validation (deliberately
// NOT the Preflight send gate — see the anti-drift test below), length
// guidance, the post-scheduling demotion detector, and cancel copy.

import { describe, it, expect } from 'vitest';
import type { Campaign } from './types';
import {
  CAMPAIGN_STATUSES,
  AUDIENCE_MODES,
  canEditCampaign,
  canSendCampaign,
  canCancelCampaign,
  canResumeCampaign,
  interestsApplyToMode,
  validateCampaignDraft,
  subjectLengthAdvice,
  preheaderLengthAdvice,
  wasDemotedAfterScheduling,
  demotionExplanation,
  cancelCopy,
  resumeCopy,
  campaignStatusLabel,
  campaignStatusBadgeClass,
} from './campaigns';
import { formatDateTime } from './admin';

function campaign(fields: Partial<Campaign>): Campaign {
  return {
    id: 1,
    name: 'n',
    subject: 's',
    body_md: 'b',
    status: 'draft',
    audience_mode: 'all',
    interest_ids: [],
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...fields,
  };
}

describe('canEditCampaign / canSendCampaign / canCancelCampaign', () => {
  const expected: Record<string, { edit: boolean; send: boolean; cancel: boolean }> = {
    draft: { edit: true, send: true, cancel: false },
    scheduled: { edit: true, send: false, cancel: true },
    sending: { edit: false, send: false, cancel: true },
    paused_delivery_health: { edit: false, send: false, cancel: true },
    sent: { edit: false, send: false, cancel: false },
    canceled: { edit: false, send: false, cancel: false },
    failed: { edit: false, send: false, cancel: false },
  };

  for (const status of CAMPAIGN_STATUSES) {
    it(`status=${status}`, () => {
      const want = expected[status];
      expect(canEditCampaign(status)).toBe(want.edit);
      expect(canSendCampaign(status)).toBe(want.send);
      expect(canCancelCampaign(status)).toBe(want.cancel);
    });
  }
});

describe('interestsApplyToMode', () => {
  const expected: Record<string, boolean> = {
    all: false,
    any_of: true,
    all_of: true,
    none_selected: false,
  };
  for (const mode of AUDIENCE_MODES) {
    it(`mode=${mode.value}`, () => {
      expect(interestsApplyToMode(mode.value)).toBe(expected[mode.value]);
    });
  }
});

describe('wasDemotedAfterScheduling', () => {
  it('true for draft + scheduled_at + no started_at', () => {
    expect(
      wasDemotedAfterScheduling(campaign({ status: 'draft', scheduled_at: '2026-01-02T00:00:00Z' })),
    ).toBe(true);
  });

  it('false for a plain draft (keys absent)', () => {
    const c = campaign({ status: 'draft' });
    expect('scheduled_at' in c).toBe(false);
    expect(wasDemotedAfterScheduling(c)).toBe(false);
  });

  it('false for a scheduled campaign', () => {
    expect(
      wasDemotedAfterScheduling(campaign({ status: 'scheduled', scheduled_at: '2026-01-02T00:00:00Z' })),
    ).toBe(false);
  });

  it('false for a canceled campaign', () => {
    expect(
      wasDemotedAfterScheduling(campaign({ status: 'canceled', scheduled_at: '2026-01-02T00:00:00Z' })),
    ).toBe(false);
  });

  it('false for a draft carrying started_at', () => {
    expect(
      wasDemotedAfterScheduling(
        campaign({
          status: 'draft',
          scheduled_at: '2026-01-02T00:00:00Z',
          started_at: '2026-01-02T00:05:00Z',
        }),
      ),
    ).toBe(false);
  });
});

describe('demotionExplanation', () => {
  // #0178: pin what the sentence CLAIMS, not one phrasing of it — a
  // phrase-match test here would be exactly the kind #0174 was filed over.

  it('names the scheduled time when scheduled_at is present', () => {
    const scheduledAt = '2026-03-04T15:30:00Z';
    const msg = demotionExplanation(campaign({ status: 'draft', scheduled_at: scheduledAt }));
    expect(msg).toContain(formatDateTime(scheduledAt));
  });

  it('falls back to unformatted wording when scheduled_at is absent', () => {
    const c = campaign({ status: 'draft' });
    expect('scheduled_at' in c).toBe(false);
    const msg = demotionExplanation(c);
    // No real timestamp exists to show, so nothing date-shaped should
    // appear — this is the branch's whole point, not a specific phrase.
    expect(/\d{4}/.test(msg)).toBe(false);
  });

  it('says sending was refused and the campaign is back in draft', () => {
    const msg = demotionExplanation(
      campaign({ status: 'draft', scheduled_at: '2026-03-04T15:30:00Z' }),
    ).toLowerCase();
    expect(msg).toContain('refused');
    expect(msg).toContain('draft');
  });

  it('does not name an internal component as the agent of the refusal (CLAUDE.md §9)', () => {
    // #0175 dropped "the send worker" deliberately: the property that
    // matters to an admin is that sending was refused, not who refused it.
    const msg = demotionExplanation(
      campaign({ status: 'draft', scheduled_at: '2026-03-04T15:30:00Z' }),
    ).toLowerCase();
    expect(msg).not.toContain('worker');
  });

  it('points at the checks panel as the actionable next step', () => {
    const msg = demotionExplanation(
      campaign({ status: 'draft', scheduled_at: '2026-03-04T15:30:00Z' }),
    ).toLowerCase();
    expect(msg).toContain('check');
  });
});

describe('validateCampaignDraft', () => {
  it('rejects a blank name', () => {
    const r = validateCampaignDraft({ name: '  ', subject: 's', bodyMd: 'b', mode: 'all', interestIds: [] });
    expect(r.ok).toBe(false);
  });

  it('rejects a blank subject', () => {
    const r = validateCampaignDraft({ name: 'n', subject: '  ', bodyMd: 'b', mode: 'all', interestIds: [] });
    expect(r.ok).toBe(false);
  });

  it('rejects a blank body', () => {
    const r = validateCampaignDraft({ name: 'n', subject: 's', bodyMd: '  ', mode: 'all', interestIds: [] });
    expect(r.ok).toBe(false);
  });

  it('rejects an empty interest set under any_of', () => {
    const r = validateCampaignDraft({ name: 'n', subject: 's', bodyMd: 'b', mode: 'any_of', interestIds: [] });
    expect(r.ok).toBe(false);
  });

  it('rejects an empty interest set under all_of', () => {
    const r = validateCampaignDraft({ name: 'n', subject: 's', bodyMd: 'b', mode: 'all_of', interestIds: [] });
    expect(r.ok).toBe(false);
  });

  // The anti-drift assertion: this must return ok for a draft that has no
  // test send, no physical address, and a zero audience — those are
  // #0045's Preflight requirements, not this function's. Restating any of
  // them here would turn this test red.
  it('is ok for a draft with no test send, no physical address, and a zero audience', () => {
    const r = validateCampaignDraft({ name: 'n', subject: 's', bodyMd: 'b', mode: 'all', interestIds: [] });
    expect(r.ok).toBe(true);
  });

  it('an over-length subject does not change the verdict', () => {
    const longSubject = 'x'.repeat(200);
    const r = validateCampaignDraft({
      name: 'n',
      subject: longSubject,
      bodyMd: 'b',
      mode: 'all',
      interestIds: [],
    });
    expect(r.ok).toBe(true);
  });
});

describe('subjectLengthAdvice', () => {
  it('ok at the low boundary', () => {
    expect(subjectLengthAdvice(50).tone).toBe('ok');
  });
  it('warn just past the low boundary', () => {
    expect(subjectLengthAdvice(51).tone).toBe('warn');
  });
  it('warn at the high boundary', () => {
    expect(subjectLengthAdvice(78).tone).toBe('warn');
  });
  it('over just past the high boundary', () => {
    expect(subjectLengthAdvice(79).tone).toBe('over');
  });
});

describe('preheaderLengthAdvice', () => {
  it('ok at the low boundary', () => {
    expect(preheaderLengthAdvice(80).tone).toBe('ok');
  });
  it('warn just past the low boundary', () => {
    expect(preheaderLengthAdvice(81).tone).toBe('warn');
  });
  it('warn at the high boundary', () => {
    expect(preheaderLengthAdvice(100).tone).toBe('warn');
  });
  it('over just past the high boundary', () => {
    expect(preheaderLengthAdvice(101).tone).toBe('over');
  });
});

describe('cancelCopy', () => {
  it('differs between scheduled and sending', () => {
    expect(cancelCopy('scheduled')).not.toBe(cancelCopy('sending'));
  });

  it('contains no digits, in either case, when remaining is omitted', () => {
    expect(/\d/.test(cancelCopy('scheduled'))).toBe(false);
    expect(/\d/.test(cancelCopy('sending'))).toBe(false);
  });

  // #0048: remaining is the live remaining-recipient count only #0048's SSE
  // stream knows — see lib/campaignProgress.ts's remainingForCancel.
  it('still contains no digits for scheduled even when a remaining count is passed', () => {
    // scheduled never had anything sent yet; a "remaining" count would be
    // meaningless there, so it must be ignored.
    expect(/\d/.test(cancelCopy('scheduled', 706))).toBe(false);
  });

  it('includes the remaining count for sending when supplied', () => {
    const msg = cancelCopy('sending', 706);
    expect(msg).toContain('706');
  });

  it('uses singular "1 recipient" for exactly one remaining', () => {
    const msg = cancelCopy('sending', 1);
    expect(msg).toContain('1 recipient');
    expect(msg).not.toContain('1 recipients');
  });

  it('uses plural "recipients" for a remaining count other than 1, including 0', () => {
    expect(cancelCopy('sending', 0)).toContain('0 recipients');
    expect(cancelCopy('sending', 2)).toContain('2 recipients');
  });

  it('falls back to the no-digits wording when remaining is undefined', () => {
    expect(cancelCopy('sending', undefined)).toBe(cancelCopy('sending'));
  });

  // #0124: a paused campaign has already sent some recipients, exactly like
  // a sending one — "nothing has been sent yet" would be actively wrong.
  it('treats paused_delivery_health like sending, not like scheduled', () => {
    expect(cancelCopy('paused_delivery_health')).toBe(cancelCopy('sending'));
    expect(cancelCopy('paused_delivery_health')).not.toBe(cancelCopy('scheduled'));
  });

  it('includes the remaining count for paused_delivery_health when supplied', () => {
    expect(cancelCopy('paused_delivery_health', 42)).toContain('42');
  });
});

// #0124: the circuit breaker's recovery path.
describe('canResumeCampaign', () => {
  for (const status of CAMPAIGN_STATUSES) {
    it(`status=${status}`, () => {
      expect(canResumeCampaign(status)).toBe(status === 'paused_delivery_health');
    });
  }
});

describe('resumeCopy', () => {
  it('returns non-empty, stable copy', () => {
    const msg = resumeCopy();
    expect(msg.length).toBeGreaterThan(0);
    expect(resumeCopy()).toBe(msg);
  });
});

describe('campaignStatusLabel / campaignStatusBadgeClass', () => {
  it('gives paused_delivery_health a distinct, non-empty label from every other status', () => {
    const label = campaignStatusLabel('paused_delivery_health');
    expect(label.length).toBeGreaterThan(0);
    for (const status of CAMPAIGN_STATUSES) {
      if (status === 'paused_delivery_health') continue;
      expect(campaignStatusLabel(status)).not.toBe(label);
    }
  });

  it('returns the input verbatim for an unrecognized status', () => {
    expect(campaignStatusLabel('some_future_status')).toBe('some_future_status');
  });

  it('badges paused_delivery_health as danger, like failed — both demand operator attention', () => {
    expect(campaignStatusBadgeClass('paused_delivery_health')).toBe(campaignStatusBadgeClass('failed'));
    expect(campaignStatusBadgeClass('paused_delivery_health')).toBe('badge-danger');
  });

  it('every CAMPAIGN_STATUSES value maps to a known badge class', () => {
    const known = new Set(['badge-success', 'badge-danger', 'badge-muted']);
    for (const status of CAMPAIGN_STATUSES) {
      expect(known.has(campaignStatusBadgeClass(status))).toBe(true);
    }
  });
});
