// #0226: `#0060` bounced once already because the privacy policy's erasure
// list drifted from what erasure actually leaves (three named categories,
// four actually retained -- the audit log was missing). The only thing that
// kept it true after that fix was prose: a header comment above the
// `StatusList` telling the next editor to keep it in step -- exactly the
// mechanism that was already in place when the drift happened. This file
// turns that "keep it in step" instruction into a test that fails on drift
// in EITHER direction: a category added to what erasure retains without the
// policy naming it (under-promising deletion, i.e. the page implies less is
// kept than really is), or a category the policy names that erasure no
// longer actually retains (over-promising deletion -- CLAUDE.md §9's
// concern, since a false "we delete this" claim about personal data is the
// more serious failure mode of the two).
//
// What this compares against (`#0226`'s own "hard part" note): a committed
// fixture, `ERASURE_RETAINED_CATEGORIES` below -- the weakest of the three
// options the issue names, but explicitly accepted ("even that turns silent
// drift into a visible diff, which is the whole point"). Strengthened two
// ways beyond a bare fixture:
//   1. each category's `requiredPhrases` are asserted as present in the
//      matching StatusList item (not just a count), so swapping two items'
//      order or content still fails even though the total count wouldn't
//      change;
//   2. `sourceRef` names the exact line in `internal/subscribers/erase.go`
//      or `internal/handlers/admin_subscribers.go` this category's claim
//      rests on -- not read structurally by this test (that Go source isn't
//      reachable from a `.test.ts` file's own AST walk the way the Svelte
//      side is), but pinned here as the citation a human -- or `#0220`'s own
//      path-citation guard -- can check against the tree.
//
// Reads the REAL `PrivacyPolicy.svelte` (`import.meta.glob`, the same
// technique `citationGuard.test.ts` and `modalFocusWiring
// .structuralGuard.test.ts` already use) and walks its own AST (svelte
// /compiler's `parse`) to find the specific `<StatusList>` inside the
// `aria-labelledby="leave-h"` ("How to leave") section, then reads its
// `items` prop's array-literal elements directly off the parsed AST -- not
// a source-text regex, and not mounting the component (`#0094`'s harness
// would work here too, but reading the prop off the AST needs no DOM and
// stays on the fast, non-jsdom default `CLAUDE.md` §1 describes).
import { describe, it, expect } from 'vitest';
import { parse as parseSvelte } from 'svelte/compiler';

type SvelteNode = Record<string, unknown>;

interface RetainedCategory {
  key: string;
  requiredPhrases: string[];
  sourceRef: string;
}

