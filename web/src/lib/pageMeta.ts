// Head preview meta for client-side navigation (#0711).
//
// Safari's share sheet (Messages, Notes, AirDrop) reads a page's preview
// from the open tab's live DOM rather than fetching the URL. The server
// renders the right og:*/description/canonical for the first page loaded,
// but the SPA never touched them on navigation, so a guide reached by a
// click shared the previous page's card.
//
// The values come from GET /api/page-meta, which returns exactly what the
// server renders into that path's head (internal/seo/pagemeta.go, pinned by
// TestPageMeta_MatchesRenderedHead). The client keeps no copy of any meta
// text or rule, so there is nothing to drift. The first load is skipped by
// the caller (App.svelte, behind shouldAnnounceNavigation) because the
// server-rendered head is already correct. JSON-LD is not refreshed.
import { apiGet } from './api';

/** GET /api/page-meta's body. */
export interface PageMeta {
  title: string;
  description: string;
  og_title: string;
  og_description: string;
  og_image: string;
  og_url: string;
  og_type: string;
  twitter_card: string;
  canonical?: string;
}

function setMeta(doc: Document, attr: 'name' | 'property', key: string, value: string): void {
  let el = doc.head.querySelector<HTMLMetaElement>(`meta[${attr}="${key}"]`);
  if (!el) {
    el = doc.createElement('meta');
    el.setAttribute(attr, key);
    doc.head.appendChild(el);
  }
  el.setAttribute('content', value);
}

/** Writes every server meta field into doc's head; adds or removes canonical. */
export function applyPageMeta(doc: Document, m: PageMeta): void {
  setMeta(doc, 'name', 'description', m.description);
  setMeta(doc, 'property', 'og:title', m.og_title);
  setMeta(doc, 'property', 'og:description', m.og_description);
  setMeta(doc, 'property', 'og:image', m.og_image);
  setMeta(doc, 'property', 'og:url', m.og_url);
  setMeta(doc, 'property', 'og:type', m.og_type);
  setMeta(doc, 'name', 'twitter:card', m.twitter_card);
  setMeta(doc, 'name', 'twitter:title', m.og_title);
  setMeta(doc, 'name', 'twitter:description', m.og_description);

  let link = doc.head.querySelector<HTMLLinkElement>('link[rel="canonical"]');
  if (m.canonical) {
    if (!link) {
      link = doc.createElement('link');
      link.setAttribute('rel', 'canonical');
      doc.head.appendChild(link);
    }
    link.setAttribute('href', m.canonical);
  } else if (link) {
    link.remove();
  }
}

let seq = 0;

/**
 * Fetches path's meta and applies it, unless a later call has started since
 * (a slow earlier response never overwrites a newer navigation). path must
 * be the bare pathname: a token-bearing query string never leaves the page.
 * A failure leaves the head as it was.
 */
export async function updatePageMetaFor(path: string, doc: Document = document): Promise<void> {
  const mine = ++seq;
  try {
    const m = await apiGet<PageMeta>('/api/page-meta?path=' + encodeURIComponent(path));
    if (mine === seq) applyPageMeta(doc, m);
  } catch (err) {
    console.warn('page meta: refresh failed', err);
  }
}
