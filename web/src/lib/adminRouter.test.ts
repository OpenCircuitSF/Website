// Unit tests for the admin hash router (#0527) — pure functions, no DOM.
// The route table here is the same one adminRouter.ts's doc comment records;
// these cases cover criterion 11's list: params, query, trailing slash,
// unknown routes, and the non-'#/' hashes that are not admin routes at all.
import { describe, it, expect } from 'vitest';
import {
  parseAdminHash,
  adminSectionHash,
  adminCampaignHash,
  adminWorkshopHash,
  adminAuditCampaignHash,
  resolvePostLoginDestination,
  outreachViewFromQuery,
  adminOutreachHash,
  ADMIN_NAV_SECTIONS,
} from './adminRouter';

describe('parseAdminHash', () => {
  it('maps the empty hash, "#", and "#/" to Overview', () => {
    for (const hash of ['', '#', '#/']) {
      const r = parseAdminHash(hash);
      expect(r?.section).toBe('overview');
      expect(r?.campaign).toBeNull();
      expect(r?.workshopId).toBeNull();
    }
  });

  it('maps every plain section, including with a trailing slash', () => {
    for (const section of ADMIN_NAV_SECTIONS) {
      if (section === 'overview') continue;
      expect(parseAdminHash(`#/${section}`)?.section).toBe(section);
      expect(parseAdminHash(`#/${section}/`)?.section).toBe(section);
    }
  });

  it('returns null for hashes that are not admin routes (skip link, same-page anchors)', () => {
    expect(parseAdminHash('#main-content')).toBeNull();
    expect(parseAdminHash('#')).not.toBeNull(); // '#' normalizes to Overview
    expect(parseAdminHash('main-content')).toBeNull();
  });

  it('parses #/campaigns/:id as the editor and #/campaigns/:id/stats as stats', () => {
    expect(parseAdminHash('#/campaigns/42')).toMatchObject({
      section: 'campaigns',
      campaign: { id: 42, mode: 'editor' },
    });
    expect(parseAdminHash('#/campaigns/42/stats')).toMatchObject({
      section: 'campaigns',
      campaign: { id: 42, mode: 'stats' },
    });
  });

  it('parses #/outreach as the Outreach section, listed right after Workshops (#0544)', () => {
    expect(parseAdminHash('#/outreach')?.section).toBe('outreach');
    expect(ADMIN_NAV_SECTIONS.indexOf('outreach')).toBe(ADMIN_NAV_SECTIONS.indexOf('workshops') + 1);
  });

  it('parses #/workshops/:id as the workshop editor', () => {
    expect(parseAdminHash('#/workshops/7')).toMatchObject({
      section: 'workshops',
      workshopId: 7,
    });
    expect(parseAdminHash('#/workshops/7')?.campaign).toBeNull();
  });

  it('parses the hash query part with URLSearchParams — never window.location.search', () => {
    const r = parseAdminHash('#/audit?campaign=42');
    expect(r?.section).toBe('audit');
    expect(r?.query.get('campaign')).toBe('42');
  });

  it('sends unknown routes, malformed ids, and stray segments to not-found without crashing', () => {
    for (const hash of ['#/nope', '#/campaigns/abc', '#/campaigns/42/whatever', '#/workshops/abc', '#/campaigns/42/stats/extra']) {
      expect(parseAdminHash(hash)?.section).toBe('not-found');
    }
  });

  it('still routes a section whose query carries a trailing slash before the query', () => {
    expect(parseAdminHash('#/audit/?campaign=42')?.section).toBe('audit');
  });
});

describe('hash builders', () => {
  it('canonicalizes Overview to "#/" and other sections to "#/<section>"', () => {
    expect(adminSectionHash('overview')).toBe('#/');
    expect(adminSectionHash('settings')).toBe('#/settings');
    expect(adminSectionHash('crt')).toBe('#/crt');
    expect(adminSectionHash('outreach')).toBe('#/outreach');
  });

  it('builds campaign editor, stats, workshop, and audit-filter URLs', () => {
    expect(adminCampaignHash(42)).toBe('#/campaigns/42');
    expect(adminCampaignHash(42, 'stats')).toBe('#/campaigns/42/stats');
    expect(adminWorkshopHash(7)).toBe('#/workshops/7');
    expect(adminAuditCampaignHash(42)).toBe('#/audit?campaign=42');
  });
});

describe('resolvePostLoginDestination (#0527 criterion 6)', () => {
  it('honors a same-origin path, marking fragment-carrying ones for a full load', () => {
    expect(resolvePostLoginDestination('/admin#/campaigns/42')).toEqual({
      path: '/admin#/campaigns/42',
      fullLoad: true,
    });
    expect(resolvePostLoginDestination('/admin')).toEqual({ path: '/admin', fullLoad: false });
  });

  it('rejects absent, protocol-relative, and absolute next values in favor of /account', () => {
    for (const next of [null, '', '//evil.example.com/x', 'https://evil.example.com', 'admin']) {
      expect(resolvePostLoginDestination(next)).toEqual({ path: '/account', fullLoad: false });
    }
  });
});

describe('outreach sub-views (#0548)', () => {
  it('reads the view from the hash query, defaulting to workshops', () => {
    expect(outreachViewFromQuery(parseAdminHash('#/outreach')!.query)).toBe('workshops');
    expect(outreachViewFromQuery(parseAdminHash('#/outreach?view=locations')!.query)).toBe('locations');
    expect(outreachViewFromQuery(parseAdminHash('#/outreach?view=nope')!.query)).toBe('workshops');
    expect(parseAdminHash('#/outreach?view=locations')?.section).toBe('outreach');
  });
  it('builds the hashes', () => {
    expect(adminOutreachHash()).toBe('#/outreach');
    expect(adminOutreachHash('workshops')).toBe('#/outreach');
    expect(adminOutreachHash('locations')).toBe('#/outreach?view=locations');
  });
});