// ERASURE_RETAINED_CATEGORIES: the four things `subscribers.Store.Erase`
// (`internal/subscribers/erase.go`) and the `subscriber.erased` audit entry
// it feeds (`internal/handlers/admin_subscribers.go`) actually leave behind
// after a hard delete, as of `#0226`. `requiredPhrases` are matched
// case-insensitively, substring, against a SINGLE StatusList item -- every
// phrase in a category's list must appear in the SAME item for that
// category to be considered present (see matchCategories below).
const ERASURE_RETAINED_CATEGORIES: RetainedCategory[] = [
  {
    key: 'suppression',
    // A bare 'suppression' was too weak: #0237 widened the audit_log item
    // below to disclose a "suppression removal" audit entry, and that
    // phrase also contains the substring "suppression" -- so with the old,
    // single-word phrase, matchItemToCategory (which checks categories in
    // THIS ARRAY'S order and returns the first fully-satisfied one) claimed
    // the audit_log item for THIS category before ever reaching audit_log's
    // own, leaving audit_log unmatched even though its item text was
    // correct. Caught by the "passes against the real, current four items"
    // test going from 4/4 matched to 3/4 the moment that wording landed.
    requiredPhrases: ['permanent suppression entry'],
    sourceRef: 'internal/subscribers/erase.go: addSuppression(... SuppressionReasonManual ...) before the DELETE',
  },
  {
    key: 'email_sends (anonymized)',
    requiredPhrases: ['anonymized'],
    sourceRef: "internal/subscribers/erase.go: UPDATE email_sends SET email = 'erased-' || id || '@erased.invalid'",
  },
  {
    key: 'email_events (untouched, purpose includes bounce/complaint handling)',
    // "bounce" alone is too weak a phrase: the item already names "bounces"
    // as one of the retained EVENT TYPES, so a substring match on "bounce"
    // stays true even after the PURPOSE clause this category actually
    // checks for is deleted (caught by this guard's own mutation proof
    // below, which is why the phrase is the full purpose clause, not the
    // word alone).
    requiredPhrases: ['deliverability events', 'bounce/complaint handling'],
    sourceRef: 'internal/subscribers/erase.go doc comment: "email_events rows, untouched"; PRD §6.9\'s delivery-health circuit breaker',
  },
  {
    key: 'audit_log (subscriber/admin/automated categories, per-request IP except automated SES entries, six actions carry the email plus one contingent on admin search text)',
    // "admin" alone is too weak: the OLD, narrower wording this replaces
    // already said "an internal ADMIN audit log entry" (describing WHO
    // reads it, not whose IP the erasure row carries) -- also caught by
    // this guard's own mutation proof below. The phrases here are unique to
    // the corrected wording.
    //
    // 'your email address' was added after #0226's FIRST review pass
    // (2026-08-24, BOUNCED): that pass widened the IP phrasing but deleted
    // the item's pre-existing email-address disclosure outright, and this
    // guard's requiredPhrases at the time had nothing that would have
    // caught it -- a green guard sitting over an under-disclosing page.
    // Mutation-proved below (see "fails when the audit-log item drops the
    // email-address disclosure").
    //
    // #0237 (#0226's own phase-3 review) widened this category again: the
    // item was still scoped to the five SUBSCRIBER-DRIVEN audit.Entry call
    // sites #0226 examined, and five more -- admin-initiated (manual add,
    // suppression removal) and SES-driven (two bounce paths, one complaint)
    // -- also write the subscriber's email and also survive erasure. The
    // phrases below are unique to the widened wording: 'admin-initiated
    // actions' and 'automated entries from our delivery provider' assert
    // the item now names all three ORIGIN categories a row can come from,
    // not just the subscriber-driven one; 'no ip at all for the automated
    // delivery-provider entries' asserts the corrected IP claim (the three
    // SES entries set no IP field at all -- verified directly against
    // ses_notifications.go, not assumed); 'suppression removal', 'a
    // bounce', and 'a complaint' assert three of the newly-disclosed
    // email-carrying actions by name (manual add is covered by the
    // pre-existing 'your email address' + the categories phrase together).
    // The DURABLE version of "did we miss a sixth site" is
    // internal/handlers/audit_email_metadata_guard_test.go (#0237), which
    // reads the actual Go source rather than trusting this phrase list to
    // have been updated -- these phrases catch the PAGE regressing away
    // from what that guard has already confirmed is true, not the other
    // direction.
    //
    // #0237's own phase-3 review bounced the first pass for a real hole in
    // that Go guard (it matched only the literal key "email", missing the
    // "recipient_email"/"subscriber_address" shape a real site already
    // used) -- fixed by widening the guard's key match, which surfaced two
    // MORE real sites. Neither is added to the "six actions" list above
    // (neither is a subscriber's own address: one is the admin's own
    // address plus a synthetic test recipient, never disclosed here), but
    // one -- an export's audit entry, whose Metadata carries the raw text
    // of an admin's search box -- genuinely CAN carry a subscriber's own
    // address if that is what was searched for, so the item now discloses
    // it too, contingently rather than as a certainty. 'search text is
    // recorded' asserts that clause is still present; it is deliberately
    // NOT one of the "six actions" phrases above, since this seventh
    // disclosure is conditional, not structural, and the item's wording
    // says so ("if a staff member ever searches ... that search text is
    // recorded too", not "an export always records your address").
    requiredPhrases: [
      'audit log',
      'admin-initiated actions',
      'automated entries from our delivery provider',
      'no ip at all for the automated delivery-provider entries',
      'ip of the acting admin',
      'suppression removal',
      'a bounce',
      'a complaint',
      'your email address',
      'search text is recorded',
    ],
    sourceRef:
      'internal/handlers/audit_email_metadata_guard_test.go (#0237) pins the full, current set: internal/handlers/confirm.go ActionSubscriberConfirmed, internal/handlers/admin_subscribers.go ActionSubscriberManualAdd and ActionSubscriberErased, internal/handlers/admin_suppressions.go ActionSuppressionRemoved, internal/handlers/ses_notifications.go ActionSubscriberBounced (x2) and ActionSubscriberComplained, plus (pinned but NOT part of the "six actions" disclosure -- neither is a real subscriber\'s own address) internal/handlers/admin_campaign_preview.go ActionEmailCampaignTestSent and internal/handlers/admin_subscribers_export.go ActionSubscriberExported -- see that file for each one\'s exact line and IP-presence, re-derived from the tree on every run rather than hardcoded here a second time.',
  },
];

