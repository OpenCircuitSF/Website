// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
// eslint-disable-next-line import/no-unresolved -- vite ?raw import, not a module
import appSource from '../App.svelte?raw';
import { applyPageMeta, updatePageMetaFor, type PageMeta } from './pageMeta';

const BASE = 'https://www.opencircuitsf.com';

// The ten preview tags web/index.html ships, with placeholder content.
const HEAD = `
<meta name="description" content="%%OC_DESCRIPTION%%">
<meta property="og:title" content="%%OC_OG_TITLE%%">
<meta property="og:description" content="%%OC_OG_DESCRIPTION%%">
<meta property="og:image" content="%%OC_OG_IMAGE%%">
<meta property="og:url" content="%%OC_OG_URL%%">
<meta property="og:type" content="%%OC_OG_TYPE%%">
<meta property="og:site_name" content="Open Circuit SF">
<meta name="twitter:card" content="%%OC_TWITTER_CARD%%">
<meta name="twitter:title" content="%%OC_TWITTER_TITLE%%">
<meta name="twitter:description" content="%%OC_TWITTER_DESCRIPTION%%">`;

const guide: PageMeta = {
  title: 'BJT vs MOSFET',
  description: 'Which transistor to use.',
  og_title: 'BJT vs MOSFET',
  og_description: 'Which transistor to use.',
  og_image: `${BASE}/guides/bjt-vs-mosfet/og.png?v=abc`,
  og_url: `${BASE}/guides/bjt-vs-mosfet`,
  og_type: 'article',
  twitter_card: 'summary_large_image',
  canonical: `${BASE}/guides/bjt-vs-mosfet`,
};
const guidesIndex: PageMeta = {
  ...guide,
  title: 'Guides',
  og_title: 'Guides',
  og_image: `${BASE}/og-default.png`,
  og_url: `${BASE}/guides`,
  canonical: `${BASE}/guides`,
};
const fallback: PageMeta = {
  title: 'Open Circuit SF',
  description: 'Hands-on electronics workshops.',
  og_title: 'Open Circuit SF',
  og_description: 'Hands-on electronics workshops.',
  og_image: `${BASE}/og-default.png`,
  og_url: `${BASE}/`,
  og_type: 'website',
  twitter_card: 'summary_large_image',
};
const about: PageMeta = { ...fallback, og_title: 'About Open Circuit SF', canonical: `${BASE}/about` };

const byPath: Record<string, PageMeta> = {
  '/guides': guidesIndex,
  '/guides/bjt-vs-mosfet': guide,
  '/login': fallback,
  '/about': about,
};

function meta(sel: string): string | null {
  return document.head.querySelector(sel)?.getAttribute('content') ?? null;
}
function canonical(): string | null {
  return document.head.querySelector('link[rel="canonical"]')?.getAttribute('href') ?? null;
}
function jsonResponse(m: PageMeta): Response {
  return new Response(JSON.stringify(m), { status: 200, headers: { 'Content-Type': 'application/json' } });
}

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  document.head.innerHTML = HEAD;
  fetchMock = vi.fn(async (url: string) => {
    const path = decodeURIComponent(new URL(url, BASE).searchParams.get('path') ?? '');
    return jsonResponse(byPath[path]);
  });
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('pageMeta (#0711)', () => {
  it('applyPageMeta writes every server field into the head', () => {
    applyPageMeta(document, guide);
    expect(meta('meta[name="description"]')).toBe(guide.description);
    expect(meta('meta[property="og:title"]')).toBe(guide.og_title);
    expect(meta('meta[property="og:description"]')).toBe(guide.og_description);
    expect(meta('meta[property="og:image"]')).toBe(guide.og_image);
    expect(meta('meta[property="og:url"]')).toBe(guide.og_url);
    expect(meta('meta[property="og:type"]')).toBe(guide.og_type);
    expect(meta('meta[name="twitter:card"]')).toBe(guide.twitter_card);
    expect(meta('meta[name="twitter:title"]')).toBe(guide.og_title);
    expect(meta('meta[name="twitter:description"]')).toBe(guide.og_description);
    expect(meta('meta[property="og:site_name"]')).toBe('Open Circuit SF');
  });

  it('adds a canonical link when the meta has one and removes it when it does not', () => {
    expect(canonical()).toBeNull();
    applyPageMeta(document, guide);
    expect(canonical()).toBe(guide.canonical);
    expect(document.head.querySelectorAll('link[rel="canonical"]').length).toBe(1);
    applyPageMeta(document, fallback);
    expect(canonical()).toBeNull();
  });

  it('navigating /guides -> /guides/{slug} -> /login leaves no guide meta behind', async () => {
    await updatePageMetaFor('/guides');
    await updatePageMetaFor('/guides/bjt-vs-mosfet');
    expect(meta('meta[property="og:image"]')).toBe(guide.og_image);
    expect(canonical()).toBe(guide.canonical);
    await updatePageMetaFor('/login');
    expect(meta('meta[property="og:image"]')).toBe(`${BASE}/og-default.png`);
    expect(meta('meta[property="og:url"]')).toBe(fallback.og_url);
    expect(canonical()).toBeNull();
  });

  it('a slower earlier response never overwrites a later navigation', async () => {
    let releaseFirst: (r: Response) => void = () => {};
    fetchMock.mockImplementationOnce(() => new Promise<Response>((res) => (releaseFirst = res)));
    const first = updatePageMetaFor('/guides/bjt-vs-mosfet');
    await updatePageMetaFor('/about');
    releaseFirst(jsonResponse(guide));
    await first;
    expect(meta('meta[property="og:title"]')).toBe(about.og_title);
    expect(canonical()).toBe(about.canonical);
  });

  it('sends only the pathname, URL-encoded', async () => {
    await updatePageMetaFor('/guides/bjt-vs-mosfet');
    expect(fetchMock.mock.calls[0][0]).toBe('/api/page-meta?path=%2Fguides%2Fbjt-vs-mosfet');
  });

  it('a failed fetch leaves the head unchanged', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    fetchMock.mockRejectedValueOnce(new TypeError('offline'));
    const before = document.head.innerHTML;
    await updatePageMetaFor('/guides/bjt-vs-mosfet');
    expect(document.head.innerHTML).toBe(before);
    expect(warn).toHaveBeenCalledTimes(1);
  });

  it('App.svelte refreshes page meta right after setting document.title', () => {
    const call = 'updatePageMetaFor(route.path)';
    expect(appSource.split(call).length - 1).toBe(1);
    const title = appSource.indexOf('document.title = titleForRoute(route)');
    const gate = appSource.indexOf('if (!shouldAnnounceNavigation()) return;');
    const at = appSource.indexOf(call);
    expect(gate).toBeGreaterThan(-1);
    expect(title).toBeGreaterThan(gate);
    expect(at).toBeGreaterThan(title);
  });
});
