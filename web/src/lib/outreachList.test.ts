import { describe, expect, it } from 'vitest';
import type { AdminWorkshop, ConnectionGroup, OutreachSummary } from './types';
import {
  upcomingPublished,
  outreachChip,
  outreachRowButtonLabel,
  editorOutreachLinkLabel,
  unannouncedUpcoming,
  outreachAttentionItems,
} from './outreachList';

const NOW = new Date('2026-10-18T12:00:00Z');

function ws(id: number, over: Partial<AdminWorkshop> = {}): AdminWorkshop {
  return {
    id, slug: `w${id}`, title: `W${id}`, status: 'published', starts_at: '2026-11-01T01:30:00Z',
    interest_ids: [], created_at: '', updated_at: '', ...over,
  };
}
function sum(id: number, counts: Record<string, number>): OutreachSummary {
  return {
    outreach_id: id, workshop_id: id, workshop_slug: '', workshop_title: '', workshop_status: 'published',
    campaign_slug: '', starts_at: null, counts, created_at: '',
  };
}
function group(name: ConnectionGroup['name'], configured: boolean): ConnectionGroup {
  return { name, configured, error: '', fields: [] };
}

describe('upcomingPublished', () => {
  it('keeps published upcoming workshops only, soonest first, undated last', () => {
    const rows = [
      ws(1, { starts_at: '2026-12-01T00:00:00Z' }),
      ws(2, { status: 'draft' }),
      ws(3, { starts_at: '2026-10-01T00:00:00Z' }),
      ws(4, { starts_at: undefined }),
      ws(5, { starts_at: '2026-11-01T00:00:00Z' }),
      ws(6, { starts_at: undefined, status: 'canceled' }),
    ];
    expect(upcomingPublished(rows, NOW).map((w) => w.id)).toEqual([5, 1, 4]);
  });
  it('counts a workshop starting exactly now as upcoming, as the public list does', () => {
    expect(upcomingPublished([ws(1, { starts_at: NOW.toISOString() })], NOW).map((w) => w.id)).toEqual([1]);
  });
});

describe('chip and button labels', () => {
  it('reads Not started with no summary, and Promote', () => {
    expect(outreachChip(undefined)).toEqual({ label: 'Not started', tone: 'muted' });
    expect(outreachRowButtonLabel(undefined)).toBe('Promote');
  });
  it('reports posted and scheduled counts, and Open', () => {
    const s = sum(1, { posted: 2, scheduled: 4, draft: 1 });
    expect(outreachChip(s).label).toBe('2 posted · 4 scheduled');
    expect(outreachRowButtonLabel(s)).toBe('Open');
  });
  it('falls back to drafted, then set up', () => {
    expect(outreachChip(sum(1, { draft: 3 })).label).toBe('3 drafted');
    expect(outreachChip(sum(1, {})).label).toBe('Set up · no posts');
  });
  it('labels the editor link for both states', () => {
    expect(editorOutreachLinkLabel(false, 0)).toBe('Promote this workshop');
    expect(editorOutreachLinkLabel(true, 2)).toBe('Open outreach · 2 posted');
  });
});

describe('needs attention', () => {
  it('counts upcoming workshops with nothing posted or scheduled', () => {
    const rows = [ws(1), ws(2), ws(3)];
    const sums = [sum(1, { posted: 1 }), sum(2, { draft: 2 })];
    expect(unannouncedUpcoming(rows, sums, NOW).map((w) => w.id)).toEqual([2, 3]);
  });
  it('names disconnected services and the unannounced count', () => {
    const items = outreachAttentionItems(
      [group('shortlinks', true), group('bluesky', false), group('mastodon', false)],
      [ws(1)], [], NOW,
    );
    expect(items.map((i) => i.message)).toEqual([
      'Not connected: Bluesky, Mastodon. Connect in Settings → Outreach.',
      '1 upcoming workshop not yet announced.',
    ]);
  });
  it('reports nothing when all is connected and announced', () => {
    expect(outreachAttentionItems([group('bluesky', true)], [ws(1)], [sum(1, { posted: 1 })], NOW)).toEqual([]);
  });
});
