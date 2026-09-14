// #0520: the shared shape of the static page copy that lives in
// web/src/content/about.json and web/src/content/privacy.json -- ONE source
// consumed by BOTH the SPA (About.svelte / PrivacyPolicy.svelte render from
// it via StaticPage.svelte) and the Go server renderer
// (internal/seo/staticpages.go embeds the same files at compile time and
// substitutes the same text into the served raw HTML). Neither side keeps a
// second, hand-typed copy of the text, so the two cannot drift.
//
// This module is the SPA-side contract: parseStaticPage validates a raw
// JSON payload into StaticPageContent, so a malformed content file fails
// loudly here (and in the unit tests below) instead of rendering a broken
// page. The Go side runs its own, equivalent decode -- two parsers over one
// file, which is exactly the point: the TEXT has one source; the two
// runtimes each interpret it.

export interface StaticPageLinkRef {
  key: string;
  label: string;
}

/** One paragraph, list, or link row. A `p` block may carry a leading
 * `<strong>` label ("Unsubscribe."), a trailing inline link (the mailto
 * contact line), and `tail` text placed after that link. */
export type StaticPageBlock =
  | {
      type: 'p';
      text: string;
      strong?: string;
      muted?: boolean;
      link?: StaticPageLinkRef;
      tail?: string;
    }
  | { type: 'list'; items: string[] }
  | { type: 'links'; items: StaticPageLinkRef[] };

export interface StaticPageSection {
  heading: string;
  blocks: StaticPageBlock[];
}

export interface StaticPageContent {
  h1: string;
  intro: StaticPageBlock[];
  sections: StaticPageSection[];
  /** Every link destination, keyed by the `key` a block references. Keeps
   * the hrefs themselves single-sourced too: About.svelte's Discord/Luma
   * URLs are re-exported by links.ts from here, never retyped. */
  links: Record<string, string>;
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

function parseLinkRef(v: unknown, where: string): StaticPageLinkRef {
  if (!isRecord(v) || typeof v.key !== 'string' || v.key === '' || typeof v.label !== 'string' || v.label === '') {
    throw new Error(`staticPage: ${where} must carry { key, label } with non-empty strings`);
  }
  return { key: v.key, label: v.label };
}

function parseBlock(v: unknown, where: string): StaticPageBlock {
  if (!isRecord(v)) throw new Error(`staticPage: ${where} must be an object`);
  switch (v.type) {
    case 'p': {
      if (typeof v.text !== 'string' || v.text === '') {
        throw new Error(`staticPage: ${where} is a "p" block without text`);
      }
      const block: StaticPageBlock = { type: 'p', text: v.text };
      if (typeof v.strong === 'string') block.strong = v.strong;
      if (v.muted === true) block.muted = true;
      if (v.link !== undefined) block.link = parseLinkRef(v.link, `${where}.link`);
      if (typeof v.tail === 'string') block.tail = v.tail;
      return block;
    }
    case 'list': {
      if (!Array.isArray(v.items) || v.items.length === 0 || v.items.some((i) => typeof i !== 'string' || i === '')) {
        throw new Error(`staticPage: ${where} is a "list" block without a non-empty string[] items`);
      }
      return { type: 'list', items: v.items as string[] };
    }
    case 'links': {
      if (!Array.isArray(v.items) || v.items.length === 0) {
        throw new Error(`staticPage: ${where} is a "links" block without items`);
      }
      return { type: 'links', items: v.items.map((l, i) => parseLinkRef(l, `${where}.items[${i}]`)) };
    }
    default:
      throw new Error(`staticPage: ${where} has unknown block type ${String(v.type)}`);
  }
}

/** Validate a raw parsed-JSON payload (about.json / privacy.json) into
 * StaticPageContent, throwing a message that names the offending element --
 * a malformed content file must fail loudly, not render half a page. */
export function parseStaticPage(raw: unknown): StaticPageContent {
  if (!isRecord(raw)) throw new Error('staticPage: content must be a JSON object');
  if (typeof raw.h1 !== 'string' || raw.h1 === '') {
    throw new Error('staticPage: h1 must be a non-empty string');
  }
  if (!isRecord(raw.links)) throw new Error('staticPage: links must be an object of href strings');
  for (const [key, href] of Object.entries(raw.links)) {
    if (typeof href !== 'string' || href === '') {
      throw new Error(`staticPage: links["${key}"] must be a non-empty href string`);
    }
  }
  if (!Array.isArray(raw.intro) || raw.intro.length === 0) {
    throw new Error('staticPage: intro must be a non-empty block array');
  }
  const intro = raw.intro.map((b, i) => parseBlock(b, `intro[${i}]`));
  if (!Array.isArray(raw.sections) || raw.sections.length === 0) {
    throw new Error('staticPage: sections must be a non-empty array');
  }
  const sections = raw.sections.map((s, i) => {
    if (!isRecord(s) || typeof s.heading !== 'string' || s.heading === '' || !Array.isArray(s.blocks) || s.blocks.length === 0) {
      throw new Error(`staticPage: sections[${i}] must carry { heading, blocks } with non-empty values`);
    }
    return {
      heading: s.heading,
      blocks: s.blocks.map((b, j) => parseBlock(b, `sections[${i}].blocks[${j}]`)),
    };
  });
  return { h1: raw.h1, intro, sections, links: raw.links as Record<string, string> };
}

/** Whether a link destination should open in a new tab with the SPA's
 * standard external rel attributes. mailto: contacts open in the mail
 * client (no target/rel), matching what About.svelte has always rendered
 * for its contact line; everything else in these pages is an external
 * https URL rendered target="_blank" rel="noopener noreferrer". */
export function externalLinkAttrs(href: string): { target?: string; rel?: string } {
  if (href.startsWith('mailto:')) return {};
  return { target: '_blank', rel: 'noopener noreferrer' };
}
