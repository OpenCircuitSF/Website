// Pure logic for the Server settings area of admin Settings. Kept out of the
// component so it is unit-testable without a DOM.

import type { ServerSettingField, ServerSettingGroup, ServerSettingGroupName } from './types';

export const GROUP_LABELS: Record<ServerSettingGroupName, string> = {
  media: 'Media',
  email: 'Email',
  ses: 'Amazon SES',
  sending: 'Sending',
  outreach: 'Outreach',
};

export const FIELD_LABELS: Record<string, { label: string; placeholder: string; help: string }> = {
  media_dir: {
    label: 'Media directory',
    placeholder: '/var/www/media',
    help: 'Where uploaded images are written. The service must be able to write to it.',
  },
  email_from: {
    label: 'From address',
    placeholder: 'Open Circuit SF <contact@mailing.opencircuitsf.com>',
    help: 'Must be on a verified SES identity.',
  },
  email_reply_to: {
    label: 'Reply-To address',
    placeholder: 'contact@opencircuitsf.com',
    help: 'Where replies to list mail go.',
  },
  email_list_domain: {
    label: 'List-Unsubscribe domain',
    placeholder: 'lists.opencircuitsf.com',
    help: 'A subdomain whose mail is routed to SES. Never the bare domain.',
  },
  ses_sandbox: {
    label: 'SES sandbox',
    placeholder: '',
    help: 'Only changes the dashboard warning; it does not change how mail is sent.',
  },
  ses_inbound_bucket: {
    label: 'Inbound mail bucket',
    placeholder: 'opencircuitsf-inbound',
    help: 'The S3 bucket inbound unsubscribe mail is stored in.',
  },
  send_batch_size: {
    label: 'Send batch size',
    placeholder: '50',
    help: 'Recipients claimed per batch, 1 to 500.',
  },
  bluesky_card_url: {
    label: 'Bluesky card link',
    placeholder: '',
    help: 'Where the link card on a Bluesky post points. Short link: tracked by ShortLinks, but the card footer shows go.opencircuitsf.com. Workshop page: the card footer shows opencircuitsf.com, but clicks are not tracked.',
  },
  outreach_hashtags: {
    label: 'Outreach hashtags',
    placeholder: '#SanFrancisco #Electronics',
    help: 'Appended to new drafts on their own line. Up to 6, each # followed by up to 40 letters or digits. Use CamelCase (#SanFrancisco, not #sanfrancisco) so screen readers say the words apart.',
  },
  outreach_channels: {
    label: 'Channels',
    placeholder: '',
    help: 'Channels outreach drafts, counts and shows. A channel that is off is left out of Draft all, the summary line, results and the Post by hand list; posts it already has are kept and return if you switch it on again. TikTok and YouTube need a video, so they start off.',
  },
  outreach_post_hour: {
    label: 'Suggested posting hour',
    placeholder: '10',
    help: 'The hour of day (0 to 23, Pacific time) Reminder and Last call posts are suggested for.',
  },
};

/** The outreach channels in display order, as internal/serversettings spells them. */
export const OUTREACH_CHANNEL_CHOICES: readonly { name: string; label: string }[] = [
  { name: 'bluesky', label: 'Bluesky' },
  { name: 'mastodon', label: 'Mastodon' },
  { name: 'instagram', label: 'Instagram' },
  { name: 'tiktok', label: 'TikTok' },
  { name: 'youtube', label: 'YouTube' },
];

/** The channel names a comma list switches on, in display order; unknown entries are ignored. */
export function channelsOn(csv: string): string[] {
  const want = new Set(csv.split(',').map((s) => s.trim().toLowerCase()));
  return OUTREACH_CHANNEL_CHOICES.filter((c) => want.has(c.name)).map((c) => c.name);
}

/** The comma list after switching one channel on or off, in display order. */
export function toggleChannel(csv: string, name: string, on: boolean): string {
  const set = new Set(channelsOn(csv));
  if (on) set.add(name);
  else set.delete(name);
  return OUTREACH_CHANNEL_CHOICES.filter((c) => set.has(c.name))
    .map((c) => c.name)
    .join(',');
}

export const PRECEDENCE_NOTE =
  'A value saved here overrides the matching environment variable. Clear a saved value to fall back to the environment variable or the default.';
export const RESTART_NOTE = 'Takes effect after the service restarts (for example, at the next deploy).';
export const PENDING_NOTE = 'Saved. The running service still uses the previous value until it restarts.';
export const LIVE_NOTE = 'Takes effect immediately.';

/** How a section's saved changes take effect. */
export type ApplyKind = 'immediate' | 'deploy' | 'mixed';

/** Words for the section badge. */
export function applyBadgeText(kind: ApplyKind): string {
  return kind === 'immediate' ? 'Applies immediately' : kind === 'deploy' ? 'Applies at next deploy' : 'Some fields apply at next deploy';
}

/** One badge for a section: all live, all restart-bound, or a mix. */
export function applyKind(fields: readonly { restart_required: boolean }[]): ApplyKind {
  const n = fields.filter((f) => f.restart_required).length;
  return n === 0 ? 'immediate' : n === fields.length ? 'deploy' : 'mixed';
}

/** The starting draft for a group: saved values only, everything else blank. */
export function initialDraft(group: ServerSettingGroup): Record<string, string> {
  const draft: Record<string, string> = {};
  for (const f of group.fields) {
    draft[f.key] = f.source === 'database' ? f.value : '';
  }
  return draft;
}

/**
 * The PATCH values for a group's edited draft. A field is sent when its
 * trimmed draft differs from the starting one; blank means clear, and is only
 * sent when there is a saved value to clear.
 */
export function buildPatch(group: ServerSettingGroup, draft: Record<string, string>): Record<string, string> {
  const start = initialDraft(group);
  const patch: Record<string, string> = {};
  for (const f of group.fields) {
    const typed = String(draft[f.key] ?? '').trim();
    if (typed === (start[f.key] ?? '')) continue;
    if (typed === '' && f.source !== 'database') continue;
    patch[f.key] = typed;
  }
  return patch;
}

/** A client-side hint mirroring the server's rules. The server stays authoritative. */
export function fieldHint(key: string, value: string): string | null {
  const v = String(value ?? '').trim();
  if (v === '') return null;
  switch (key) {
    case 'media_dir':
      return !v.startsWith('/') || v.replace(/\/+$/, '') === ''
        ? 'Must be an absolute directory path other than /.'
        : null;
    case 'send_batch_size': {
      const n = Number(v);
      return /^\d+$/.test(v) && n >= 1 && n <= 500 ? null : 'Must be a whole number from 1 to 500.';
    }
    case 'outreach_post_hour': {
      const n = Number(v);
      return /^\d+$/.test(v) && n >= 0 && n <= 23 ? null : 'Must be a whole hour from 0 to 23.';
    }
    case 'outreach_channels':
      return channelsOn(v).length > 0 ? null : 'Turn on at least one channel.';
    case 'outreach_hashtags': {
      const tags = v.split(/\s+/);
      return tags.length <= 6 && tags.every((x) => /^#[\p{L}\p{N}]{1,40}$/u.test(x) && /\p{L}/u.test(x))
        ? null
        : 'Up to 6 hashtags, each # followed by up to 40 letters or digits (not digits alone).';
    }
    case 'email_list_domain':
      return v.replace(/\.$/, '').split('.').length >= 3
        ? null
        : 'Must be a mail subdomain such as lists.example.org, never a bare domain.';
    default:
      return null;
  }
}

/** Whether a field is currently saved and therefore clearable. */
export function isSaved(f: ServerSettingField): boolean {
  return f.source === 'database';
}