function findAttr(el: SvelteNode, name: string): SvelteNode | undefined {
  const attrs = el.attributes as SvelteNode[] | undefined;
  return attrs?.find((a) => a.type === 'Attribute' && a.name === name);
}

function attrTextEquals(attr: SvelteNode | undefined, want: string): boolean {
  const v = attr?.value;
  return Array.isArray(v) && v.length === 1 && (v[0] as SvelteNode)?.type === 'Text' && (v[0] as SvelteNode).data === want;
}

function collectByType(node: unknown, types: Set<string>, out: SvelteNode[] = [], seen = new Set<unknown>()): SvelteNode[] {
  if (node === null || typeof node !== 'object') return out;
  if (seen.has(node)) return out;
  seen.add(node);
  if (Array.isArray(node)) {
    for (const item of node) collectByType(item, types, out, seen);
    return out;
  }
  const obj = node as SvelteNode;
  if (typeof obj.type === 'string' && types.has(obj.type)) out.push(obj);
  for (const key of Object.keys(obj)) {
    if (key === 'parent') continue;
    collectByType(obj[key], types, out, seen);
  }
  return out;
}

// findErasureStatusListItems locates the section with
// aria-labelledby="leave-h" ("How to leave"), finds the (single)
// <StatusList> component inside it, and returns its `items` array-literal
// elements as plain strings, read directly off the parsed ArrayExpression
// -- not eval'd, not regex-matched against source text.
function findErasureStatusListItems(source: string): string[] {
  const ast = parseSvelte(source, { filename: 'PrivacyPolicy.svelte', modern: true }) as unknown as SvelteNode;

  const sections = collectByType(ast.fragment, new Set(['RegularElement'])).filter((n) => attrTextEquals(findAttr(n, 'aria-labelledby'), 'leave-h'));
  if (sections.length !== 1) {
    throw new Error(`expected exactly one section with aria-labelledby="leave-h", found ${sections.length}`);
  }

  const statusLists = collectByType(sections[0], new Set(['Component'])).filter((n) => n.name === 'StatusList');
  if (statusLists.length !== 1) {
    throw new Error(`expected exactly one <StatusList> inside the "How to leave" section, found ${statusLists.length}`);
  }

  const itemsAttr = findAttr(statusLists[0], 'items');
  const expr = (itemsAttr?.value as SvelteNode | undefined)?.expression as SvelteNode | undefined;
  if (!expr || expr.type !== 'ArrayExpression') {
    throw new Error('StatusList\'s "items" prop is not a literal array expression -- cannot read it structurally');
  }
  const elements = (expr.elements as SvelteNode[] | undefined) ?? [];
  return elements.map((el, i) => {
    if (el.type !== 'Literal' || typeof el.value !== 'string') {
      throw new Error(`StatusList items[${i}] is not a string literal`);
    }
    return el.value as string;
  });
}

interface MatchResult {
  matchedCategory: RetainedCategory | null;
  missingPhrases: string[];
}

