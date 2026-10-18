import { describe, expect, it } from 'vitest';
import {
  buildPatch,
  fieldHint,
  initialDraft,
  LIVE_NOTE,
  PENDING_NOTE,
  PRECEDENCE_NOTE,
  RESTART_NOTE,
} from './serverSettings';
import type { ServerSettingGroup } from './types';

function group(source: 'database' | 'environment'): ServerSettingGroup {
  return {
    name: 'email',
    error: '',
    fields: [
      {
        key: 'email_reply_to',
        env_var: 'EMAIL_REPLY_TO',
        source,
        value: 'a@example.org',
        default: '',
        restart_required: true,
        pending_restart: false,
      },
    ],
  };
}

describe('buildPatch', () => {
  it('sends nothing for an untouched draft', () => {
    const g = group('database');
    expect(buildPatch(g, initialDraft(g))).toEqual({});
  });
  it('sends a changed value, trimmed', () => {
    const g = group('database');
    expect(buildPatch(g, { email_reply_to: '  b@example.org ' })).toEqual({ email_reply_to: 'b@example.org' });
  });
  it('sends an empty string only to clear a saved value', () => {
    expect(buildPatch(group('database'), { email_reply_to: '' })).toEqual({ email_reply_to: '' });
    expect(buildPatch(group('environment'), { email_reply_to: '' })).toEqual({});
  });
});

describe('fieldHint', () => {
  it('checks the media directory', () => {
    expect(fieldHint('media_dir', 'relative')).toBe('Must be an absolute directory path other than /.');
    expect(fieldHint('media_dir', '/')).not.toBeNull();
    expect(fieldHint('media_dir', '/var/www/media')).toBeNull();
  });
  it('checks the batch size', () => {
    for (const v of ['0', '501', '5x']) expect(fieldHint('send_batch_size', v)).toBe('Must be a whole number from 1 to 500.');
    expect(fieldHint('send_batch_size', '50')).toBeNull();
  });
  it('checks the list domain', () => {
    expect(fieldHint('email_list_domain', 'example.org')).not.toBeNull();
    expect(fieldHint('email_list_domain', 'lists.example.org')).toBeNull();
  });
  it('has no opinion on other fields or blank input', () => {
    expect(fieldHint('email_from', 'x')).toBeNull();
    expect(fieldHint('media_dir', '  ')).toBeNull();
  });
});

describe('notes', () => {
  it('keeps the exact copy', () => {
    expect(PRECEDENCE_NOTE).toBe(
      'A value saved here overrides the matching environment variable. Clear a saved value to fall back to the environment variable or the default.',
    );
    expect(RESTART_NOTE).toBe('Takes effect after the service restarts (for example, at the next deploy).');
    expect(PENDING_NOTE).toBe('Saved. The running service still uses the previous value until it restarts.');
    expect(LIVE_NOTE).toBe('Takes effect immediately.');
  });
});
