// Posting several automatic cards from one confirmation (#0582), shared by the
// Announcement section and the posts list: sends one after the other, catches
// each failure separately and returns the per-post notices and the one line
// that reports them all.
import { sendOutreachPost, ApiError } from './api';
import { combinedPostResult, type CardNotice } from './outreachCards';
import type { OutreachChannel, OutreachPost } from './types';

export interface PostAllResult {
  line: string;
  notices: Record<number, CardNotice>;
}

export async function postAll(
  items: { post: OutreachPost; channel: OutreachChannel }[],
  onChange: (post: OutreachPost) => void,
): Promise<PostAllResult> {
  const outcomes: { label: string; ok: boolean; error?: string }[] = [];
  const notices: Record<number, CardNotice> = {};
  for (const { post, channel } of items) {
    try {
      const updated = await sendOutreachPost(post.id);
      onChange(updated);
      outcomes.push({ label: channel.label, ok: true });
      notices[post.id] = { text: 'Queued. It posts within a few seconds; this page updates when it does.', kind: 'status', status: updated.status };
    } catch (err) {
      const reason = err instanceof ApiError ? err.message : 'please try again';
      outcomes.push({ label: channel.label, ok: false, error: reason });
      notices[post.id] = { text: `Could not post: ${reason}`, kind: 'error', status: post.status };
    }
  }
  return { line: combinedPostResult(outcomes), notices };
}
