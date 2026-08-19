// Unit tests for the SubscribeForm/PreferenceCenter/ConfirmSubscription pure
// logic (subscribe.ts): UTM capture/parse, request/patch shaping, the
// response-invariance guarantee (#0029's acceptance criterion — "a test that
// the success state does not vary by response"), and the slug-toggle helper.
// No DOM or network.

import { describe, it, expect } from 'vitest';
import {
  parseUtmParams,
  captureUtmParams,
  loadUtmParams,
  UTM_STORAGE_KEY,
  buildSubscribeRequest,
  onSubscribeSuccess,
  SUBSCRIBE_SUCCESS_ROUTE,
  toggleSlug,
  buildSaveInterestsPatch,
  buildUnsubscribeEverythingPatch,
  inactiveStatusMessage,
  showSubscribeAgainAffordance,
  COMPLAINED_NO_RESUBSCRIBE_MESSAGE,
  type UtmStorage,
} from './subscribe';

// A trivial in-memory Storage fake — no jsdom required, matching theme.test.ts's
// convention for testing DOM-injectable modules.
function fakeStorage(initial: Record<string, string> = {}): UtmStorage {
  const map = new Map(Object.entries(initial));
  return {
    getItem: (key) => map.get(key) ?? null,
    setItem: (key, value) => {
      map.set(key, value);
    },
  };
}

describe('parseUtmParams', () => {
  it('returns empty object for a search string with no utm params', () => {
    expect(parseUtmParams('')).toEqual({});
    expect(parseUtmParams('?foo=bar')).toEqual({});
  });

  it('extracts all three params when present', () => {
    expect(parseUtmParams('?utm_source=instagram&utm_medium=social&utm_campaign=solder-oct')).toEqual({
      utm_source: 'instagram',
      utm_medium: 'social',
      utm_campaign: 'solder-oct',
    });
  });

  it('extracts a partial set, omitting absent fields entirely', () => {
    expect(parseUtmParams('?utm_source=newsletter')).toEqual({ utm_source: 'newsletter' });
  });

  it('ignores unrelated query params', () => {
    expect(parseUtmParams('?utm_source=x&token=abc123&other=1')).toEqual({ utm_source: 'x' });
  });
});

describe('captureUtmParams / loadUtmParams', () => {
  it('stores parsed params and reads them back with empty defaults for absent fields', () => {
    const storage = fakeStorage();
    captureUtmParams('?utm_source=instagram&utm_campaign=solder-oct', storage);
    expect(loadUtmParams(storage)).toEqual({
      utm_source: 'instagram',
      utm_medium: '',
      utm_campaign: 'solder-oct',
    });
  });

  it('does not overwrite previously captured attribution with an empty pageview', () => {
    const storage = fakeStorage();
    captureUtmParams('?utm_source=instagram', storage);
    captureUtmParams('', storage); // a later pageview with no UTM params
    expect(loadUtmParams(storage)).toEqual({ utm_source: 'instagram', utm_medium: '', utm_campaign: '' });
  });

  it('loadUtmParams defaults to all-empty when nothing was ever captured', () => {
    expect(loadUtmParams(fakeStorage())).toEqual({ utm_source: '', utm_medium: '', utm_campaign: '' });
  });

  it('loadUtmParams tolerates malformed stored JSON', () => {
    const storage = fakeStorage({ [UTM_STORAGE_KEY]: 'not json{{{' });
    expect(loadUtmParams(storage)).toEqual({ utm_source: '', utm_medium: '', utm_campaign: '' });
  });
});

describe('buildSubscribeRequest', () => {
  it('shapes the wire body, trimming the email and passing everything else through', () => {
    const body = buildSubscribeRequest({
      email: '  person@example.com  ',
      interests: ['homelab', 'soldering'],
      website: '',
      renderedAt: 1_700_000_000_000,
      utm: { utm_source: 'instagram', utm_medium: 'social', utm_campaign: 'solder-oct' },
    });
    expect(body).toEqual({
      email: 'person@example.com',
      interests: ['homelab', 'soldering'],
      website: '',
      rendered_at: 1_700_000_000_000,
      utm_source: 'instagram',
      utm_medium: 'social',
      utm_campaign: 'solder-oct',
    });
  });

  it('never rejects a non-ASCII local part client-side — passes it through unchanged (trimmed)', () => {
    const body = buildSubscribeRequest({
      email: '  büro@example.com ',
      interests: [],
      website: '',
      renderedAt: 0,
      utm: { utm_source: '', utm_medium: '', utm_campaign: '' },
    });
    expect(body.email).toBe('büro@example.com');
  });
});

