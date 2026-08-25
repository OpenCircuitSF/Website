// Unit tests for the Admin pure logic: the deactivation reason value ↔ label
// map, the deactivation `other`-requires-note validation, audit
// actor/target/metadata rendering, the user-id filter parser, and the
// pagination math. No DOM or network — only the data shaping the Admin view
// delegates to lib/admin.ts.

import { describe, it, expect } from 'vitest';
import type { AdminUser, AuditEntry, Interest, Subscriber } from './types';
import {
  DEACTIVATION_REASONS,
  deactivationReasonLabel,
  isValidDeactivationReason,
  validateDeactivation,
  canDeactivate,
  actorLabel,
  targetLabel,
  formatMetadata,
  formatDateTime,
  pageInfo,
  parseUserIdFilter,
  parseTargetFilter,
  campaignAuditFilter,
  AUDIT_TARGET_EMAIL_CAMPAIGN,
  registrationsEnabled,
  isValidInterestSlug,
  validateNewInterest,
  sortedInterests,
  reorderSwap,
  hasSubscribers,
  subscriberStatusLabel,
  subscriberEventActionLabel,
  subscriberStatusBadgeClass,
  canClearComplaint,
  subscribersExportHref,
  isPlausibleEmail,
  validateManualAddEmail,
  validateSuppressNote,
  signupEvidenceSummary,
  softBounceSummary,
  SUPPRESSION_REASONS,
  suppressionReasonLabel,
  validateSuppressionNote,
  suppressionRemovalBlocked,
} from './admin';

function adminUser(overrides: Partial<AdminUser> = {}): AdminUser {
  return {
    id: 2,
    email: 'user@example.com',
    is_admin: false,
    active: true,
    created_at: '2026-05-25T12:00:00Z',
    ...overrides,
  };
}

function auditEntry(overrides: Partial<AuditEntry> = {}): AuditEntry {
  return {
    id: 1,
    actor_id: 1,
    user_id: null,
    action: 'settings.updated',
    target_type: 'settings',
    target_id: null,
    metadata: null,
    ip_address: '127.0.0.1',
    created_at: '2026-05-25T12:00:00Z',
    ...overrides,
  };
}

describe('deactivation reasons', () => {
  it('offers exactly the six PRD reason values in order', () => {
    expect(DEACTIVATION_REASONS.map((d) => d.value)).toEqual([
      'malware_distribution',
      'phishing',
      'spam',
      'harassment',
      'terms_violation',
      'other',
    ]);
  });

  it('labels a known value and passes through an unknown one', () => {
    expect(deactivationReasonLabel('phishing')).toBe('Phishing');
    expect(deactivationReasonLabel('terms_violation')).toBe('Terms of service violation');
    expect(deactivationReasonLabel('mystery')).toBe('mystery');
  });

  it('validates membership', () => {
    expect(isValidDeactivationReason('spam')).toBe(true);
    expect(isValidDeactivationReason('nope')).toBe(false);
    expect(isValidDeactivationReason('')).toBe(false);
  });
});

describe('validateDeactivation', () => {
  it('rejects an unknown reason', () => {
    expect(validateDeactivation('', 'x')).toEqual({ error: 'Select a deactivation reason.' });
    expect(validateDeactivation('bogus', 'x')).toEqual({ error: 'Select a deactivation reason.' });
  });

  it('requires a note when reason is other', () => {
    expect(validateDeactivation('other', '')).toEqual({
      error: 'A note is required when the reason is "Other".',
    });
    expect(validateDeactivation('other', '   ')).toEqual({
      error: 'A note is required when the reason is "Other".',
    });
  });

  it('accepts other with a non-empty note (trimmed)', () => {
    expect(validateDeactivation('other', '  see ticket  ')).toEqual({ note: 'see ticket' });
  });

  it('allows an empty note for non-other reasons', () => {
    expect(validateDeactivation('spam', '')).toEqual({ note: '' });
    expect(validateDeactivation('phishing', '  noted  ')).toEqual({ note: 'noted' });
  });
});

