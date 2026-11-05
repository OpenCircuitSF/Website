// What a hand-posted row copies (#0626): the post with its ShortLink, or the
// post with no links at all, reading cleanly. Pure, so it is unit-testable.
import { LINK_TOKEN } from './outreach';
import { isHashtagParagraph } from './outreachMessage';
import type { OutreachChannel, OutreachPost } from './types';

/** Instagram's call to action. Pinned by internal/outreach/outreach_test.go. */
const BIO_CTA = 'Sign up at the link in our bio.';
const MARK = String.fromCharCode(0);
const BRACKETED = new RegExp(`\\s*\\([^()]*${MARK}[^()]*\\)`, 'g');
const ANY_URL = /(?:https?:\/\/|www\.)[^\s<>"]+/gi;
const URL_TAIL = /[.,;:!?)\]}'"…]+$/;
const NOT_A_SENTENCE_END = /(?:^|[\s("'])(?:mr|mrs|ms|dr|st|vs|jr|sr|approx)$|[^\s]\.[^\s]*$/i;
const DANGLING = /(?::|→|->|⇒|👇|⬇️?|-|–|—)$|^(?:sign[ -]?up|register|rsvp|tickets?|details|more info|learn more|links?)(?: (?:here|below|now))?[.!]?$/iu;

function markLinks(line: string, shortUrl: string | null): string {
  let s = line.split(LINK_TOKEN).join(MARK);
  s = s.replace(ANY_URL, (m) => MARK + (m.match(URL_TAIL)?.[0] ?? ''));
  const bare = (shortUrl ?? '').replace(/^https?:\/\//i, '');
  return bare ? s.split(bare).join(MARK) : s;
}

function sentences(line: string): string[] {
  const out: string[] = [];
  let start = 0;
  for (let i = 0; i < line.length; i++) {
    if (!'.!?…'.includes(line[i])) continue;
    if (i + 1 < line.length && !/\s/.test(line[i + 1])) continue;
    if (line[i] === '.' && NOT_A_SENTENCE_END.test(line.slice(start, i))) continue;
    out.push(line.slice(start, i + 1).trim());
    start = i + 1;
  }
  const tail = line.slice(start).trim();
  if (tail) out.push(tail);
  return out;
}

function withoutLinks(line: string): string {
  const kept: string[] = [];
  for (const s of sentences(line.replace(BRACKETED, ''))) {
    if (!s.includes(MARK)) {
      kept.push(s);
      continue;
    }
    const after = s.slice(s.lastIndexOf(MARK) + 1).trim();
    if (isHashtagParagraph(after)) kept.push(after);
    else if (kept.length > 0 && DANGLING.test(kept[kept.length - 1])) kept.pop();
  }
  return kept.join(' ');
}

/** The post with every link taken out; '' when nothing else is left. A body with no link comes back as written. */
export function textOnly(body: string, shortUrl: string | null): string {
  const lines = body.replace(/\r\n?/g, '\n').split('\n');
  const marked = lines.map((l) => markLinks(l, shortUrl));
  if (!marked.some((l) => l.includes(MARK))) return lines.join('\n').trim();
  const out: string[] = [];
  for (const line of marked) {
    if (!line.includes(MARK)) {
      out.push(line);
      continue;
    }
    const left = withoutLinks(line);
    if (left !== '') {
      out.push(left);
      continue;
    }
    while (out.length > 0 && out[out.length - 1].trim() === '') out.pop();
    const prior = out.length > 0 ? sentences(out[out.length - 1].trim()) : [];
    if (prior.length > 0 && DANGLING.test(prior[prior.length - 1])) {
      prior.pop();
      out[out.length - 1] = prior.join(' ');
    }
    out.push('');
  }
  return out.join('\n').replace(/\n{3,}/g, '\n\n').trim();
}

/** The post with its ShortLink in it, or null when it has none: never a raw token. */
export function withLink(body: string, shortUrl: string | null): string | null {
  if (!shortUrl) return null;
  const text = body.replace(/\r\n?/g, '\n').split(LINK_TOKEN).join(shortUrl).trim();
  if (text.includes(shortUrl)) return text;
  const line = `Sign up: ${shortUrl}`;
  if (text.includes(BIO_CTA)) return text.split(BIO_CTA).join(line);
  const paras = text.split('\n\n');
  if (paras.length > 1 && isHashtagParagraph(paras[paras.length - 1])) paras.splice(paras.length - 1, 0, line);
  else paras.push(line);
  return paras.join('\n\n');
}

export function handCopies(post: Pick<OutreachPost, 'body' | 'short_url'>): { withLink: string | null; textOnly: string } {
  return { withLink: withLink(post.body, post.short_url), textOnly: textOnly(post.body, post.short_url) };
}

/** The same video goes to these three, so they sit together in a section. */
export function isVideoChannel(channel: Pick<OutreachChannel, 'name'>): boolean {
  return channel.name === 'instagram' || channel.name === 'tiktok' || channel.name === 'youtube';
}

export function handChannelLabel(channel: Pick<OutreachChannel, 'name' | 'label'>): string {
  return channel.name === 'instagram' ? 'Instagram Reels' : channel.label;
}

export function handLinkLabel(channel: Pick<OutreachChannel, 'name' | 'automatic'>): string {
  if (channel.automatic) return 'Link to paste';
  return channel.name === 'instagram' ? 'Put this in your bio' : 'ShortLink';
}

/** False when the browser has no clipboard or refuses. Call it directly from the click: Safari needs that. */
export async function writeClipboard(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}
