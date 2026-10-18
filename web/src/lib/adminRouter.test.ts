// Unit tests for the admin hash router (#0527) — pure functions, no DOM.
// The route table here is the same one adminRouter.ts's doc comment records;
// these cases cover criterion 11's list: params, query, trailing slash,
// unknown routes, and the non-'#/' hashes that are not admin routes at all.
import { describe, it, expect } from 'vitest';
import {
  parseAdminHash,
  adminSectionHash,
  adminSettingsHash,
  settingsSectionFromQuery,
  adminCampaignHash,
  adminWorkshopHash,
  adminAuditCampaignHash,
  resolvePostLoginDestination,
  outreachViewFromQuery,
  adminOutreachHash,
  outreachWorkshopFromQuery,
  adminOutreachResultsHash,
  adminOutreachWorkshopHash,
  outreachFlyerFromQuery,
  outreachLayoutFromQuery,
  adminOutreachFlyersHash,
  adminOutreachPrintHash,
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

describe('outreach results routing (#0546)', () => {
  it('reads the results view and the workshop param', () => {
    const r = parseAdminHash('#/outreach?view=results&workshop=12')!;
    expect(r.section).toBe('outreach');
    expect(outreachViewFromQuery(r.query)).toBe('results');
    expect(outreachWorkshopFromQuery(r.query)).toBe(12);
  });
  it('accepts only a positive integer workshop', () => {
    for (const bad of ['', '0', '-3', '1.5', 'abc', '1e3', '99999999999999999999']) {
      expect(outreachWorkshopFromQuery(new URLSearchParams({ workshop: bad }))).toBeNull();
    }
    expect(outreachWorkshopFromQuery(new URLSearchParams())).toBeNull();
  });
  it('builds the results hashes', () => {
    expect(adminOutreachResultsHash()).toBe('#/outreach?view=results');
    expect(adminOutreachResultsHash(12)).toBe('#/outreach?view=results&workshop=12');
    expect(adminOutreachHash('results')).toBe('#/outreach?view=results');
  });
});

describe('outreach flyers routing (#0549)', () => {
  it('parses the flyers and print views and the flyer id', () => {
    const f = parseAdminHash('#/outreach?view=flyers&flyer=7')!;
    expect(f.section).toBe('outreach');
    expect(outreachViewFromQuery(f.query)).toBe('flyers');
    expect(outreachFlyerFromQuery(f.query)).toBe(7);
    const p = parseAdminHash('#/outreach?view=print&flyer=7&layout=half')!;
    expect(outreachViewFromQuery(p.query)).toBe('print');
    expect(outreachLayoutFromQuery(p.query)).toBe('half');
  });
  it('rejects a malformed flyer id and defaults the layout to full', () => {
    for (const bad of ['', '0', '-1', '1.5', 'abc', '99999999999999999999']) {
      expect(outreachFlyerFromQuery(new URLSearchParams({ flyer: bad }))).toBeNull();
    }
    expect(outreachFlyerFromQuery(new URLSearchParams())).toBeNull();
    expect(outreachLayoutFromQuery(new URLSearchParams())).toBe('full');
    expect(outreachLayoutFromQuery(new URLSearchParams({ layout: 'poster' }))).toBe('full');
  });
  it('builds the hashes', () => {
    expect(adminOutreachFlyersHash()).toBe('#/outreach?view=flyers');
    expect(adminOutreachFlyersHash(7)).toBe('#/outreach?view=flyers&flyer=7');
    expect(adminOutreachPrintHash(7, 'half')).toBe('#/outreach?view=print&flyer=7&layout=half');
    expect(adminOutreachHash('flyers')).toBe('#/outreach?view=flyers');
  });
});

describe('outreach workshop page (#0568)', () => {
  it('parses view=workshop only with a valid workshop param', () => {
    expect(outreachViewFromQuery(parseAdminHash('#/outreach?view=workshop&workshop=6')!.query)).toBe('workshop');
    expect(outreachWorkshopFromQuery(parseAdminHash('#/outreach?view=workshop&workshop=6')!.query)).toBe(6);
    expect(outreachViewFromQuery(parseAdminHash('#/outreach?view=workshop')!.query)).toBe('workshops');
    expect(outreachViewFromQuery(parseAdminHash('#/outreach?view=workshop&workshop=abc')!.query)).toBe('workshops');
    expect(outreachViewFromQuery(parseAdminHash('#/outreach?view=nope')!.query)).toBe('workshops');
  });
  it('builds the hash', () => {
    expect(adminOutreachWorkshopHash(6)).toBe('#/outreach?view=workshop&workshop=6');
  });
});

describe('settings sections (#0584)', () => {
  it('builds and parses the section deep link', () => {
    expect(adminSettingsHash()).toBe('#/settings');
    expect(adminSettingsHash('outreach')).toBe('#/settings?section=outreach');
    const route = parseAdminHash(adminSettingsHash('outreach'));
    expect(route?.section).toBe('settings');
    expect(settingsSectionFromQuery(route!.query)).toBe('outreach');
  });

  it('ignores an unknown section', () => {
    expect(settingsSectionFromQuery(new URLSearchParams('section=nope'))).toBeNull();
    expect(settingsSectionFromQuery(new URLSearchParams())).toBeNull();
  });
});
