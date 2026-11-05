import { describe, expect, it } from 'vitest';
import {
  handLinkReportMessage,
  linkFailureReasonLabel,
  linkReportMessage,
} from './outreachLinks';

describe('linkFailureReasonLabel', () => {
  it('words every reason and defaults unknown ones to failed', () => {
    expect(linkFailureReasonLabel('rate_limited')).toBe('ShortLinks is rate limiting');
    expect(linkFailureReasonLabel('timeout')).toBe('timed out');
    expect(linkFailureReasonLabel('not_configured')).toBe('ShortLinks is not configured');
    expect(linkFailureReasonLabel('rejected')).toBe('ShortLinks rejected the token');
    expect(linkFailureReasonLabel('code_changed')).toBe('the location code changed');
    expect(linkFailureReasonLabel('failed')).toBe('failed');
    expect(linkFailureReasonLabel('mystery')).toBe('failed');
  });
});

describe('handLinkReportMessage', () => {
  it('is null when nothing failed', () => {
    expect(handLinkReportMessage(null)).toBeNull();
    expect(handLinkReportMessage({ created: 6, existing: 3, failed: [], stopped: false })).toBeNull();
  });
  it('words one failure in the singular', () => {
    expect(
      handLinkReportMessage({ created: 5, existing: 3, stopped: false, failed: [{ kind: 'post', label: 'TikTok · Reminder', reason: 'failed' }] }),
    ).toBe('1 ShortLink could not be created: TikTok · Reminder (failed). Everything else here still works, and opening this page again tries again.');
  });
  it('words several in the plural and never names a button', () => {
    const msg = handLinkReportMessage({
      created: 4, existing: 3, stopped: true,
      failed: [
        { kind: 'post', label: 'TikTok · Reminder', reason: 'rate_limited' },
        { kind: 'post', label: 'YouTube Shorts · Final', reason: 'timeout' },
      ],
    });
    expect(msg).toBe(
      '2 ShortLinks could not be created: TikTok · Reminder (ShortLinks is rate limiting), YouTube Shorts · Final (timed out). Everything else here still works, and opening this page again tries again.',
    );
    expect(msg).not.toMatch(/Create missing links/);
  });
});

describe('linkReportMessage', () => {
  it('is null when nothing failed', () => {
    expect(linkReportMessage(null)).toBeNull();
    expect(linkReportMessage(undefined)).toBeNull();
    expect(linkReportMessage({ created: 3, existing: 0, failed: [], stopped: false })).toBeNull();
  });
  it('names each failed link and how to recover', () => {
    expect(
      linkReportMessage({
        created: 13, existing: 0, stopped: false,
        failed: [
          { kind: 'post', label: 'Bluesky · Reminder', reason: 'rate_limited' },
          { kind: 'flyer', label: 'Flyer · Mission Library', reason: 'failed' },
        ],
      }),
    ).toBe(
      '2 links could not be created: Bluesky · Reminder (ShortLinks is rate limiting), Flyer · Mission Library (failed). Everything else is saved. A missing link is made when it is needed: press Create link on a post, or Print on a flyer.',
    );
    expect(linkReportMessage({ created: 0, existing: 0, stopped: false, failed: [{ kind: 'post', label: 'X', reason: 'timeout' }] })).toMatch(/^1 link could not/);
  });
  it('names only controls that exist (#0629)', () => {
    const msg = linkReportMessage({ created: 0, existing: 0, stopped: false, failed: [{ kind: 'post', label: 'X', reason: 'failed' }] });
    expect(msg).not.toMatch(/Create missing links|Create links\b/);
    expect(msg).toContain('Create link on a post');
  });
});
