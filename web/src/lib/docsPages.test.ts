// #0735: the docs page list, the docs content files and the router agree.
import { describe, expect, it } from 'vitest';
import { DOCS_PAGES, docsPathForFile } from './docsPages';
import { STATIC_ROUTES, parsePath } from './router';
import { titleForRoute } from './pageTitle';

const RAW = import.meta.glob('../content/docs/*.json', { eager: true, import: 'default' }) as Record<
  string,
  {
    h1: string;
    title: string;
    links: Record<string, string>;
    intro: Block[];
    sections: { blocks: Block[] }[];
  }
>;
type Block = { type: string; text?: string; link?: unknown };
const FILES = new Map(Object.entries(RAW).map(([k, v]) => [docsPathForFile(k), v]));
const PATHS = DOCS_PAGES.map((p) => p.path);

describe('DOCS_PAGES', () => {
  it('has one entry per content file and one file per entry', () => {
    expect([...FILES.keys()].sort()).toEqual([...PATHS].sort());
    for (const p of DOCS_PAGES) expect(FILES.get(p.path)?.h1).toBe(p.h1);
  });

  it('is linked in full from the /docs index', () => {
    const index = FILES.get('/docs');
    const linked = Object.values(index?.links ?? {}).filter((h) => h.startsWith('/docs/'));
    expect([...new Set(linked)].sort()).toEqual(PATHS.filter((p) => p !== '/docs').sort());
  });

  it('routes every docs path to the docs view, and nothing else there', () => {
    for (const p of PATHS) expect(parsePath(p).name).toBe('docs');
    const routed = Object.entries(STATIC_ROUTES)
      .filter(([, name]) => name === 'docs')
      .map(([path]) => path);
    expect(routed.sort()).toEqual([...PATHS].sort());
  });

  // #0735 review: the SPA's document.title (titleForRoute, from DOCS_PAGES'
  // h1) and the server fallback's <title> (the file's own title) are two
  // derivations of one fact; pin them together.
  it("gives each page the same title in the SPA as in the file's title field", () => {
    for (const p of PATHS) expect(titleForRoute(parsePath(p))).toBe(FILES.get(p)?.title);
  });

  // #0735 review: a paragraph's text runs straight into its inline link in
  // both renderers, so text that ends without a space glues the link label
  // onto the sentence ("by event type.See the events").
  it('leaves a space between a paragraph and its inline link', () => {
    for (const [path, page] of FILES) {
      const blocks = [...page.intro, ...page.sections.flatMap((s) => s.blocks)];
      for (const b of blocks) {
        if (b.type !== 'p' || !b.link || !b.text) continue;
        expect(b.text, `${path}: ${b.text}`).toMatch(/\s$/);
      }
    }
  });
});

describe('docsPathForFile', () => {
  it('maps index.json to /docs and other stems to /docs/<stem>', () => {
    expect(docsPathForFile('../content/docs/index.json')).toBe('/docs');
    expect(docsPathForFile('../content/docs/newsletter.json')).toBe('/docs/newsletter');
  });
});
