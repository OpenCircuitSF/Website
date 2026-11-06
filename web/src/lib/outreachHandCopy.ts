// What a hand-posted row copies (#0626): the post with its ShortLink, or the
// post with no links at all, reading cleanly. It also holds what Save message
// does to hand-posted rows (#0634). Pure, so it is unit-testable.
import { LINK_TOKEN, countCharacters, isOverLimit } from './outreach';
import { sendsItself } from './outreachCards';
import { composeBody, isHashtagParagraph, planSaveMessage } from './outreachMessage';
import { formatPacificWeekday, toPacificInput } from './pacificTime';
import type { OutreachChannel, OutreachPost, WorkshopOutreach } from './types';

/** Instagram's call to action. Pinned by internal/outreach/outreach_test.go. */
const BIO_CTA = 'Sign up at the link in our bio.';
const MARK = String.fromCharCode(0);
const BRACKETED = new RegExp(`\\s*\\([^()]*${MARK}[^()]*\\)`, 'g');
const ANY_URL = /(?:https?:\/\/|www\.)[^\s<>"]+/gi;
const URL_TAIL = /[.,;:!?)\]}'"…]+$/;
const NOT_A_SENTENCE_END = /(?:^|[\s("'])(?:mr|mrs|ms|dr|st|vs|jr|sr|approx)$|[^\s]\.[^\s]*$/i;
const DANGLING = /(?::|→|->|⇒|👇|⬇️?|-|–|—)$|^(?:sign[ -]?up|register|rsvp|tickets?|details|more info|learn more|links?)(?: (?:here|below|now))?[.!]?$/iu;
const LEAD_IN = /\b(?:sign[ -]?up|rsvp)(?: (?:now|today))? (?:below|here)[.!:]?$/i;
const pointsAtLink = (s: string): boolean => DANGLING.test(s) || LEAD_IN.test(s);

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
    else if (kept.length > 0 && pointsAtLink(kept[kept.length - 1])) kept.pop();
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
    if (prior.length > 0 && pointsAtLink(prior[prior.length - 1])) {
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

const SITE_LINE = /^Open Circuit SF: https?:\/\/\S+$/;

/**
 * A hand-posted body's message and ending (#0634). The ending is the trailing
 * run of paragraphs that carry the link or its token, are Instagram's bio line
 * or the site line, plus one final hashtag line. Save message keeps the ending
 * and replaces the message.
 */
export function splitHandBody(body: string, shortUrl: string | null): { message: string; tail: string[] } {
  const paras = body.split('\n\n');
  const tail: string[] = [];
  while (paras.length > 1) {
    const last = paras[paras.length - 1];
    const t = last.trim();
    const tags = isHashtagParagraph(last);
    const cta = last.includes(LINK_TOKEN) || (shortUrl !== null && shortUrl !== '' && last.includes(shortUrl)) || t === BIO_CTA || SITE_LINE.test(t);
    if (!tags && !cta) break;
    if (tags && tail.length > 0) break;
    tail.unshift(paras.pop() as string);
  }
  return { message: paras.join('\n\n'), tail };
}

type MessageView = Pick<WorkshopOutreach, 'channels' | 'waves' | 'posts' | 'off_channels'>;
export interface MessageSaveItem {
  post: OutreachPost;
  channel: OutreachChannel;
  label: string;
  body: string;
  over: boolean;
}
export interface MessageSaveLeft {
  post: OutreachPost;
  label: string;
}
export interface MessageSave {
  /** Written by Save message: the automatic targets, then the video rows, in channel order. */
  write: MessageSaveItem[];
  /** Left alone because customized. */
  customized: MessageSaveLeft[];
  /** Left alone because an editor on it holds unsaved typing (#0632). */
  editing: MessageSaveLeft[];
}

/** What Save message does in a section (#0634): every post it writes, and every one it leaves alone and why. */
export function planMessageSave(view: MessageView, wave: string, message: string, editing: readonly number[] = []): MessageSave {
  const out: MessageSave = { write: [], customized: [], editing: [] };
  const inSection = (name: string) => view.posts.find((p) => p.channel === name && p.wave === wave) ?? null;
  const open = (p: OutreachPost) => p.status !== 'posted' && p.status !== 'queued';
  for (const item of planSaveMessage(view, wave, message)) {
    if (editing.includes(item.post.id)) out.editing.push({ post: item.post, label: item.channel.label });
    else out.write.push({ ...item, label: item.channel.label });
  }
  for (const channel of view.channels) {
    const post = inSection(channel.name);
    if (sendsItself(channel) && post && post.customized && open(post)) out.customized.push({ post, label: channel.label });
  }
  const off = view.off_channels ?? [];
  for (const channel of view.channels) {
    if (sendsItself(channel) || !isVideoChannel(channel) || off.some((o) => o.name === channel.name)) continue;
    const post = inSection(channel.name);
    if (!post || !open(post)) continue;
    const label = handChannelLabel(channel);
    if (post.customized) out.customized.push({ post, label });
    else if (editing.includes(post.id)) out.editing.push({ post, label });
    else {
      const body = composeBody(message, splitHandBody(post.body, post.short_url).tail);
      out.write.push({ post, channel, label, body, over: isOverLimit(channel, countCharacters(channel, body, post.prepared).count) });
    }
  }
  return out;
}

const listOf = (xs: string[]): string => (xs.length <= 1 ? (xs[0] ?? '') : `${xs.slice(0, -1).join(', ')} and ${xs[xs.length - 1]}`);
const kept = (left: MessageSaveLeft[]): string =>
  `${listOf(left.map((l) => l.label))} ${left.length > 1 ? 'are customized and kept their' : 'is customized and kept its'} own text.`;
const editingLine = (left: MessageSaveLeft[], after: boolean): string => {
  const n = left.length;
  const names = listOf(left.map((l) => l.label));
  return after
    ? `${names} ${n > 1 ? 'have' : 'has'} unsaved edits open, so ${n > 1 ? 'they were' : 'it was'} not changed.`
    : `${names} ${n > 1 ? 'have' : 'has'} unsaved edits open, so Save message leaves ${n > 1 ? 'them' : 'it'} alone.`;
};

/** Shown under Save message while the Message has unsaved typing: what saving changes beyond the previews. '' when nothing to say. */
export function messageSavePreview(save: MessageSave): string {
  const video = save.write.filter((i) => isVideoChannel(i.channel)).map((i) => i.label);
  const parts: string[] = [];
  if (video.length > 0) parts.push(`Saving also changes the video text for ${listOf(video)}.`);
  if (save.customized.length > 0) parts.push(kept(save.customized));
  if (save.editing.length > 0) parts.push(editingLine(save.editing, false));
  return parts.join(' ');
}

/** The line after Save message: what was written and what was left alone. */
export function messageSaveStatus(save: Pick<MessageSave, 'customized' | 'editing'>, saved: string[]): string {
  const parts = [`Saved to ${listOf(saved)}.`];
  if (save.customized.length > 0) parts.push(kept(save.customized));
  if (save.editing.length > 0) parts.push(editingLine(save.editing, true));
  return parts.join(' ');
}

export function handLinkLabel(channel: Pick<OutreachChannel, 'name' | 'automatic'>): string {
  if (channel.automatic) return 'Link to paste';
  return channel.name === 'instagram' ? 'Put this in your bio' : 'ShortLink';
}

/**
 * When to post a hand-posted row (#0635): the section's first automatic post
 * still to come, else, once they all went out, that day (#0640), else the
 * suggested day. A day and an instruction, so it never reads as something the
 * site will send. Said once, on the Video group line (#0640).
 */
export function handPostWhen(view: Pick<WorkshopOutreach, 'channels' | 'waves' | 'posts'>, wave: string, now: Date = new Date()): string {
  const auto = view.posts.filter((p) => p.wave === wave && view.channels.some((c) => c.name === p.channel && sendsItself(c)));
  const scheduled = auto
    .filter((p) => p.status === 'scheduled' && p.scheduled_at && !p.held)
    .map((p) => p.scheduled_at as string)
    .sort()[0];
  if (!scheduled && auto.length > 0 && auto.every((p) => p.status === 'posted')) {
    const went = auto.map((p) => p.posted_at).filter((t): t is string => t !== null).sort().pop();
    return went ? `The automatic posts went out ${formatPacificWeekday(went)}; post the video soon.` : 'The automatic posts went out; post the video soon.';
  }
  const iso = scheduled ?? view.waves.find((w) => w.name === wave)?.suggested_at ?? null;
  const day = iso ? toPacificInput(iso).slice(0, 10) : '';
  if (!iso || !day) return 'No set day: post when you are ready.';
  const label = formatPacificWeekday(iso);
  const today = toPacificInput(now.toISOString()).slice(0, 10);
  if (day < today) return `The day for this was ${label}; post it soon if it still helps.`;
  const alongside = scheduled ? ', with the automatic posts' : '';
  return day === today ? `Aim for today${alongside}.` : `Aim for ${label}${alongside}.`;
}

export function sectionVideoId(name: string): string {
  return `outreach-section-${name}-video`;
}

/** The link to a section's Video group, '' when the section shows none. Counted as the rows are: an off channel with no post has no row. */
export function videoJumpLabel(view: Pick<WorkshopOutreach, 'channels' | 'posts' | 'off_channels'>, wave: string): string {
  if (!view.posts.some((p) => p.wave === wave)) return '';
  const inSection = (name: string) => view.posts.find((p) => p.channel === name && p.wave === wave) ?? null;
  const rows = view.channels.filter((c) => isVideoChannel(c) && (inSection(c.name) !== null || !(view.off_channels ?? []).some((o) => o.name === c.name)));
  if (rows.length === 0) return '';
  const left = rows.filter((c) => inSection(c.name)?.status !== 'posted').length;
  return left === 0 ? 'Go to Video: all posted' : `Go to Video: ${left} left to post`;
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
