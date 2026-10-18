// What the platform previews draw (#0583): the text split into styled runs, the
// link card each network builds, and the small checks around them. Pure, so
// the components stay thin.

import type { OutreachPost, WorkshopOutreach } from './types';
import { isSafeLinkHref } from './linkSafety';
import { APP_NAME } from './branding';

export interface Segment {
  kind: 'text' | 'hashtag' | 'link';
  text: string;
}

const TOKEN_RE = /(https?:\/\/[^\s]+|(?<![\w&])#[\p{L}\p{N}_]+)/gu;

/** Splits a post body into plain text, hashtags and links, in order. */
export function segments(body: string): Segment[] {
  const out: Segment[] = [];
  let last = 0;
  for (const m of body.matchAll(TOKEN_RE)) {
    const at = m.index ?? 0;
    if (at > last) out.push({ kind: 'text', text: body.slice(last, at) });
    out.push({ kind: m[0].startsWith('#') ? 'hashtag' : 'link', text: m[0] });
    last = at + m[0].length;
  }
  if (last < body.length) out.push({ kind: 'text', text: body.slice(last) });
  return out;
}

/** The host of a URL, or the fallback when it does not parse. */
export function hostOf(url: string | null | undefined, fallbackHost: string): string {
  if (!url) return fallbackHost;
  try {
    return new URL(url).host || fallbackHost;
  } catch {
    return fallbackHost;
  }
}

type CardView = Pick<WorkshopOutreach, 'workshop'> & { bluesky_card?: 'short' | 'canonical' };

export interface LinkCard {
  title: string;
  description: string;
  /** A same-site path to draw, or null when the card goes out without a picture. */
  image: string | null;
  host: string;
}

const isMedia = (cover: string | null | undefined): cover is string => !!cover && cover.startsWith('/media/') && isSafeLinkHref(cover);

/** The external card Bluesky attaches: the picture only for an uploaded /media/ cover. */
export function blueskyCard(view: CardView, post: Pick<OutreachPost, 'short_url'>, fallbackHost: string): LinkCard {
  const host = view.bluesky_card === 'canonical' ? fallbackHost : hostOf(post.short_url, fallbackHost);
  return {
    title: view.workshop.title,
    description: (view.workshop.summary ?? '').trim(),
    image: isMedia(view.workshop.cover_image) ? view.workshop.cover_image : null,
    host,
  };
}

/**
 * The preview Mastodon builds from the link; null when the body carries no URL.
 * It mirrors the workshop page's og tags (internal/seo workshopRouteMeta): the
 * image is the cover as given (a same-site path or an absolute URL), else the
 * generated og.png. The byline is the page's og:site_name, which Mastodon shows
 * in place of a host whenever the page names one (web/index.html always does).
 */
export function mastodonLinkCard(view: CardView, body: string): LinkCard | null {
  const first = segments(body).find((s) => s.kind === 'link');
  if (!first) return null;
  const cover = view.workshop.cover_image;
  return {
    title: view.workshop.title,
    description: (view.workshop.summary ?? '').trim(),
    image: cover && isSafeLinkHref(cover) && /^(\/|https?:)/i.test(cover) ? cover : `/workshops/${view.workshop.slug}/og.png`,
    host: APP_NAME,
  };
}

/** True when the Bluesky and Mastodon posts of a wave would go out with the same text. */
export function identicalAutomaticBodies(
  view: { posts: Pick<OutreachPost, 'channel' | 'wave' | 'body'>[] },
  wave: string,
  channels: string[] = ['bluesky', 'mastodon'],
): boolean {
  const bodies = channels.map((c) => view.posts.find((p) => p.channel === c && p.wave === wave)?.body);
  if (bodies.some((b) => b === undefined)) return false;
  return bodies[0] === bodies[1];
}

/** The post's own address when it is a safe link, else null. */
export function copyablePostLink(post: Pick<OutreachPost, 'remote_url'>): string | null {
  const u = post.remote_url;
  return u && isSafeLinkHref(u) ? u : null;
}