describe('canDeactivate', () => {
  it('offers deactivate for an active non-admin who is not the current user', () => {
    expect(canDeactivate(adminUser({ id: 2 }), 1)).toBe(true);
  });

  it('refuses for an admin, for self, and for an already-inactive user', () => {
    expect(canDeactivate(adminUser({ id: 2, is_admin: true }), 1)).toBe(false);
    expect(canDeactivate(adminUser({ id: 1 }), 1)).toBe(false);
    expect(canDeactivate(adminUser({ id: 2, active: false }), 1)).toBe(false);
  });
});

describe('audit rendering', () => {
  it('labels a NULL actor as system and a numeric actor as user #N', () => {
    expect(actorLabel(auditEntry({ actor_id: null }))).toBe('system');
    expect(actorLabel(auditEntry({ actor_id: 7 }))).toBe('user #7');
  });

  it('labels the target with type and optional id', () => {
    expect(targetLabel(auditEntry({ target_type: null }))).toBe('—');
    expect(targetLabel(auditEntry({ target_type: 'settings', target_id: null }))).toBe('settings');
    expect(targetLabel(auditEntry({ target_type: 'user', target_id: 5 }))).toBe('user #5');
  });

  it('renders metadata as a sorted compact key=value string', () => {
    expect(formatMetadata({ new_value: 'true', key: 'registrations_enabled' })).toBe(
      'key=registrations_enabled, new_value=true',
    );
  });

  it('handles null, empty, primitive, and nested metadata', () => {
    expect(formatMetadata(null)).toBe('');
    expect(formatMetadata({})).toBe('');
    expect(formatMetadata('boom')).toBe('boom');
    expect(formatMetadata({ note: null, reason: 'spam' })).toBe('note=, reason=spam');
    expect(formatMetadata({ list: [1, 2] })).toBe('list=[1,2]');
  });

  it('formats a timestamp and falls back on a bad one', () => {
    expect(formatDateTime('not-a-date')).toBe('not-a-date');
    expect(formatDateTime('2026-05-25T12:00:00Z')).not.toBe('2026-05-25T12:00:00Z');
  });
});

describe('parseUserIdFilter', () => {
  it('treats empty as no filter', () => {
    expect(parseUserIdFilter('')).toEqual({ userId: null });
    expect(parseUserIdFilter('   ')).toEqual({ userId: null });
  });

  it('parses a positive integer', () => {
    expect(parseUserIdFilter(' 42 ')).toEqual({ userId: 42 });
  });

  it('rejects non-numeric or non-positive input', () => {
    expect(parseUserIdFilter('abc')).toEqual({ error: 'Enter a numeric user id.' });
    expect(parseUserIdFilter('0')).toEqual({ error: 'Enter a numeric user id.' });
    expect(parseUserIdFilter('-3')).toEqual({ error: 'Enter a numeric user id.' });
    expect(parseUserIdFilter('1.5')).toEqual({ error: 'Enter a numeric user id.' });
  });
});

describe('parseTargetFilter', () => {
  it('treats both fields empty as no filter', () => {
    expect(parseTargetFilter('', '')).toEqual({ targetType: null, targetId: null });
    expect(parseTargetFilter('  ', '  ')).toEqual({ targetType: null, targetId: null });
  });

  it('filters by target_type alone when target_id is empty', () => {
    expect(parseTargetFilter('email_campaign', '')).toEqual({
      targetType: 'email_campaign',
      targetId: null,
    });
  });

  it('parses a target_type/target_id pair', () => {
    expect(parseTargetFilter('email_campaign', ' 42 ')).toEqual({
      targetType: 'email_campaign',
      targetId: 42,
    });
  });

  it('rejects a target_id without a target_type — a bare id is ambiguous across types (#0114)', () => {
    expect(parseTargetFilter('', '42')).toEqual({
      error: 'Target type is required when target id is set.',
    });
  });

  it('rejects a non-numeric or non-positive target_id', () => {
    expect(parseTargetFilter('email_campaign', 'abc')).toEqual({
      error: 'Enter a numeric target id.',
    });
    expect(parseTargetFilter('email_campaign', '0')).toEqual({
      error: 'Enter a numeric target id.',
    });
    expect(parseTargetFilter('email_campaign', '-3')).toEqual({
      error: 'Enter a numeric target id.',
    });
  });
});

