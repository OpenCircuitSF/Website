import { describe, it, expect } from 'vitest';
import {
  workshopStatusLabel,
  workshopStatusBadgeClass,
  canPublish,
  canUnpublish,
  canCancel,
  publishConfirmMessage,
  unpublishConfirmMessage,
  cancelConfirmMessage,
  cancelVisibilityOutcome,
  deleteConfirmMessage,
  announceTargetingDescription,
  announceTargetingClass,
  interestIdsDiverge,
  announceUnsavedInterestsHint,
  isSlugEditable,
  toDatetimeLocalValue,
  fromDatetimeLocalValue,
  toggleInterestId,
  toggleSortDirection,
  sortWorkshopsByDate,
  validateWorkshopForm,
  isDeleteConflict,
  workshopToFormFields,
  blankWorkshopFormFields,
  isPreviewStale,
  type WorkshopFormFields,
} from './workshopAdmin';
import type { AdminWorkshop } from './types';

function workshop(fields: Partial<AdminWorkshop> = {}): AdminWorkshop {
  return {
    id: 1,
    slug: 'soldering-101',
    title: 'Soldering fundamentals',
    status: 'draft',
    interest_ids: [],
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...fields,
  };
}

function blankFields(overrides: Partial<WorkshopFormFields> = {}): WorkshopFormFields {
  return { ...blankWorkshopFormFields(), ...overrides };
}

describe('workshopStatusLabel', () => {
  it('labels the three known statuses', () => {
    expect(workshopStatusLabel('draft')).toBe('Draft');
    expect(workshopStatusLabel('published')).toBe('Published');
    expect(workshopStatusLabel('canceled')).toBe('Canceled');
  });

  it('returns an unknown value as-is', () => {
    expect(workshopStatusLabel('weird')).toBe('weird');
  });
});

describe('workshopStatusBadgeClass', () => {
  it('is success for published, danger for canceled, muted otherwise', () => {
    expect(workshopStatusBadgeClass('published')).toBe('badge-success');
    expect(workshopStatusBadgeClass('canceled')).toBe('badge-danger');
    expect(workshopStatusBadgeClass('draft')).toBe('badge-muted');
  });
});