describe('onSubscribeSuccess — response-invariance (#0026 uniform 202)', () => {
  it('returns the fixed success route regardless of the response body content', () => {
    const route = onSubscribeSuccess({ message: 'Check your email to confirm.' });
    expect(route).toBe(SUBSCRIBE_SUCCESS_ROUTE);
  });

  it('returns the IDENTICAL route for wildly different response bodies', () => {
    const responses: unknown[] = [
      { message: 'Check your email to confirm.' },
      { message: 'A completely different message the server could theoretically send' },
      { message: 'x', extra_field_a_future_server_change_might_add: 'secret subscriber state' },
      undefined,
      null,
      'not even an object',
      42,
    ];
    const routes = responses.map((r) => onSubscribeSuccess(r));
    for (const route of routes) {
      expect(route).toBe(SUBSCRIBE_SUCCESS_ROUTE);
    }
    // Every one of them is literally the same value — the UI cannot have
    // branched on response content, because it never looked past this
    // constant.
    expect(new Set(routes).size).toBe(1);
  });
});

describe('toggleSlug', () => {
  it('adds a slug not already selected', () => {
    const next = toggleSlug(new Set(['homelab']), 'soldering');
    expect(Array.from(next).sort()).toEqual(['homelab', 'soldering']);
  });

  it('removes a slug already selected', () => {
    const next = toggleSlug(new Set(['homelab', 'soldering']), 'soldering');
    expect(Array.from(next)).toEqual(['homelab']);
  });

  it('does not mutate the input set', () => {
    const original = new Set(['homelab']);
    toggleSlug(original, 'soldering');
    expect(Array.from(original)).toEqual(['homelab']);
  });
});

describe('buildSaveInterestsPatch / buildUnsubscribeEverythingPatch', () => {
  it('builds an interests-replacement patch', () => {
    expect(buildSaveInterestsPatch('tok123', new Set(['homelab', 'robotics']))).toEqual({
      token: 'tok123',
      interests: ['homelab', 'robotics'],
    });
  });

  it('an empty selection is a first-class value, not omitted', () => {
    expect(buildSaveInterestsPatch('tok123', new Set())).toEqual({ token: 'tok123', interests: [] });
  });

  it('builds a distinct unsubscribe-everything patch carrying no interests field', () => {
    const patch = buildUnsubscribeEverythingPatch('tok123');
    expect(patch).toEqual({ token: 'tok123', unsubscribe: true });
    expect(patch.interests).toBeUndefined();
  });
});

describe('inactiveStatusMessage (#0031 review finding 1)', () => {
  it('never claims an active subscription for a non-active status', () => {
    for (const status of ['unsubscribed', 'complained', 'bounced', 'pending', 'something-unexpected']) {
      expect(inactiveStatusMessage(status)).not.toMatch(/you're subscribed/i);
    }
  });

  it('gives distinct, status-specific copy for the known non-active statuses', () => {
    const messages = ['unsubscribed', 'complained', 'bounced', 'pending'].map(inactiveStatusMessage);
    expect(new Set(messages).size).toBe(messages.length);
  });

  it('falls back to a generic not-subscribed statement for an unrecognized status', () => {
    expect(inactiveStatusMessage('some-future-status')).toBe("You're not currently subscribed.");
  });

  // #0090: `complained` used to get the same vague "isn't currently
  // subscribed" text every other non-active status got, even though the
  // page's own unsubscribe path already has honest copy for exactly this
  // case (internal/handlers/preferences.go's patchUnsubscribe no-op
  // message). Reusing that copy, rather than writing a second version of
  // it, is what keeps the two from drifting apart the next time either is
  // edited -- assert on the shared constant, not a duplicated literal.
  it('gives complained the same honest copy the unsubscribe path already returns, not the vague default', () => {
    expect(inactiveStatusMessage('complained')).toBe(COMPLAINED_NO_RESUBSCRIBE_MESSAGE);
    expect(inactiveStatusMessage('complained')).not.toBe("This address isn't currently subscribed.");
  });

  it("complained's copy names how to reach a human", () => {
    expect(inactiveStatusMessage('complained')).toMatch(/contact us/i);
  });
});

describe('showSubscribeAgainAffordance (#0090 — the dead end)', () => {
  // complained can never leave that status from this page: goToSubscribe is
  // a client-side navigate with no mutation, and the public form's #0026
  // uniform 202 masks that existingSignup's StatusComplained branch sends
  // no confirmation email. Offering the button walks someone into that dead
  // end from a page that already knows their status.
  it('is suppressed for complained', () => {
    expect(showSubscribeAgainAffordance('complained')).toBe(false);
  });

  // pending, unsubscribed and bounced can all legitimately resubscribe by
  // submitting the public form again; active never reaches this panel
  // (isActive routes to the editor instead), but the function is still
  // asked to behave safely if it ever did.
  it('is present for every other status the panel can show', () => {
    for (const status of ['pending', 'unsubscribed', 'bounced', 'active', 'some-future-status']) {
      expect(showSubscribeAgainAffordance(status)).toBe(true);
    }
  });
});