describe('campaignAuditFilter', () => {
  it('builds the email_campaign target filter for a campaign id', () => {
    expect(campaignAuditFilter(42)).toEqual({
      targetType: AUDIT_TARGET_EMAIL_CAMPAIGN,
      targetId: 42,
    });
  });

  it('uses the same target_type string the server writes (internal/audit.TargetEmailCampaign)', () => {
    expect(AUDIT_TARGET_EMAIL_CAMPAIGN).toBe('email_campaign');
  });
});

describe('pageInfo', () => {
  it('computes pages, range, and nav flags for a middle page', () => {
    const info = pageInfo(125, 2, 50);
    expect(info.totalPages).toBe(3);
    expect(info.firstItem).toBe(51);
    expect(info.lastItem).toBe(100);
    expect(info.hasPrev).toBe(true);
    expect(info.hasNext).toBe(true);
  });

  it('treats an empty list as page 1 of 1 with a 0 range', () => {
    const info = pageInfo(0, 1, 50);
    expect(info.totalPages).toBe(1);
    expect(info.firstItem).toBe(0);
    expect(info.lastItem).toBe(0);
    expect(info.hasPrev).toBe(false);
    expect(info.hasNext).toBe(false);
  });

  it('caps the last item at total on the final partial page', () => {
    const info = pageInfo(125, 3, 50);
    expect(info.firstItem).toBe(101);
    expect(info.lastItem).toBe(125);
    expect(info.hasNext).toBe(false);
    expect(info.hasPrev).toBe(true);
  });

  it('clamps an out-of-range page and bad per_page defensively', () => {
    expect(pageInfo(10, 99, 50).page).toBe(1);
    expect(pageInfo(10, 0, 0).perPage).toBe(1);
  });
});

describe('registrationsEnabled', () => {
  it('is true only for the literal "true"', () => {
    expect(registrationsEnabled('true')).toBe(true);
    expect(registrationsEnabled('false')).toBe(false);
    expect(registrationsEnabled(undefined)).toBe(false);
    expect(registrationsEnabled('TRUE')).toBe(false);
  });
});

function interest(overrides: Partial<Interest> = {}): Interest {
  return {
    id: 1,
    slug: 'home-automation',
    name: 'Home Automation',
    sort_order: 40,
    active: true,
    subscriber_count: 0,
    created_at: '2026-05-25T12:00:00Z',
    ...overrides,
  };
}

describe('isValidInterestSlug', () => {
  it('accepts lowercase hyphenated slugs', () => {
    expect(isValidInterestSlug('home-automation')).toBe(true);
    expect(isValidInterestSlug('3d-printing')).toBe(true);
    expect(isValidInterestSlug('beginner')).toBe(true);
  });

  it('rejects uppercase, underscores, and leading/trailing/doubled hyphens', () => {
    expect(isValidInterestSlug('Upper-Case')).toBe(false);
    expect(isValidInterestSlug('has_underscore')).toBe(false);
    expect(isValidInterestSlug('-leading')).toBe(false);
    expect(isValidInterestSlug('trailing-')).toBe(false);
    expect(isValidInterestSlug('double--hyphen')).toBe(false);
    expect(isValidInterestSlug('')).toBe(false);
  });
});

