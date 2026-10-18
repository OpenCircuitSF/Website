// Pure logic for the Announcement-first outreach page (#0582): the one shared
// Message field, how it is written into every automatic post, and the status
// text of the collapsed wave rows. No DOM, so it is unit-testable.
import { LINK_TOKEN, countCharacters, isOverLimit } from './outreach';
import { sendsItself, weekdayTime } from './outreachCards';
import type { OutreachChannel, OutreachPost, WorkshopOutreach } from './types';

type View = Pick<WorkshopOutreach, 'channels' | 'waves' | 'posts'>;

const HASHTAGS = /^(#[\p{L}\p{N}_]+\s*)+$/u;

/** A paragraph made only of hashtags. */
export function isHashtagParagraph(p: string): boolean {
  return HASHTAGS.test(p.trim());
}

/**
 * Splits a post body into the part the Message field edits and the tail it
 * leaves alone: trailing paragraphs that are a hashtag line or carry the
 * tracked link (Mastodon's "sign up here" line).
 */
export function splitBody(body: string, shortUrl: string | null): { message: string; tail: string[] } {
  const paras = body.split('\n\n');
  const tail: string[] = [];
  while (paras.length > 1) {
    const last = paras[paras.length - 1];
    const carriesLink = last.includes(LINK_TOKEN) || (shortUrl !== null && shortUrl !== '' && last.includes(shortUrl));
    if (!isHashtagParagraph(last) && !carriesLink) break;
    tail.unshift(paras.pop() as string);
  }
  return { message: paras.join('\n\n'), tail };
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

/** The collapsed wave row's one-line status. */
export function waveRowStatus(view: View, wave: string): RowStatus {
  const inWave = view.posts.filter((p) => p.wave === wave);
  if (inWave.length === 0) return { text: 'Not written', action: 'write' };
  const auto = inWave.filter((p) => {
    const ch = view.channels.find((c) => c.name === p.channel);
    return ch ? sendsItself(ch) : false;
  });
  const posted = auto.filter((p) => p.status === 'posted').length;
  if (auto.length > 0 && posted === auto.length) return { text: `Posted ${posted} of ${auto.length}`, action: 'open' };
  if (auto.some((p) => p.status === 'queued')) return { text: 'Posting now…', action: 'open' };
  if (auto.some((p) => p.status === 'failed')) return { text: 'Drafted · a post failed', action: 'open' };
  const times = auto.filter((p) => p.status === 'scheduled' && p.scheduled_at).map((p) => p.scheduled_at as string).sort();
  if (times.length > 0) {
    const held = auto.some((p) => p.held);
    return { text: held ? 'Scheduled · on hold until the workshop is published' : `Posts automatically ${weekdayTime(times[0])}`, action: 'open' };
  }
  if (posted > 0) return { text: `Posted ${posted} of ${auto.length}`, action: 'open' };
  return { text: 'Drafted · not scheduled', action: 'open' };
}

/** The "Post by hand" row's status: cards for channels the site cannot post to itself. */
export function manualRowStatus(view: View): string {
  const manual = view.posts.filter((p) => {
    const ch = view.channels.find((c) => c.name === p.channel);
    return ch ? !sendsItself(ch) : false;
  });
  if (manual.length === 0) return 'Nothing drafted';
  const posted = manual.filter((p) => p.status === 'posted').length;
  return `Posted ${posted} of ${manual.length}`;
}
