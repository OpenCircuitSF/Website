// #0520: unit tests for the shared static-page contract (staticPage.ts) and
// for the two content files themselves -- the ONE source both the SPA and
// the Go server renderer (internal/seo/staticpages.go) read. A malformed
// content file must fail loudly here, before it can render half a page.
import { describe, expect, it } from 'vitest';
import { externalLinkAttrs, parseStaticPage, type StaticPageContent } from './staticPage';
import aboutRaw from '../content/about.json';
import privacyRaw from '../content/privacy.json';

describe('parseStaticPage', () => {
  it('parses a valid page', () => {
    const page = parseStaticPage({
      h1: 'Page',
      intro: [{ type: 'p', text: 'Hello' }],
      sections: [{ heading: 'S', blocks: [{ type: 'list', items: ['a'] }] }],
      links: { email: 'mailto:x@y.z' },
    });
    expect(page.h1).toBe('Page');
    expect(page.intro).toHaveLength(1);
    expect(page.sections[0].blocks[0]).toEqual({ type: 'list', items: ['a'] });
  });

  it('rejects a missing h1', () => {
    expect(() => parseStaticPage({ intro: [{ type: 'p', text: 'x' }], sections: [{ heading: 's', blocks: [{ type: 'p', text: 'y' }] }], links: {} })).toThrow();
  });

  it('rejects an unknown block type', () => {
    expect(() =>
      parseStaticPage({ h1: 'H', intro: [{ type: 'quote', text: 'x' }], sections: [{ heading: 's', blocks: [{ type: 'p', text: 'y' }] }], links: {} }),
    ).toThrow(/unknown block type/);
  });

  it('rejects a list block with an empty items array', () => {
    expect(() => parseStaticPage({ h1: 'H', intro: [{ type: 'list', items: [] }], sections: [{ heading: 's', blocks: [{ type: 'p', text: 'y' }] }], links: {} })).toThrow(/list/);
  });

  it('rejects sections without heading or blocks', () => {
    expect(() => parseStaticPage({ h1: 'H', intro: [{ type: 'p', text: 'x' }], sections: [{ heading: '', blocks: [{ type: 'p', text: 'y' }] }], links: {} })).toThrow();
  });

  it('rejects a p block without text', () => {
    expect(() => parseStaticPage({ h1: 'H', intro: [{ type: 'p', text: '' }], sections: [{ heading: 's', blocks: [{ type: 'p', text: 'y' }] }], links: {} })).toThrow(/without text/);
  });
});

describe('the committed content files are valid (they are the one shared source)', () => {
  it('about.json parses', () => {
    const about: StaticPageContent = parseStaticPage(aboutRaw);
    expect(about.h1).toBe('About Open Circuit SF');
    expect(about.sections.map((s) => s.heading)).toEqual([
      'How workshops run',
      'Venues',
      'Get involved',
      'Contact',
    ]);
  });

  it('privacy.json parses', () => {
    const privacy: StaticPageContent = parseStaticPage(privacyRaw);
    expect(privacy.h1).toBe('Privacy Policy');
    expect(privacy.sections.map((s) => s.heading)).toContain('How to leave');
  });
});

describe('externalLinkAttrs', () => {
  it('gives https destinations the SPA standard target and rel', () => {
    expect(externalLinkAttrs('https://discord.gg/Fq9ug6QXV3')).toEqual({ target: '_blank', rel: 'noopener noreferrer' });
  });

  it('leaves mailto destinations alone', () => {
    expect(externalLinkAttrs('mailto:contact@opencircuitsf.com')).toEqual({});
  });
});