describe('validateNewInterest', () => {
  it('rejects an invalid slug', () => {
    expect(validateNewInterest('Bad Slug', 'Name')).toEqual({
      error: 'Slug must be lowercase letters, numbers, and single hyphens (e.g. "home-automation").',
    });
  });

  it('rejects an empty name', () => {
    expect(validateNewInterest('valid-slug', '  ')).toEqual({ error: 'Name is required.' });
  });

  it('trims and accepts a valid slug and name', () => {
    expect(validateNewInterest('  valid-slug  ', '  Valid Name  ')).toEqual({
      slug: 'valid-slug',
      name: 'Valid Name',
    });
  });
});

describe('sortedInterests', () => {
  it('orders by sort_order then name, without mutating the input', () => {
    const list = [
      interest({ id: 3, name: 'C', sort_order: 20 }),
      interest({ id: 1, name: 'B', sort_order: 10 }),
      interest({ id: 2, name: 'A', sort_order: 10 }),
    ];
    const original = [...list];
    const sorted = sortedInterests(list);
    expect(sorted.map((i) => i.id)).toEqual([2, 1, 3]);
    expect(list).toEqual(original);
  });
});

describe('reorderSwap', () => {
  const list = [
    interest({ id: 1, name: 'First', sort_order: 10 }),
    interest({ id: 2, name: 'Second', sort_order: 20 }),
    interest({ id: 3, name: 'Third', sort_order: 30 }),
  ];

  it('swaps sort_order with the previous item when moving up', () => {
    expect(reorderSwap(list, 2, 'up')).toEqual({
      moved: { id: 2, sortOrder: 10 },
      other: { id: 1, sortOrder: 20 },
    });
  });

  it('swaps sort_order with the next item when moving down', () => {
    expect(reorderSwap(list, 2, 'down')).toEqual({
      moved: { id: 2, sortOrder: 30 },
      other: { id: 3, sortOrder: 20 },
    });
  });

  it('returns null at the top/bottom edge or for an unknown id', () => {
    expect(reorderSwap(list, 1, 'up')).toBeNull();
    expect(reorderSwap(list, 3, 'down')).toBeNull();
    expect(reorderSwap(list, 999, 'up')).toBeNull();
  });
});

describe('hasSubscribers', () => {
  it('is true only when subscriber_count is greater than zero', () => {
    expect(hasSubscribers(interest({ subscriber_count: 0 }))).toBe(false);
    expect(hasSubscribers(interest({ subscriber_count: 3 }))).toBe(true);
  });
});

// ── Subscribers screen (#0032) ────────────────────────────────────────────────

function subscriber(overrides: Partial<Subscriber> = {}): Subscriber {
  return {
    id: 1,
    email: 'person@example.com',
    status: 'active',
    created_at: '2026-05-25T12:00:00Z',
    email_events: [],
    ...overrides,
  };
}

describe('subscriberStatusLabel', () => {
  it('capitalizes every known status', () => {
    expect(subscriberStatusLabel('pending')).toBe('Pending');
    expect(subscriberStatusLabel('active')).toBe('Active');
    expect(subscriberStatusLabel('unsubscribed')).toBe('Unsubscribed');
    expect(subscriberStatusLabel('bounced')).toBe('Bounced');
    expect(subscriberStatusLabel('complained')).toBe('Complained');
  });

  it('returns an unknown value as-is', () => {
    expect(subscriberStatusLabel('mystery')).toBe('mystery');
  });
});

describe('subscriberEventActionLabel', () => {
  it('labels every action in the closed set (#0126, PRD §6.11)', () => {
    const actions = [
      'signup_requested',
      'confirmation_sent',
      'confirmed',
      'confirmation_expired',
      'welcome_sent',
      'interests_changed',
      'unsubscribed',
      'resubscribed',
      'imported',
      'invite_sent',
      'invite_accepted',
      'invite_expired',
      'import_revoked',
      'campaign_sent',
      'bounced_soft',
      'bounced_hard',
      'complained',
      'delivered',
      'suppressed',
      'unsuppressed',
      'admin_edited',
      'erased',
    ];
    for (const action of actions) {
      const label = subscriberEventActionLabel(action);
      expect(label, `action ${action} has no distinct label`).not.toBe(action);
      expect(label.length).toBeGreaterThan(0);
    }
  });

  it('returns an unknown value as-is', () => {
    expect(subscriberEventActionLabel('mystery')).toBe('mystery');
  });
});

