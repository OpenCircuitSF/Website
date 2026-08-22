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
  interestsApplyToMode,
  validateCampaignDraft,
  subjectLengthAdvice,
  preheaderLengthAdvice,
  wasDemotedAfterScheduling,
  cancelCopy,
} from './campaigns';

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

  it('contains no digits, in either case', () => {
    expect(/\d/.test(cancelCopy('scheduled'))).toBe(false);
    expect(/\d/.test(cancelCopy('sending'))).toBe(false);
  });
});
