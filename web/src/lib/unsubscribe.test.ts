// Unit tests for the Unsubscribe view's pure logic (unsubscribe.ts, #0036).
// No DOM or network -- see subscribe.test.ts for the established pattern
// this file follows.

import { describe, it, expect } from 'vitest';
import {
  resolveDoneMessage,
  extractToken,
  UNSUBSCRIBE_FALLBACK_MESSAGE,
  type UnsubscribeResult,
} from './unsubscribe';

describe('resolveDoneMessage', () => {
  it('returns the server message for a successful unsubscribe', () => {
    const result: UnsubscribeResult = { message: "You've been unsubscribed.", no_op: false };
    expect(resolveDoneMessage(result)).toBe("You've been unsubscribed.");
  });

  it('returns the server message for an unknown/expired/replayed token, unchanged', () => {
    // unsubscribe.go answers the SAME neutral message for a missing, unknown,
    // and replayed token -- this function must not editorialize on top of it.
    const result: UnsubscribeResult = {
      message: 'If this address was on our list, it has been unsubscribed.',
      no_op: true,
    };
    expect(resolveDoneMessage(result)).toBe(
      'If this address was on our list, it has been unsubscribed.',
    );
  });

  it('returns the server message for a complained no-op, unchanged -- no error branch', () => {
    // The one response whose no_op is true for a reason other than "unknown
    // token": a still-complained row. This function does not distinguish it
    // from any other case -- see the module doc comment.
    const result: UnsubscribeResult = {
      message:
        "No change: this address is marked as having complained about a previous email and can't be unsubscribed again from this link.",
      no_op: true,
    };
    expect(resolveDoneMessage(result)).toBe(
      "No change: this address is marked as having complained about a previous email and can't be unsubscribed again from this link.",
    );
  });

  it('falls back to the fixed generic message when the request never got a response', () => {
    expect(resolveDoneMessage(null)).toBe(UNSUBSCRIBE_FALLBACK_MESSAGE);
  });
});

describe('extractToken', () => {
  it('returns the token when present', () => {
    expect(extractToken(new URLSearchParams('?token=abc123'))).toBe('abc123');
  });

  it('returns null when no token param is present', () => {
    expect(extractToken(new URLSearchParams(''))).toBeNull();
  });

  it('returns null for a blank token value', () => {
    expect(extractToken(new URLSearchParams('?token='))).toBeNull();
  });

  it('returns null for a whitespace-only token value', () => {
    expect(extractToken(new URLSearchParams('?token=%20%20'))).toBeNull();
  });

  it('ignores unrelated query params', () => {
    expect(extractToken(new URLSearchParams('?utm_source=x&token=zzz&other=1'))).toBe('zzz');
  });
});