describe('status transition offers', () => {
  it('canPublish is true unless already published', () => {
    expect(canPublish('draft')).toBe(true);
    expect(canPublish('canceled')).toBe(true);
    expect(canPublish('published')).toBe(false);
  });

  it('canUnpublish is true only when published', () => {
    expect(canUnpublish('published')).toBe(true);
    expect(canUnpublish('draft')).toBe(false);
    expect(canUnpublish('canceled')).toBe(false);
  });

  it('canCancel is true unless already canceled -- offered even for the #0177 draft-with-published_at cell, see canCancel\'s doc comment for why', () => {
    expect(canCancel('draft')).toBe(true);
    expect(canCancel('published')).toBe(true);
    expect(canCancel('canceled')).toBe(false);
  });

  it('confirm messages mention the title', () => {
    expect(publishConfirmMessage('Soldering 101')).toContain('Soldering 101');
    expect(unpublishConfirmMessage('Soldering 101')).toContain('Soldering 101');
    expect(cancelConfirmMessage('Soldering 101', 'published', '2026-01-01T00:00:00Z')).toContain(
      'Soldering 101',
    );
    expect(cancelConfirmMessage('Soldering 101', 'draft', null)).toContain('Soldering 101');
    expect(deleteConfirmMessage('Soldering 101')).toContain('Soldering 101');
  });

  // #0177: cancelVisibilityOutcome derives the true consequence from BOTH
  // status and published_at together (#0171's matrix), not from a single
  // `wasPublished = !!published_at` boolean -- that collapse was the
  // defect (a draft with a leftover published_at read as "was published"
  // and was told canceling wouldn't change its visibility, when it
  // actually publishes it). Each state below produces a DIFFERENT output,
  // so swapping any two branches' return values fails at least one of
  // these three assertions -- that is the mutation-provability the issue
  // asks for at the derivation layer, before copy is even involved.
  describe('cancelVisibilityOutcome (#0177: the matrix, not one boolean)', () => {
    it('never-published draft, or published -> unpublished (published_at NULL either way): stays private', () => {
      expect(cancelVisibilityOutcome('draft', null)).toBe('staysPrivate');
      expect(cancelVisibilityOutcome('draft', undefined)).toBe('staysPrivate');
    });

    it('published (published_at always set, #0171): stays visible', () => {
      expect(cancelVisibilityOutcome('published', '2026-01-01T00:00:00Z')).toBe('staysVisible');
    });

    it('draft with a leftover published_at -- #0171\'s canceled -> draft cell: becomes visible', () => {
      expect(cancelVisibilityOutcome('draft', '2026-01-01T00:00:00Z')).toBe('becomesVisible');
    });
  });

  // #0180: pinned whole-sentence with toBe, not a set of toMatch/not.toMatch
  // fragments. #0177's review found a mutation that escapes fragment-style
  // assertions entirely: appending a false history clause to either shared
  // branch -- "...and has not been published before" onto staysPrivate, or
  // "...it has no publication history at all" onto becomesVisible --
  // without using the one literal phrase (`never published`) the old
  // negative check watched for. Every existing toMatch/not.toMatch survived
  // that mutation (the true claim was still present, the one banned phrase
  // was still absent), so a false clause tacked onto the end went
  // unnoticed. A toBe on the complete sentence has no such gap: ANY
  // difference from the exact string -- reworded, appended, reordered --
  // fails the assertion. This dialog earned that cost the hard way, across
  // three passes (#0171's boolean, #0174's phrasing, #0177's matrix), each
  // of which shipped copy that was wrong for a state nobody had enumerated
  // yet. See this module's neighboring copy functions
  // (announceTargetingDescription, announceUnsavedInterestsHint) and
  // lib/campaigns.ts's demotionExplanation/cancelCopy for the deliberate
  // choice NOT to extend whole-sentence pinning there -- recorded in
  // issues/0180.md, not repeated here.
  it('cancel copy for a currently-published workshop: exact staysVisible sentence', () => {
    expect(cancelConfirmMessage('X', 'published', '2026-01-01T00:00:00Z')).toBe(
      'Cancel "X"? It stays visible on the public site, marked canceled, rather than disappearing.',
    );
  });

  // #0171: canceling a workshop that is not currently visible (a
  // never-published draft, or one unpublished before this cancel --
  // published_at NULL either way) does NOT make it public. The old
  // unconditional "stays visible" copy was wrong for this case -- the
  // exact defect issues/0171.md is about. #0174 additionally requires the
  // copy not claim "It was never published" -- false on the published ->
  // unpublished -> canceled path (it WAS published; unpublishing cleared
  // published_at). "not currently published" is true of both sub-cases.
  it('cancel copy for a currently-invisible, never-published-or-unpublished workshop: exact staysPrivate sentence', () => {
    expect(cancelConfirmMessage('X', 'draft', null)).toBe(
      'Cancel "X"? It is not currently published, so it stays private — canceling it will not make it visible.',
    );
  });

  // #0177's actual defect: a draft whose published_at survives from an
  // earlier publish (#0171's canceled -> draft cell). The dialog must warn
  // that canceling PUBLISHES it, not promise continuity with its current
  // (invisible) state.
  it('cancel copy for a draft with a leftover published_at: exact becomesVisible sentence', () => {
    expect(cancelConfirmMessage('X', 'draft', '2026-01-01T00:00:00Z')).toBe(
      'Cancel "X"? It is not currently visible, but canceling it will make it visible on the public site, marked canceled — it was published before, and that history was never cleared.',
    );
  });
});

