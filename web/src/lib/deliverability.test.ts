import { describe, it, expect } from 'vitest';
import {
  deliverabilityEventLabel,
  deliverabilityEventBadgeClass,
  streakSummary,
} from './deliverability';
import type { DeliverabilityEvent } from './types';

function event(fields: Partial<DeliverabilityEvent>): DeliverabilityEvent {
  return { event_type: 'Bounce', timestamp: '2026-01-01T00:00:00Z', ...fields };
}

describe('deliverabilityEventLabel', () => {
  it('includes bounce type and subtype when both are present', () => {
    expect(deliverabilityEventLabel(event({ event_type: 'Bounce', bounce_type: 'Transient', bounce_subtype: 'General' }))).toBe(
      'Bounce — Transient (General)',
    );
  });

  it('includes bounce type alone when subtype is absent', () => {
    expect(deliverabilityEventLabel(event({ event_type: 'Bounce', bounce_type: 'Permanent' }))).toBe('Bounce — Permanent');
  });

  it('falls back to the bare event type for a Bounce row with no bounce_type', () => {
    expect(deliverabilityEventLabel(event({ event_type: 'Bounce' }))).toBe('Bounce');
  });

  it('is just the event type for non-Bounce events', () => {
    expect(deliverabilityEventLabel(event({ event_type: 'Complaint' }))).toBe('Complaint');
    expect(deliverabilityEventLabel(event({ event_type: 'Delivery' }))).toBe('Delivery');
    expect(deliverabilityEventLabel(event({ event_type: 'Reject' }))).toBe('Reject');
  });
});

describe('deliverabilityEventBadgeClass', () => {
  it('is danger for a Complaint', () => {
    expect(deliverabilityEventBadgeClass(event({ event_type: 'Complaint' }))).toBe('badge-danger');
  });

  it('is danger for a Permanent bounce', () => {
    expect(deliverabilityEventBadgeClass(event({ event_type: 'Bounce', bounce_type: 'Permanent' }))).toBe('badge-danger');
  });

  it('is muted for a Transient or Undetermined bounce', () => {
    expect(deliverabilityEventBadgeClass(event({ event_type: 'Bounce', bounce_type: 'Transient' }))).toBe('badge-muted');
    expect(deliverabilityEventBadgeClass(event({ event_type: 'Bounce', bounce_type: 'Undetermined' }))).toBe('badge-muted');
  });

  it('is success for a Delivery', () => {
    expect(deliverabilityEventBadgeClass(event({ event_type: 'Delivery' }))).toBe('badge-success');
  });

  it('is muted for anything else', () => {
    for (const t of ['Reject', 'RenderingFailure', 'DeliveryDelay', 'Send', 'SomeFutureType']) {
      expect(deliverabilityEventBadgeClass(event({ event_type: t }))).toBe('badge-muted');
    }
  });
});

describe('streakSummary', () => {
  it('marks a zero streak as reset', () => {
    expect(streakSummary(0)).toBe('0 (reset)');
  });

  it('is the bare number for any positive streak', () => {
    expect(streakSummary(1)).toBe('1');
    expect(streakSummary(5)).toBe('5');
  });
});