describe('subscriberStatusBadgeClass', () => {
  it('is badge-success only for active', () => {
    expect(subscriberStatusBadgeClass('active')).toBe('badge-success');
  });

  it('is badge-danger for bounced and complained', () => {
    expect(subscriberStatusBadgeClass('bounced')).toBe('badge-danger');
    expect(subscriberStatusBadgeClass('complained')).toBe('badge-danger');
  });

  it('is badge-muted for pending and unsubscribed', () => {
    expect(subscriberStatusBadgeClass('pending')).toBe('badge-muted');
    expect(subscriberStatusBadgeClass('unsubscribed')).toBe('badge-muted');
  });
});

describe('canClearComplaint', () => {
  it('is true only for complained — the server refuses (409) on every other status', () => {
    expect(canClearComplaint('complained')).toBe(true);
    expect(canClearComplaint('active')).toBe(false);
    expect(canClearComplaint('pending')).toBe(false);
    expect(canClearComplaint('unsubscribed')).toBe(false);
    expect(canClearComplaint('bounced')).toBe(false);
  });
});

describe('subscribersExportHref (#0219)', () => {
  it('with no filter, points at the bare export endpoint', () => {
    expect(subscribersExportHref()).toBe('/admin/subscribers/export');
    expect(subscribersExportHref({})).toBe('/admin/subscribers/export');
  });

  it('carries status alone', () => {
    expect(subscribersExportHref({ status: 'active' })).toBe('/admin/subscribers/export?status=active');
  });

  it('carries interestId alone, as interest_id', () => {
    expect(subscribersExportHref({ interestId: 7 })).toBe('/admin/subscribers/export?interest_id=7');
  });

  it('carries q alone, URL-encoded', () => {
    expect(subscribersExportHref({ q: 'a b@example.com' })).toBe(
      '/admin/subscribers/export?q=a+b%40example.com',
    );
  });

  it('carries all three together, matching what admin_subscribers_export.go reads (status, interest_id, q)', () => {
    expect(subscribersExportHref({ status: 'pending', interestId: 3, q: 'example.com' })).toBe(
      '/admin/subscribers/export?status=pending&interest_id=3&q=example.com',
    );
  });

  it('omits a param entirely when its value is falsy (empty string, 0, undefined) — never emits status= or interest_id=0', () => {
    expect(subscribersExportHref({ status: '', interestId: 0, q: '' })).toBe('/admin/subscribers/export');
  });
});

describe('isPlausibleEmail', () => {
  it('accepts an ordinary address', () => {
    expect(isPlausibleEmail('person@example.com')).toBe(true);
  });

  it('rejects a string with no @ or no domain dot', () => {
    expect(isPlausibleEmail('not-an-email')).toBe(false);
    expect(isPlausibleEmail('person@localhost')).toBe(false);
  });
});

describe('validateManualAddEmail', () => {
  it('trims and accepts a valid address', () => {
    expect(validateManualAddEmail('  person@example.com  ')).toEqual({
      email: 'person@example.com',
    });
  });

  it('rejects an invalid address with an error message', () => {
    const result = validateManualAddEmail('nope');
    expect('error' in result).toBe(true);
  });
});

describe('validateSuppressNote', () => {
  it('rejects a blank or whitespace-only note', () => {
    expect('error' in validateSuppressNote('')).toBe(true);
    expect('error' in validateSuppressNote('   ')).toBe(true);
  });

  it('trims and accepts a non-empty note', () => {
    expect(validateSuppressNote('  requested by phone  ')).toEqual({
      note: 'requested by phone',
    });
  });
});