// matchCategories pairs each StatusList item with the FIRST category all of
// whose requiredPhrases it contains (case-insensitive substring), and
// reports, per item, either the matched category or which category came
// closest and what phrase(s) it was missing -- so a failure names the
// reason, not just a mismatch (criterion 4).
function matchItemToCategory(item: string, categories: RetainedCategory[]): MatchResult {
  const lower = item.toLowerCase();
  let best: { category: RetainedCategory; missing: string[] } | null = null;
  for (const category of categories) {
    const missing = category.requiredPhrases.filter((p) => !lower.includes(p.toLowerCase()));
    if (missing.length === 0) {
      return { matchedCategory: category, missingPhrases: [] };
    }
    if (!best || missing.length < best.missing.length) {
      best = { category, missing };
    }
  }
  return { matchedCategory: null, missingPhrases: best?.missing ?? [] };
}

const SOURCE_FILES = import.meta.glob('./PrivacyPolicy.svelte', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

function readPrivacyPolicySource(): string {
  const entries = Object.values(SOURCE_FILES);
  if (entries.length !== 1) {
    throw new Error(`expected exactly one PrivacyPolicy.svelte via import.meta.glob, found ${entries.length}`);
  }
  return entries[0];
}

describe('privacy policy erasure list guard (#0226): the "How to leave" StatusList matches what erasure actually retains', () => {
  it('names exactly the categories erasure retains, each with its required substance', () => {
    const items = findErasureStatusListItems(readPrivacyPolicySource());

    const unmatched: string[] = [];
    const matchedKeys = new Set<string>();
    for (const item of items) {
      const { matchedCategory, missingPhrases } = matchItemToCategory(item, ERASURE_RETAINED_CATEGORIES);
      if (!matchedCategory) {
        unmatched.push(`  "${item}"\n    closest category missing: ${missingPhrases.join(', ') || '(no category matched at all)'}`);
        continue;
      }
      matchedKeys.add(matchedCategory.key);
    }

    const missingCategories = ERASURE_RETAINED_CATEGORIES.filter((c) => !matchedKeys.has(c.key));

    if (unmatched.length > 0 || missingCategories.length > 0) {
      const detail = [
        unmatched.length > 0
          ? `StatusList item(s) that do not match any known retained category (added without updating this guard, or reworded away from a category's required phrases):\n${unmatched.join('\n')}`
          : null,
        missingCategories.length > 0
          ? `Retained categor${missingCategories.length === 1 ? 'y' : 'ies'} the policy no longer names (removed from the list while erasure still keeps it -- over-promising deletion):\n${missingCategories
              .map((c) => `  ${c.key} (see ${c.sourceRef})`)
              .join('\n')}`
          : null,
      ]
        .filter(Boolean)
        .join('\n');
      throw new Error(`privacy policy erasure list does not match what erasure actually retains (#0060, #0226):\n${detail}`);
    }

    expect(items).toHaveLength(ERASURE_RETAINED_CATEGORIES.length);
  });
});

describe('privacy policy erasure list guard: mutation proofs (synthetic fixtures)', () => {
  const fixtureSource = (items: string[]): string => {
    const itemsLiteral = items.map((s) => `'${s.replace(/'/g, "\\'")}'`).join(',\n          ');
    return `<script>\n  let x = 1;\n</script>\n<section aria-labelledby="leave-h">\n  <StatusList\n    items={[\n          ${itemsLiteral},\n        ]}\n  />\n</section>`;
  };

  const realItems = (): string[] => findErasureStatusListItems(readPrivacyPolicySource());

  it('passes against the real, current four items', () => {
    const items = realItems();
    expect(items).toHaveLength(4);
    const matched = new Set<string>();
    for (const item of items) {
      const { matchedCategory } = matchItemToCategory(item, ERASURE_RETAINED_CATEGORIES);
      expect(matchedCategory, `real item did not match any category: "${item}"`).not.toBeNull();
      if (matchedCategory) matched.add(matchedCategory.key);
    }
    expect(matched.size).toBe(ERASURE_RETAINED_CATEGORIES.length);
  });

  it('fails when a fifth retained thing is added without updating the policy (criterion: adding without updating fails)', () => {
    const items = [...realItems(), 'a fifth thing nobody told the policy about'];
    const src = fixtureSource(items);
    const parsedItems = findErasureStatusListItems(src);
    expect(parsedItems).toHaveLength(5);

    const { matchedCategory } = matchItemToCategory(parsedItems[4], ERASURE_RETAINED_CATEGORIES);
    expect(matchedCategory, 'the fifth, unrecognized item should not match any known category').toBeNull();
  });

  it('fails when a category the policy still names is removed (criterion: removing a still-retained category fails, in the other direction)', () => {
    // Drop the audit_log item -- the shape #0060's own bounce found:
    // over-promising deletion by omission.
    const items = realItems().slice(0, 3);
    const src = fixtureSource(items);
    const parsedItems = findErasureStatusListItems(src);
    expect(parsedItems).toHaveLength(3);

    const matched = new Set<string>();
    for (const item of parsedItems) {
      const { matchedCategory } = matchItemToCategory(item, ERASURE_RETAINED_CATEGORIES);
      if (matchedCategory) matched.add(matchedCategory.key);
    }
    const missing = ERASURE_RETAINED_CATEGORIES.filter((c) => !matched.has(c.key));
    expect(missing.length, 'removing the audit_log item should leave exactly one category unmatched').toBe(1);
    expect(missing[0].key).toContain('audit_log');
  });

  it('fails when the deliverability-events item drops the bounce/complaint-handling purpose (criterion 6, in reverse)', () => {
    const items = realItems().map((item) =>
      item.includes('deliverability events') ? item.replace(' and to keep our own bounce/complaint handling accurate', '') : item,
    );
    const src = fixtureSource(items);
    const parsedItems = findErasureStatusListItems(src);

    const eventsItem = parsedItems.find((i) => i.includes('deliverability events'));
    expect(eventsItem, 'fixture setup: expected a deliverability-events item').toBeDefined();

    const eventsCategory = ERASURE_RETAINED_CATEGORIES.find((c) => c.key.startsWith('email_events'));
    expect(eventsCategory, 'fixture setup: expected the email_events category to exist').toBeDefined();
    // Check the missing phrase against its OWN category directly, not the
    // tree-wide "closest of all four categories" search matchItemToCategory
    // does for the main test's diagnostic message -- with the purpose
    // clause gone, this item is ALSO missing "suppression" and
    // "anonymized" relative to the other three categories, so the closest
    // match by phrase-count alone is not necessarily email_events, and
    // asserting against the wrong category's missing list would be a
    // vacuous test.
    const lower = (eventsItem ?? '').toLowerCase();
    const missingFromOwnCategory = (eventsCategory?.requiredPhrases ?? []).filter((p) => !lower.includes(p.toLowerCase()));
    expect(missingFromOwnCategory).toEqual(['bounce/complaint handling']);

    const { matchedCategory } = matchItemToCategory(eventsItem ?? '', ERASURE_RETAINED_CATEGORIES);
    expect(matchedCategory, 'weakened deliverability-events item should no longer match ANY category').toBeNull();
  });

  it('fails when the audit-log item drops the email-address disclosure (#0226 first-review-pass regression, in reverse)', () => {
    // #0226's first review pass deleted exactly this clause while widening
    // the IP phrasing, and the guard at the time had no requiredPhrase that
    // would have caught it. Reproduce that exact deletion against the REAL
    // current item text and confirm the guard now fires.
    const items = realItems().map((item) =>
      item.includes('audit log entry')
        ? item.replace(
            '; the confirmation entry, being added manually, a suppression removal, a bounce, a complaint, and the erasure entry itself also record your email address explicitly',
            '',
          )
        : item,
    );
    const src = fixtureSource(items);
    const parsedItems = findErasureStatusListItems(src);

    const auditItem = parsedItems.find((i) => i.includes('audit log entry'));
    expect(auditItem, 'fixture setup: expected an audit-log item').toBeDefined();
    expect(auditItem, 'fixture setup: the mutation must actually remove the email clause').not.toContain('your email address');

    const auditCategory = ERASURE_RETAINED_CATEGORIES.find((c) => c.key.startsWith('audit_log'));
    expect(auditCategory, 'fixture setup: expected the audit_log category to exist').toBeDefined();
    const lower = (auditItem ?? '').toLowerCase();
    const missingFromOwnCategory = (auditCategory?.requiredPhrases ?? []).filter((p) => !lower.includes(p.toLowerCase()));
    // This mutation removes the whole trailing clause, which also happens to
    // be the only place "suppression removal" and "a complaint" (as opposed
    // to "a spam complaint" in the categories intro) appear -- so three
    // phrases go missing here, not just the email one; that is a property
    // of THIS mutation's shape, not a weaker guard.
    expect(missingFromOwnCategory).toEqual(['suppression removal', 'a complaint', 'your email address']);

    const { matchedCategory } = matchItemToCategory(auditItem ?? '', ERASURE_RETAINED_CATEGORIES);
    expect(matchedCategory, 'the audit-log item with the email disclosure dropped should no longer match ANY category').toBeNull();
  });

  it('fails when the audit-log item narrows back to only "signup IP" (criterion 5, in reverse)', () => {
    const narrowed =
      'an internal admin audit log entry recording earlier actions on your account (signup, confirmation, and the erasure itself) — this includes your email address and your signup IP address, kept so we can prove a request was honored and to investigate abuse; it is not exposed publicly and is not used to re-add or re-contact you';

    const auditCategory = ERASURE_RETAINED_CATEGORIES.find((c) => c.key.startsWith('audit_log'));
    expect(auditCategory, 'fixture setup: expected the audit_log category to exist').toBeDefined();
    const lower = narrowed.toLowerCase();
    const missingFromOwnCategory = (auditCategory?.requiredPhrases ?? []).filter((p) => !lower.includes(p.toLowerCase()));
    expect(missingFromOwnCategory).toEqual([
      'admin-initiated actions',
      'automated entries from our delivery provider',
      'no ip at all for the automated delivery-provider entries',
      'ip of the acting admin',
      'suppression removal',
      'a bounce',
      'a complaint',
      'search text is recorded',
    ]);

    const { matchedCategory } = matchItemToCategory(narrowed, ERASURE_RETAINED_CATEGORIES);
    expect(matchedCategory, 'the old, narrower audit-log wording should no longer match ANY category').toBeNull();
  });

  it('fails when the audit-log item drops coverage of the admin-initiated and SES-driven categories (#0237, in reverse)', () => {
    // #0237's own regression: the item narrows back to naming only the
    // subscriber-driven actions #0226 examined, silently dropping the
    // admin-initiated and automated-provider categories this widening
    // exists to disclose -- reproduced against the REAL current item text,
    // not a hand-written fixture, per #0237's acceptance criterion that
    // this be proved against PrivacyPolicy.svelte's actual content.
    const items = realItems().map((item) =>
      item.includes('audit log entry')
        ? item.replace(
            ' but also the erasure itself, admin-initiated actions (for example, being added to the list manually or having a suppression removed), and automated entries from our delivery provider (a bounce or a spam complaint registered against your address)',
            '',
          )
        : item,
    );
    const src = fixtureSource(items);
    const parsedItems = findErasureStatusListItems(src);

    const auditItem = parsedItems.find((i) => i.includes('audit log entry'));
    expect(auditItem, 'fixture setup: expected an audit-log item').toBeDefined();
    expect(auditItem, 'fixture setup: the mutation must actually remove the widened categories clause').not.toContain('admin-initiated actions');

    const auditCategory = ERASURE_RETAINED_CATEGORIES.find((c) => c.key.startsWith('audit_log'));
    expect(auditCategory, 'fixture setup: expected the audit_log category to exist').toBeDefined();
    const lower = (auditItem ?? '').toLowerCase();
    const missingFromOwnCategory = (auditCategory?.requiredPhrases ?? []).filter((p) => !lower.includes(p.toLowerCase()));
    expect(missingFromOwnCategory).toEqual(['admin-initiated actions', 'automated entries from our delivery provider']);

    const { matchedCategory } = matchItemToCategory(auditItem ?? '', ERASURE_RETAINED_CATEGORIES);
    expect(matchedCategory, 'the item with categories coverage dropped should no longer match ANY category').toBeNull();
  });

  it('fails when the audit-log item drops the corrected IP claim for automated SES entries (#0237, in reverse)', () => {
    // The other #0237 regression: the categories stay named, but the
    // exception clause -- the SES-driven entries carry no IP at all -- is
    // dropped, silently reverting to the false "each entry records the IP"
    // claim #0237 found untrue for those three entries. Reproduced against
    // the real current item text.
    const items = realItems().map((item) =>
      item.includes('audit log entry')
        ? item.replace(', and no IP at all for the automated delivery-provider entries, since there is no request behind them', '')
        : item,
    );
    const src = fixtureSource(items);
    const parsedItems = findErasureStatusListItems(src);

    const auditItem = parsedItems.find((i) => i.includes('audit log entry'));
    expect(auditItem, 'fixture setup: expected an audit-log item').toBeDefined();
    expect(auditItem, 'fixture setup: the mutation must actually remove the no-IP exception').not.toContain('no IP at all');

    const auditCategory = ERASURE_RETAINED_CATEGORIES.find((c) => c.key.startsWith('audit_log'));
    expect(auditCategory, 'fixture setup: expected the audit_log category to exist').toBeDefined();
    const lower = (auditItem ?? '').toLowerCase();
    const missingFromOwnCategory = (auditCategory?.requiredPhrases ?? []).filter((p) => !lower.includes(p.toLowerCase()));
    expect(missingFromOwnCategory).toEqual(['no ip at all for the automated delivery-provider entries']);

    const { matchedCategory } = matchItemToCategory(auditItem ?? '', ERASURE_RETAINED_CATEGORIES);
    expect(matchedCategory, 'the item with the no-IP exception dropped should no longer match ANY category').toBeNull();
  });

  it('fails when the audit-log item drops the export-search-text disclosure (#0237 fix pass, in reverse)', () => {
    // #0237's phase-3 review found the widened
    // metadataKeyIsSuspectedEmailCarrier Go guard now (correctly) pins
    // internal/handlers/admin_subscribers_export.go's "filter_query" key --
    // an admin's raw search text, which MAY be a subscriber's own address
    // if that is what was searched for while exporting the list. The item
    // discloses this contingently ("if a staff member ever searches ...
    // that search text is recorded too"). Dropping that clause silently
    // reverts to the earlier under-disclosure this issue exists to fix --
    // reproduced against the real current item text.
    const items = realItems().map((item) =>
      item.includes('audit log entry')
        ? item.replace(
            ', and if a staff member ever searches the subscriber list by your address while exporting it, that search text is recorded too',
            '',
          )
        : item,
    );
    const src = fixtureSource(items);
    const parsedItems = findErasureStatusListItems(src);

    const auditItem = parsedItems.find((i) => i.includes('audit log entry'));
    expect(auditItem, 'fixture setup: expected an audit-log item').toBeDefined();
    expect(auditItem, 'fixture setup: the mutation must actually remove the export-search clause').not.toContain('search text is recorded');

    const auditCategory = ERASURE_RETAINED_CATEGORIES.find((c) => c.key.startsWith('audit_log'));
    expect(auditCategory, 'fixture setup: expected the audit_log category to exist').toBeDefined();
    const lower = (auditItem ?? '').toLowerCase();
    const missingFromOwnCategory = (auditCategory?.requiredPhrases ?? []).filter((p) => !lower.includes(p.toLowerCase()));
    expect(missingFromOwnCategory).toEqual(['search text is recorded']);

    const { matchedCategory } = matchItemToCategory(auditItem ?? '', ERASURE_RETAINED_CATEGORIES);
    expect(matchedCategory, 'the item with the export-search disclosure dropped should no longer match ANY category').toBeNull();
  });
});
