import { describe, it, expect } from 'vitest';
import {
  formatPendingAge,
  pendingSignupSource,
  queueStateLabel,
  queueStateBadgeClass,
  pendingExpiredBadgeClass,
  inviteResendButtonTitle,
  sortPendingByAge,
} from './pending';
import type { PendingSubscriber } from './types';

describe('formatPendingAge', () => {
  it('reads "just now" under a minute', () => {
    expect(formatPendingAge(0)).toBe('just now');
    expect(formatPendingAge(59)).toBe('just now');
  });

  it('renders whole minutes, singular and plural', () => {
    expect(formatPendingAge(60)).toBe('1 minute');
    expect(formatPendingAge(120)).toBe('2 minutes');
    expect(formatPendingAge(3599)).toBe('59 minutes');
  });

  it('renders whole hours once past 60 minutes', () => {
    expect(formatPendingAge(3600)).toBe('1 hour');
    expect(formatPendingAge(7200)).toBe('2 hours');
    expect(formatPendingAge(86399)).toBe('23 hours');
  });

  it('renders whole days once past 24 hours', () => {
    expect(formatPendingAge(86400)).toBe('1 day');
    expect(formatPendingAge(86400 * 6)).toBe('6 days');
  });

  it('clamps a negative value (clock skew) to "just now" rather than going negative', () => {
    expect(formatPendingAge(-5)).toBe('just now');
  });
});

describe('pendingSignupSource', () => {
  it('reports "Direct" when no UTM fields are set', () => {
    expect(pendingSignupSource({})).toBe('Direct');
    expect(pendingSignupSource({ utm_source: '', utm_medium: undefined, utm_campaign: '  ' })).toBe('Direct');
  });

  it('joins whichever UTM fields are present', () => {
    expect(pendingSignupSource({ utm_source: 'newsletter' })).toBe('newsletter');
    expect(pendingSignupSource({ utm_source: 'newsletter', utm_medium: 'email' })).toBe('newsletter / email');
    expect(
      pendingSignupSource({ utm_source: 'newsletter', utm_medium: 'email', utm_campaign: 'launch' }),
    ).toBe('newsletter / email / launch');
  });
});

describe('queueStateLabel', () => {
  it('maps every known state to an operator-facing label', () => {
    expect(queueStateLabel('queued')).toBe('Queued');
    expect(queueStateLabel('sending')).toBe('Sending');
    expect(queueStateLabel('sent')).toBe('Sent');
    expect(queueStateLabel('abandoned')).toBe('Abandoned');
    expect(queueStateLabel('skipped')).toBe('Skipped');
    expect(queueStateLabel('none')).toBe('Not sent');
  });

  it('falls back to "Unknown" for an unrecognized or server-absent state', () => {
    expect(queueStateLabel('unknown')).toBe('Unknown');
    expect(queueStateLabel('something-new')).toBe('Unknown');
  });
});

describe('queueStateBadgeClass', () => {
  it('flags abandoned as a delivery failure, distinct from a merely-pending state', () => {
    expect(queueStateBadgeClass('abandoned')).toBe('badge-danger');
    expect(queueStateBadgeClass('sent')).toBe('badge-success');
    expect(queueStateBadgeClass('queued')).toBe('badge-muted');
    expect(queueStateBadgeClass('sending')).toBe('badge-muted');
    expect(queueStateBadgeClass('none')).toBe('badge-muted');
  });

  it('treats skipped as neutral, not a delivery failure (#0365 — a deliberate withholding, not an abandoned send)', () => {
    expect(queueStateBadgeClass('skipped')).toBe('badge-muted');
  });
});

describe('pendingExpiredBadgeClass', () => {
  it('is visually distinct for an expired row', () => {
    expect(pendingExpiredBadgeClass(true)).toBe('badge-danger');
    expect(pendingExpiredBadgeClass(false)).toBe('badge-muted');
  });
});

// inviteResendButtonTitle has no plain-module test until #0367 — the only
// prior coverage was indirect, through a jsdom mount of Pending.svelte
// (Pending.behavior.test.ts). These call the pure function directly (no
// DOM), per CLAUDE.md §1's "cheap path" — and, since #0367, the function no
// longer reads invite_resent_at at all (see its own doc comment), so these
// exercise only invite_resend_available.
describe('inviteResendButtonTitle', () => {
  it('states the one-and-only bound when the re-send is still available', () => {
    expect(inviteResendButtonTitle({ invite_resend_available: true })).toBe(
      'This is the one and only re-send this address will ever get.',
    );
  });

  it('names why the button is disabled once the re-send has been used', () => {
    expect(inviteResendButtonTitle({ invite_resend_available: false })).toBe(
      'This address has already received its one invitation re-send.',
    );
  });
});

function row(id: number, ageSeconds: number): PendingSubscriber {
  return {
    id,
    email: `zz-${id}@example.com`,
    confirm_sent_at: null,
    confirm_expires_at: null,
    age_seconds: ageSeconds,
    expired: false,
    invited: false,
    invite_resend_available: false,
    queue_state: 'queued',
  };
}

describe('sortPendingByAge', () => {
  it('sorts oldest (largest age_seconds) first by default', () => {
    const rows = [row(1, 10), row(2, 100), row(3, 50)];
    const sorted = sortPendingByAge(rows, true);
    expect(sorted.map((r) => r.id)).toEqual([2, 3, 1]);
  });

  it('reverses to newest-first when oldestFirst is false', () => {
    const rows = [row(1, 10), row(2, 100), row(3, 50)];
    const sorted = sortPendingByAge(rows, false);
    expect(sorted.map((r) => r.id)).toEqual([1, 3, 2]);
  });

  it('does not mutate the input array', () => {
    const rows = [row(1, 10), row(2, 100)];
    const original = [...rows];
    sortPendingByAge(rows, true);
    expect(rows).toEqual(original);
  });
});