describe('announceTargetingDescription', () => {
  it('says the draft targets everyone when the workshop has no interests', () => {
    expect(announceTargetingDescription([])).toMatch(/target everyone/i);
  });

  it('says the draft targets this workshop\'s interests when it has some', () => {
    const description = announceTargetingDescription([7]);
    expect(description).toMatch(/this workshop's interests/i);
    expect(description).not.toMatch(/everyone/i);
  });

  it('#0170: names the subject instead of opening with a pronoun -- this sentence opens its own paragraph, so "It" has no local antecedent', () => {
    const description = announceTargetingDescription([7]);
    expect(description).toMatch(/^The draft will be targeted/);
    expect(description.startsWith('It ')).toBe(false);
  });
});

describe('announceTargetingClass (#0162)', () => {
  it('is text-warn when the workshop has no interests (targets everyone)', () => {
    expect(announceTargetingClass([])).toBe('text-warn');
  });

  it('is text-muted when the workshop has interests set', () => {
    expect(announceTargetingClass([7])).toBe('text-muted');
    expect(announceTargetingClass([1, 2, 3])).toBe('text-muted');
  });
});

describe('interestIdsDiverge (#0151)', () => {
  it('is false for identical arrays in the same order', () => {
    expect(interestIdsDiverge([1, 2, 3], [1, 2, 3])).toBe(false);
  });

  it('is false for the same ids in a DIFFERENT order -- order-insensitive', () => {
    expect(interestIdsDiverge([3, 1, 2], [1, 2, 3])).toBe(false);
  });

  it('is false when both are empty', () => {
    expect(interestIdsDiverge([], [])).toBe(false);
  });

  it('treats undefined the same as an empty array on either side', () => {
    expect(interestIdsDiverge(undefined, [])).toBe(false);
    expect(interestIdsDiverge([], undefined)).toBe(false);
    expect(interestIdsDiverge(undefined, undefined)).toBe(false);
  });

  it('is true when an id was added', () => {
    expect(interestIdsDiverge([1, 2, 3], [1, 2])).toBe(true);
  });

  it('is true when an id was removed', () => {
    expect(interestIdsDiverge([1], [1, 2])).toBe(true);
  });

  it('is true when one side is empty/undefined and the other is not', () => {
    expect(interestIdsDiverge([1], [])).toBe(true);
    expect(interestIdsDiverge([1], undefined)).toBe(true);
  });

  it('is true for a same-length swap (same count, different ids)', () => {
    expect(interestIdsDiverge([1, 2], [1, 3])).toBe(true);
  });
});

describe('announceUnsavedInterestsHint (#0151)', () => {
  it('is empty when the form buffer matches the saved ids, same order', () => {
    expect(announceUnsavedInterestsHint([1, 2], [1, 2])).toBe('');
  });

  it('is empty when the form buffer matches the saved ids, different order', () => {
    expect(announceUnsavedInterestsHint([2, 1], [1, 2])).toBe('');
  });

  it('is empty when both are empty (no interests, nothing unsaved)', () => {
    expect(announceUnsavedInterestsHint([], [])).toBe('');
  });

  it('warns to save first when the buffer has an unsaved addition', () => {
    const hint = announceUnsavedInterestsHint([1, 2, 9], [1, 2]);
    expect(hint).toMatch(/save first/i);
  });

  it('warns to save first when the buffer has an unsaved removal', () => {
    const hint = announceUnsavedInterestsHint([1], [1, 2]);
    expect(hint).toMatch(/save first/i);
  });

  it('does not lead with a space -- #0162 renders it as its own paragraph, not appended text', () => {
    const hint = announceUnsavedInterestsHint([1, 2, 9], [1, 2]);
    expect(hint.startsWith(' ')).toBe(false);
  });
});

describe('isPreviewStale (#0167)', () => {
  it('is false when the buffer matches the saved body exactly', () => {
    expect(isPreviewStale('Learn to solder.', 'Learn to solder.')).toBe(false);
  });

  it('is true when the buffer has been edited since the last save', () => {
    expect(isPreviewStale('Learn to solder well.', 'Learn to solder.')).toBe(true);
  });

  it('treats an undefined saved body the same as an empty string -- a brand-new workshop', () => {
    expect(isPreviewStale('', undefined)).toBe(false);
  });

  it('is true for a brand-new workshop once the admin starts typing an unsaved body', () => {
    expect(isPreviewStale('Learn to solder.', undefined)).toBe(true);
  });

  it('is false for a brand-new workshop with an empty buffer and no saved body', () => {
    expect(isPreviewStale('', '')).toBe(false);
  });

  it('#0170: is false for a whitespace-only edit -- validateWorkshopForm saves bodyMd.trim(), so this would be a byte-identical save', () => {
    expect(isPreviewStale('Learn to solder.  ', 'Learn to solder.')).toBe(false);
    expect(isPreviewStale('  Learn to solder.', 'Learn to solder.')).toBe(false);
    expect(isPreviewStale('\n\nLearn to solder.\n', 'Learn to solder.')).toBe(false);
  });

  it('#0170: still true when whitespace differs AND the content itself differs', () => {
    expect(isPreviewStale('  Learn to solder well.', 'Learn to solder.')).toBe(true);
  });
});

describe('isSlugEditable', () => {
  it('is always false -- the API accepts no slug field on create or patch', () => {
    expect(isSlugEditable()).toBe(false);
  });
});

describe('datetime-local <-> RFC 3339', () => {
  it('empty/absent value round-trips to empty string', () => {
    expect(toDatetimeLocalValue(undefined)).toBe('');
    expect(toDatetimeLocalValue(null)).toBe('');
    expect(toDatetimeLocalValue('')).toBe('');
  });

  it('unparseable ISO input yields empty string', () => {
    expect(toDatetimeLocalValue('not-a-date')).toBe('');
  });

  it('empty datetime-local input yields undefined', () => {
    expect(fromDatetimeLocalValue('')).toBeUndefined();
    expect(fromDatetimeLocalValue('   ')).toBeUndefined();
  });

  it('unparseable datetime-local input yields undefined', () => {
    expect(fromDatetimeLocalValue('not-a-date')).toBeUndefined();
  });

  it('round-trips a local value through toDatetimeLocalValue(fromDatetimeLocalValue(x)) unchanged', () => {
    const local = '2026-06-15T09:30';
    const iso = fromDatetimeLocalValue(local);
    expect(iso).toBeDefined();
    expect(toDatetimeLocalValue(iso)).toBe(local);
  });

  it('produced ISO string parses back to the same wall-clock local value', () => {
    const local = '2026-12-31T23:45';
    const iso = fromDatetimeLocalValue(local) as string;
    // ISO string must be a valid, parseable timestamp.
    expect(Number.isNaN(new Date(iso).getTime())).toBe(false);
  });
});

describe('toggleInterestId', () => {
  it('adds an id not present', () => {
    expect(toggleInterestId([1, 2], 3)).toEqual([1, 2, 3]);
  });

  it('removes an id already present', () => {
    expect(toggleInterestId([1, 2, 3], 2)).toEqual([1, 3]);
  });

  it('does not mutate the input array', () => {
    const input = [1, 2];
    toggleInterestId(input, 3);
    expect(input).toEqual([1, 2]);
  });
});

describe('toggleSortDirection', () => {
  it('flips asc <-> desc', () => {
    expect(toggleSortDirection('asc')).toBe('desc');
    expect(toggleSortDirection('desc')).toBe('asc');
  });
});

describe('sortWorkshopsByDate', () => {
  it('sorts ascending by starts_at', () => {
    const a = workshop({ id: 1, starts_at: '2026-03-01T00:00:00Z' });
    const b = workshop({ id: 2, starts_at: '2026-01-01T00:00:00Z' });
    const c = workshop({ id: 3, starts_at: '2026-02-01T00:00:00Z' });
    const sorted = sortWorkshopsByDate([a, b, c], 'asc');
    expect(sorted.map((w) => w.id)).toEqual([2, 3, 1]);
  });

  it('sorts descending by starts_at', () => {
    const a = workshop({ id: 1, starts_at: '2026-03-01T00:00:00Z' });
    const b = workshop({ id: 2, starts_at: '2026-01-01T00:00:00Z' });
    const sorted = sortWorkshopsByDate([a, b], 'desc');
    expect(sorted.map((w) => w.id)).toEqual([1, 2]);
  });

  it('puts undated workshops after every dated one, in either direction', () => {
    const dated = workshop({ id: 1, starts_at: '2026-01-01T00:00:00Z' });
    const undated = workshop({ id: 2, starts_at: undefined });
    expect(sortWorkshopsByDate([undated, dated], 'asc').map((w) => w.id)).toEqual([1, 2]);
    expect(sortWorkshopsByDate([undated, dated], 'desc').map((w) => w.id)).toEqual([1, 2]);
  });

  it('does not mutate the input array', () => {
    const a = workshop({ id: 1, starts_at: '2026-03-01T00:00:00Z' });
    const b = workshop({ id: 2, starts_at: '2026-01-01T00:00:00Z' });
    const input = [a, b];
    sortWorkshopsByDate(input, 'asc');
    expect(input.map((w) => w.id)).toEqual([1, 2]);
  });
});

describe('validateWorkshopForm', () => {
  it('requires a title', () => {
    const result = validateWorkshopForm(blankFields({ title: '   ' }));
    expect(result).toEqual({ error: 'Title is required.' });
  });

  it('accepts a minimal valid form (title only)', () => {
    const result = validateWorkshopForm(blankFields({ title: 'Soldering 101' }));
    expect(result).toEqual({
      title: 'Soldering 101',
      // #0139: blank optional string fields are '', never undefined -- see
      // ValidatedWorkshopFields's doc comment for why `undefined` (an
      // omitted JSON key) can never mean "clear" server-side.
      summary: '',
      body_md: '',
      starts_at: '',
      ends_at: '',
      location_name: '',
      location_address: '',
      location_note: '',
      // #0146: null, never undefined -- see ValidatedWorkshopFields's
      // capacity comment for why capacity's clear sentinel differs from
      // the other optional fields' ''.
      capacity: null,
      signup_url: '',
      cover_image: '',
      interest_ids: [],
    });
  });

  it('#0139: represents a blanked optional field as an explicit empty string, not an omitted key', () => {
    // The regression this issue fixed: JSON.stringify drops an
    // undefined-valued key, which the server reads as "field not supplied,
    // leave alone" rather than "field cleared." Every optional string field
    // here must survive a round trip through JSON.stringify/JSON.parse as
    // an explicit '' so the server's own empty-string-means-clear path runs.
    const result = validateWorkshopForm(blankFields({ title: 'X' }));
    if ('error' in result) throw new Error('unexpected validation error');
    const roundTripped = JSON.parse(JSON.stringify(result));
    for (const key of [
      'summary',
      'body_md',
      'starts_at',
      'ends_at',
      'location_name',
      'location_address',
      'location_note',
      'signup_url',
      'cover_image',
    ]) {
      expect(roundTripped).toHaveProperty(key, '');
    }
  });

  it('#0146: represents a blanked capacity as an explicit null, not an omitted key', () => {
    // capacity's clear sentinel is `null`, not #0139's `''` -- the server
    // decodes it as handlers.Optional[int] (internal/handlers/optional.go),
    // which reads an explicit JSON null as "clear it" and an absent key as
    // "leave it alone". JSON.stringify keeps an explicit-null key (only
    // undefined-valued keys get dropped), so the round trip must still see
    // the key with a null value, never drop it.
    const result = validateWorkshopForm(blankFields({ title: 'X' }));
    if ('error' in result) throw new Error('unexpected validation error');
    expect(result.capacity).toBeNull();
    const roundTripped = JSON.parse(JSON.stringify(result));
    expect(roundTripped).toHaveProperty('capacity', null);
  });

  it('rejects an end before start', () => {
    const result = validateWorkshopForm(
      blankFields({
        title: 'X',
        startsAtLocal: '2026-06-15T18:00',
        endsAtLocal: '2026-06-15T09:00',
      }),
    );
    expect(result).toEqual({ error: 'End must be on or after start.' });
  });

  it('accepts an end equal to or after start', () => {
    const result = validateWorkshopForm(
      blankFields({
        title: 'X',
        startsAtLocal: '2026-06-15T09:00',
        endsAtLocal: '2026-06-15T18:00',
      }),
    );
    expect('error' in result).toBe(false);
  });

  it('rejects a non-numeric or non-positive capacity', () => {
    expect(validateWorkshopForm(blankFields({ title: 'X', capacity: 'abc' }))).toEqual({
      error: 'Capacity must be a whole number between 1 and 2147483647.',
    });
    // #0168: 0 stays rejected, not treated as "unlimited" -- #0155's
    // decision. NULL (an empty capacity field) is the only "no limit"
    // spelling; see blankWorkshopFormFields.
    expect(validateWorkshopForm(blankFields({ title: 'X', capacity: '0' }))).toEqual({
      error: 'Capacity must be a whole number between 1 and 2147483647.',
    });
    expect(validateWorkshopForm(blankFields({ title: 'X', capacity: '-5' }))).toEqual({
      error: 'Capacity must be a whole number between 1 and 2147483647.',
    });
  });

  it('accepts a positive integer capacity', () => {
    const result = validateWorkshopForm(blankFields({ title: 'X', capacity: '20' }));
    expect('error' in result).toBe(false);
    if (!('error' in result)) {
      expect(result.capacity).toBe(20);
    }
  });

  describe('capacity upper bound (#0168)', () => {
    // Mirrors migrations/000020_create_workshops.up.sql's
    // workshops_capacity_check CHECK (capacity <= 2147483647) and
    // internal/handlers/admin_workshops.go's isValidCapacity -- INT's max
    // value in Postgres. Before this, an admin typing something above that
    // got a round trip and a server 400 instead of an immediate local
    // message (#0155 made that 400 correct behavior; #0168 makes it
    // unnecessary for this specific, common typo).
    it('accepts the boundary value 2147483647', () => {
      const result = validateWorkshopForm(blankFields({ title: 'X', capacity: '2147483647' }));
      expect('error' in result).toBe(false);
      if (!('error' in result)) {
        expect(result.capacity).toBe(2147483647);
      }
    });

    it('rejects one past the boundary, 2147483648', () => {
      expect(
        validateWorkshopForm(blankFields({ title: 'X', capacity: '2147483648' })),
      ).toEqual({
        error: 'Capacity must be a whole number between 1 and 2147483647.',
      });
    });

    it('rejects a capacity far beyond the boundary', () => {
      expect(
        validateWorkshopForm(blankFields({ title: 'X', capacity: '3000000000' })),
      ).toEqual({
        error: 'Capacity must be a whole number between 1 and 2147483647.',
      });
    });
  });

  it('rejects a signup URL without http(s)', () => {
    expect(
      validateWorkshopForm(blankFields({ title: 'X', signupUrl: 'ftp://example.com' })),
    ).toEqual({ error: 'Signup URL must start with http:// or https://.' });
  });

  it('accepts an http(s) signup URL', () => {
    const result = validateWorkshopForm(
      blankFields({ title: 'X', signupUrl: 'https://example.com/rsvp' }),
    );
    expect('error' in result).toBe(false);
  });

  it('rejects a javascript: cover image', () => {
    expect(
      validateWorkshopForm(blankFields({ title: 'X', coverImage: 'javascript:alert(1)' })),
    ).toEqual({
      error:
        'Cover image must be a site-relative path starting with "/" (e.g. "/assets/workshops/soldering.jpg") — an external URL is not accepted.',
    });
  });

  it('accepts a site-relative path cover image', () => {
    expect(
      'error' in validateWorkshopForm(blankFields({ title: 'X', coverImage: '/assets/cover.jpg' })),
    ).toBe(false);
  });

  // #0138: cover_image is now same-origin only -- an http(s) URL to ANY
  // host, including this reasonable-looking one, is rejected. See
  // workshopAdmin.ts's header note for why images and Markdown links (the
  // signup URL is checked separately, above, via isHttpUrl -- it's fine for
  // that to stay external) get different rules.
  it('rejects an absolute http(s) URL cover image, even to a plausible host', () => {
    expect(
      'error' in
        validateWorkshopForm(blankFields({ title: 'X', coverImage: 'https://example.com/cover.jpg' })),
    ).toBe(true);
  });

  // #0138: protocol-relative and backslash-normalized-protocol-relative
  // cover images resolve off-site exactly like an absolute URL would --
  // //evil.host is not "just a path" and must be rejected the same way.
  it('rejects protocol-relative and backslash-disguised cover images', () => {
    for (const value of ['//evil.host/x.jpg', '\\\\evil.host/x.jpg', '/\\evil.host/x.jpg']) {
      expect('error' in validateWorkshopForm(blankFields({ title: 'X', coverImage: value }))).toBe(
        true,
      );
    }
  });

  // #0138 bounce, finding 1: a control character (tab/LF/CR) between the
  // slashes reassembles into a protocol-relative URL once a browser's URL
  // parser strips it back out -- confirmed executable in headless Chromium
  // resolving to http://evil.host/x.jpg. Same six payloads the reviewer
  // proved were wrongly accepted.
  it('rejects a control character disguising an off-site URL', () => {
    for (const value of [
      '/\t/evil.host/x.jpg',
      '/\n/evil.host/x.jpg',
      '/\r/evil.host/x.jpg',
      '/\r\n/evil.host/x.jpg',
      '/\t\\evil.host/x.jpg',
      '/\n\\evil.host/x.jpg',
    ]) {
      expect('error' in validateWorkshopForm(blankFields({ title: 'X', coverImage: value }))).toBe(
        true,
      );
    }
  });

  it('carries interest_ids straight through', () => {
    const result = validateWorkshopForm(blankFields({ title: 'X', interestIds: [1, 3, 5] }));
    expect('error' in result).toBe(false);
    if (!('error' in result)) {
      expect(result.interest_ids).toEqual([1, 3, 5]);
    }
  });
});

describe('isDeleteConflict', () => {
  it('is true only for 409', () => {
    expect(isDeleteConflict(409)).toBe(true);
    expect(isDeleteConflict(404)).toBe(false);
    expect(isDeleteConflict(500)).toBe(false);
  });
});

describe('workshopToFormFields / blankWorkshopFormFields', () => {
  it('blank fields are all empty/defaults', () => {
    expect(blankWorkshopFormFields()).toEqual({
      title: '',
      summary: '',
      bodyMd: '',
      startsAtLocal: '',
      endsAtLocal: '',
      locationName: '',
      locationAddress: '',
      locationNote: '',
      capacity: '',
      signupUrl: '',
      coverImage: '',
      interestIds: [],
    });
  });

  it('maps a loaded workshop onto form fields, substituting empty string/array for absent optionals', () => {
    const w = workshop({ title: 'Soldering 101', capacity: 12, interest_ids: [2, 4] });
    const fields = workshopToFormFields(w);
    expect(fields.title).toBe('Soldering 101');
    expect(fields.capacity).toBe('12');
    expect(fields.interestIds).toEqual([2, 4]);
    expect(fields.summary).toBe('');
  });

  it('round-trips capacity as a string', () => {
    const w = workshop({ capacity: 0 });
    // capacity 0 is falsy but not null/undefined -- must still render as '0', not ''.
    expect(workshopToFormFields(w).capacity).toBe('0');
  });
});