describe('signupEvidenceSummary', () => {
  it('includes IP and user agent when present', () => {
    const summary = signupEvidenceSummary(
      subscriber({ signup_ip: '203.0.113.5', signup_user_agent: 'Mozilla/5.0' }),
    );
    expect(summary).toContain('203.0.113.5');
    expect(summary).toContain('Mozilla/5.0');
  });

  it('notes the absence of browser evidence for a manually-added subscriber', () => {
    const summary = signupEvidenceSummary(subscriber({ signup_ip: undefined, signup_user_agent: undefined }));
    expect(summary).toContain('no browser signup evidence');
  });
});

describe('softBounceSummary', () => {
  it('reports "Not available." when the detail endpoint has not populated the count', () => {
    expect(softBounceSummary(subscriber())).toBe('Not available.');
  });

  it('reports the count, threshold, and window when under threshold', () => {
    const summary = softBounceSummary(
      subscriber({ soft_bounce_count: 2, soft_bounce_threshold: 5, soft_bounce_window_days: 30 }),
    );
    expect(summary).toContain('2 soft bounces');
    expect(summary).toContain('last 30 days');
    expect(summary).toContain('threshold: 5');
    expect(summary).not.toContain('should already be suppressed');
  });

  it('uses the singular "bounce" for a count of exactly 1', () => {
    const summary = softBounceSummary(
      subscriber({ soft_bounce_count: 1, soft_bounce_threshold: 5, soft_bounce_window_days: 30 }),
    );
    expect(summary).toContain('1 soft bounce ');
    expect(summary).not.toContain('bounces');
  });

  it('flags the address as already suppressed once the count reaches the threshold', () => {
    const summary = softBounceSummary(
      subscriber({ soft_bounce_count: 5, soft_bounce_threshold: 5, soft_bounce_window_days: 30 }),
    );
    expect(summary).toContain('should already be suppressed');
  });
});

describe('suppressionReasonLabel', () => {
  it('labels all four known reasons', () => {
    for (const reason of SUPPRESSION_REASONS) {
      expect(suppressionReasonLabel(reason).length).toBeGreaterThan(0);
    }
    expect(suppressionReasonLabel('hard_bounce')).toBe('Hard bounce');
    expect(suppressionReasonLabel('complaint')).toBe('Complaint');
    expect(suppressionReasonLabel('manual')).toBe('Manual');
    expect(suppressionReasonLabel('repeated_soft_bounce')).toBe('Repeated soft bounce');
  });

  it('returns an unknown value as-is', () => {
    expect(suppressionReasonLabel('made_up_reason')).toBe('made_up_reason');
  });
});

describe('validateSuppressionNote', () => {
  it('rejects a blank or whitespace-only note', () => {
    expect('error' in validateSuppressionNote('')).toBe(true);
    expect('error' in validateSuppressionNote('   ')).toBe(true);
  });

  it('trims and accepts a non-empty note', () => {
    expect(validateSuppressionNote('  confirmed with the subscriber  ')).toEqual({
      note: 'confirmed with the subscriber',
    });
  });
});

describe('suppressionRemovalBlocked', () => {
  it('blocks a complaint removal when a subscriber record exists', () => {
    expect(suppressionRemovalBlocked('complaint', true)).not.toBeNull();
  });

  it('allows a complaint removal when the suppression is orphaned', () => {
    expect(suppressionRemovalBlocked('complaint', false)).toBeNull();
  });

  it('never blocks a non-complaint reason, with or without a subscriber', () => {
    for (const reason of ['hard_bounce', 'manual', 'repeated_soft_bounce']) {
      expect(suppressionRemovalBlocked(reason, true)).toBeNull();
      expect(suppressionRemovalBlocked(reason, false)).toBeNull();
    }
  });
});
