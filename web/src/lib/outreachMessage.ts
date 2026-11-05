// Pure logic for the Announcement-first outreach page (#0582): the one shared
// Message field, how it is written into every automatic post, and the status
// text of the collapsed wave rows. No DOM, so it is unit-testable.
import { LINK_TOKEN, countCharacters, defaultScheduleAt, isOverLimit } from './outreach';
import { notScheduledNote, onChannels, sendsItself, weekdayTime } from './outreachCards';
import type { OutreachChannel, OutreachPost, WorkshopOutreach } from './types';

type View = Pick<WorkshopOutreach, 'channels' | 'waves' | 'posts' | 'off_channels'>;

const HASHTAGS = /^(#[\p{L}\p{N}_]+\s*)+$/u;

/** A paragraph made only of hashtags. */
export function isHashtagParagraph(p: string): boolean {
  return HASHTAGS.test(p.trim());
}

/**
 * Splits a post body into the part the Message field edits and the tail it
 * leaves alone: trailing paragraphs that are a hashtag line or carry the
 * tracked link (Mastodon's "sign up here" line).
 *
 * Known ambiguity (#0597): a body carries no marker saying whether its final
 * hashtag-only paragraph came from the template or was typed into the message
 * (Bluesky with hashtags off has no template line). The two look identical,
 * so the final hashtag paragraph is always tail. Nothing is lost: Save message
 * recomposes message + tail, so a typed final hashtag paragraph survives every
 * save and only cannot be edited from the Message field. Resolving it for
 * real needs the server to say which paragraph it appended; not worth an API
 * change for this case.
 */
export function splitBody(body: string, shortUrl: string | null): { message: string; tail: string[] } {
  const paras = body.split('\n\n');
  const tail: string[] = [];
  // Only the final hashtag line belongs to the tail (#0593): a hashtag-only
  // paragraph the editor typed at the end of the message stays in the message,
  // so it is still editable after a save.
  let tookHashtags = false;
  while (paras.length > 1) {
    const last = paras[paras.length - 1];
    const tags = isHashtagParagraph(last);
    const carriesLink = last.includes(LINK_TOKEN) || (shortUrl !== null && shortUrl !== '' && last.includes(shortUrl));
    if (!tags && !carriesLink) break;
    if (tags) {
      if (tookHashtags) break;
      tookHashtags = true;
    }
    tail.unshift(paras.pop() as string);
  }
  return { message: paras.join('\n\n'), tail };
}

/** Bluesky and Instagram drafts carry no link in the text (the card or the bio holds it); the rest do. */
function bodyCarriesLink(channel: OutreachChannel): boolean {
  return channel.name !== 'bluesky' && channel.name !== 'instagram';
}

/**
 * The tail "Use shared message" puts back (#0593). A post's own tail is kept
 * when it has one. When customizing lost the hashtag line, it is taken from a
 * non-customized sibling in the wave; when it lost the link line a channel
 * needs (Mastodon), the sibling's link line is reused with this post's own
 * short URL, or a plain "Sign up here" line is made from it.
 */
export function restoredTail(view: View, post: OutreachPost): string[] {
  const own = splitBody(post.body, post.short_url).tail;
  const channel = view.channels.find((c) => c.name === post.channel);
  const needsLink = channel ? bodyCarriesLink(channel) : false;
  const url = post.short_url || LINK_TOKEN;
  let tags = own.filter(isHashtagParagraph);
  let links = own.filter((p) => !isHashtagParagraph(p));
  const siblings = view.posts.filter((p) => p.id !== post.id && p.wave === post.wave && !p.customized);
  if (tags.length === 0) {
    for (const s of siblings) {
      const t = splitBody(s.body, s.short_url).tail.filter(isHashtagParagraph);
      if (t.length > 0) {
        tags = t;
        break;
      }
    }
  }
  if (needsLink && links.length === 0) {
    for (const s of siblings) {
      const l = splitBody(s.body, s.short_url).tail.filter((p) => !isHashtagParagraph(p));
      if (l.length > 0) {
        // The sibling's link is its own short URL, or still the draft token:
        // either way it becomes this post's link, never the sibling's.
        links = l.map((p) => (s.short_url ? p.split(s.short_url).join(url) : p).split(LINK_TOKEN).join(url));
        break;
      }
    }
    if (links.length === 0) links = [`Sign up here: ${url}`];
  }
  return [...links, ...tags];
}

/** The inverse of splitBody. */
export function composeBody(message: string, tail: string[]): string {
  return [message, ...tail].join('\n\n');
}

/** Automatic posts in a wave that a shared-message save may rewrite: not customized, not posted or queued. */
export function messageTargets(view: View, wave: string): { post: OutreachPost; channel: OutreachChannel }[] {
  const out: { post: OutreachPost; channel: OutreachChannel }[] = [];
  for (const channel of view.channels) {
    if (!sendsItself(channel)) continue;
    const post = view.posts.find((p) => p.channel === channel.name && p.wave === wave);
    if (!post || post.customized || post.status === 'posted' || post.status === 'queued') continue;
    out.push({ post, channel });
  }
  return out;
}

/** The message shown in the field: the first target's. */
export function sharedMessage(view: View, wave: string): string {
  const first = messageTargets(view, wave)[0];
  return first ? splitBody(first.post.body, first.post.short_url).message : '';
}

/** The start of what a wave says, on one line: the first written post's message, in the server's channel order. */
export function waveExcerpt(view: Pick<View, 'channels' | 'posts'>, wave: string, max = 80): string {
  for (const channel of view.channels) {
    const post = view.posts.find((p) => p.channel === channel.name && p.wave === wave);
    if (!post) continue;
    const chars = Array.from(splitBody(post.body, post.short_url).message.replace(/\s+/g, ' ').trim());
    if (chars.length === 0) continue;
    return chars.length > max ? `${chars.slice(0, max - 1).join('').trimEnd()}…` : chars.join('');
  }
  return '';
}

export interface SavePlanItem {
  post: OutreachPost;
  channel: OutreachChannel;
  body: string;
  over: boolean;
}

/** What Save message writes: the message plus each post's own tail. */
export function planSaveMessage(view: View, wave: string, message: string): SavePlanItem[] {
  return messageTargets(view, wave).map(({ post, channel }) => {
    const { tail } = splitBody(post.body, post.short_url);
    const body = composeBody(message, tail);
    return { post, channel, body, over: isOverLimit(channel, countCharacters(channel, body, post.prepared).count) };
  });
}

export interface RowStatus {
  text: string;
  /** 'write' when the wave has no posts yet; 'open' otherwise. */
  action: 'write' | 'open';
}

/** A section's one-line status, with real dates (#0622). A suggested time shows only while nothing is scheduled or posted. */
export function waveRowStatus(view: View, wave: string, now: Date = new Date()): RowStatus {
  const suggested = defaultScheduleAt(wave, view.waves, now);
  const ahead = suggested ? ` · suggested ${weekdayTime(suggested)}` : '';
  const inWave = view.posts.filter((p) => p.wave === wave);
  if (inWave.length === 0) return { text: `Not written · not scheduled${ahead}`, action: 'write' };
  const note = notScheduledNote(view, wave);
  const rest = note ? ` · ${note}` : '';
  const auto = inWave.filter((p) => {
    const ch = view.channels.find((c) => c.name === p.channel);
    return ch ? sendsItself(ch) : false;
  });
  const handChannels = view.channels.filter((c) => !sendsItself(c));
  const handPosted = inWave.filter((p) => p.status === 'posted' && handChannels.some((c) => c.name === p.channel));
  const left = onChannels(view).filter((c) => !sendsItself(c) && !handPosted.some((p) => p.channel === c.name)).length;
  const hand = left > 0 ? ` · ${left} left to post by hand` : '';
  const newestOf = (ps: OutreachPost[]) => ps.map((p) => p.posted_at).filter((t): t is string => t !== null).sort().pop();
  if (!view.channels.some(sendsItself)) {
    if (left > 0) return { text: `Drafted${hand}${ahead}`, action: 'open' };
    const t = newestOf(handPosted);
    return { text: t ? `Posted ${weekdayTime(t)}` : `Posted ${handPosted.length} of ${handChannels.length}`, action: 'open' };
  }
  const postedPosts = auto.filter((p) => p.status === 'posted');
  const posted = postedPosts.length;
  const newest = newestOf(left === 0 ? [...postedPosts, ...handPosted] : postedPosts);
  const when = newest ? weekdayTime(newest) : '';
  if (auto.length > 0 && posted === auto.length) return { text: `${when ? `Posted ${when}` : `Posted ${posted} of ${auto.length}`}${rest}${hand}`, action: 'open' };
  if (auto.some((p) => p.status === 'queued')) return { text: 'Posting now…', action: 'open' };
  if (auto.some((p) => p.status === 'failed')) return { text: `Drafted · a post failed${hand}`, action: 'open' };
  const times = auto.filter((p) => p.status === 'scheduled' && p.scheduled_at).map((p) => p.scheduled_at as string).sort();
  if (times.length > 0) {
    const first = weekdayTime(times[0]);
    if (auto.some((p) => p.held)) return { text: `Scheduled ${first} · on hold until the event is published${rest}${hand}`, action: 'open' };
    const from = times[0] !== times[times.length - 1] ? 'from ' : '';
    const count = posted > 0 ? ` · posted ${posted} of ${auto.length}` : '';
    return { text: `Posts automatically ${from}${first}${count}${rest}${hand}`, action: 'open' };
  }
  if (posted > 0) return { text: `Posted ${posted} of ${auto.length}${when ? ` · ${when}` : ''}${rest}${hand}`, action: 'open' };
  return { text: `Drafted · not scheduled${hand}${ahead}`, action: 'open' };
}

export interface WaveStrip extends RowStatus {
  ordinal: number;
  label: string;
}

/** What a section's pinned strip says: its number, its name and its status (#0622). */
export function waveStrip(view: View, wave: string, now: Date = new Date()): WaveStrip {
  const i = view.waves.findIndex((w) => w.name === wave);
  return { ...waveRowStatus(view, wave, now), ordinal: i + 1, label: i >= 0 ? view.waves[i].label : wave };
}
