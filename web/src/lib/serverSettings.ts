// Pure logic for the Server settings area of admin Settings. Kept out of the
// component so it is unit-testable without a DOM.

import type { ServerSettingField, ServerSettingGroup, ServerSettingGroupName } from './types';

export const GROUP_LABELS: Record<ServerSettingGroupName, string> = {
  media: 'Media',
  email: 'Email',
  ses: 'Amazon SES',
  sending: 'Sending',
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
};

export const PRECEDENCE_NOTE =
  'A value saved here overrides the matching environment variable. Clear a saved value to fall back to the environment variable or the default.';
export const RESTART_NOTE = 'Takes effect after the service restarts (for example, at the next deploy).';
export const PENDING_NOTE = 'Saved. The running service still uses the previous value until it restarts.';
export const LIVE_NOTE = 'Takes effect immediately.';

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
