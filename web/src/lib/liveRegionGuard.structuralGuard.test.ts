// #0242/#0243: an AST guard for the console-wide live-region decision #0063
// made in prose ("a live region must be a persistent DOM node whose text
// mutates, never a node created and destroyed by an {#if}/{#each}") and
// then applied inconsistently -- its own phase-3 re-review found six
// unconverted role="status" sites the first pass missed by scoping its
// sweep to the files it happened to be editing (#0242's Description), and
// separately found ~50 role="alert" sites the decision never even
// addressed (#0243's Description). Built as ONE guard covering both roles,
// per #0243's own acceptance criterion 3 ("#0242's AST guard covers
// role="alert" as well as role="status""), rather than two.
//
// ## The role="alert" decision (#0243)
//
// role="alert" is NOT held to the persistence rule. ARIA's alert role is
// the one live-region role assistive-technology vendors handle reliably ON
// INSERTION (unlike status/aria-live="polite", which mostly needs an
// already-present node to MUTATE) -- #0063's re-review's own finding 1
// makes this argument, and it is adopted here as the recorded decision
// this criterion asks for: an {#if}-created role="alert" region is
// accepted as sound, not converted. This guard still walks and counts
// every role="alert" site (the enumeration #0243 asks for), and still
// requires each one resolve to real content (a dynamic expression or
// non-empty static text) -- an EMPTY alert region would announce nothing
// on insertion either way, so that check has real teeth despite the
// decision.
//
// ## The role="status" rule (#0242, extended by #0286)
//
// A role="status" (or an explicit aria-live="polite"/"assertive" with no
// role="alert") element that sits inside ANY {#if}/{:else}/{#each} branch
// at all (site.governingBranch !== undefined -- a fully unconditional
// element, governingBranch undefined, is never checked: that is the sound
// "persistent node whose text mutates" case #0063 decided on) is a
// violation unless it clears ONE of three escape hatches, tried in order:
//
//   1. LOADING PLACEHOLDER: the element is STATICALLY-worded (no
//      {expression} children at all -- the text never varies) and its
//      file+text pair is named in KNOWN_LOADING_PLACEHOLDERS. #0063's own
//      fix pass explicitly enumerated 17 (at the time of writing; see
//      pageTitle.ts's sibling comment on drift and WorkshopEditor.svelte's
//      own doc comment for today's count) of exactly this shape --
//      `{#if loading}<p role="status">Loading…</p>{/if}` -- and reported
//      them as a deliberate, smaller-severity remainder rather than
//      converting them ("they announce an initial-load state rather than
//      the result of a user action"). This guard's job is to hold that
//      decision in code instead of prose, not to silently relitigate it.
//
//   2. WHOLE-PANEL SWAP: scanning the site's nearest-enclosing branch's
//      subtree (not just ancestors of the status element -- Unsubscribe.
//      svelte's `{doneMessage}` status paragraph is a SIBLING of the
//      `<h1 tabindex="-1" bind:this={doneHeading}>` that actually receives
//      focus, inside the same `{:else}` branch, not an ancestor of it)
//      finds an element carrying BOTH `tabindex="-1"` and `bind:this={V}`,
//      and the component's <script> calls `V.focus()` / `V?.focus()`
//      (zero arguments) anywhere -- not required to sit inside `$effect`,
//      unlike modalFocusWiring's stricter check, because this codebase's
//      real sites (Login.svelte, Unsubscribe.svelte, PreferenceCenter.svelte,
//      CampaignEditor.svelte) call it from a plain `await tick();
//      V?.focus();` inside an async handler, not a reactive effect. This is
//      the "legitimate alternative" #0242's criterion 2 asks the guard to
//      distinguish rather than flag: when the whole branch is fresh (its
//      FIRST appearance, the only time insertion vs. mutation matters),
//      focus moving to SOME element within it is a real signal that orients
//      an AT user to the new panel, after which any role="status" node
//      already inside it mutates normally like any other live region.
//      Nothing requires the swap target to be "about" this specific status
//      site -- CampaignEditor.svelte's `headingEl` (focused once, when a
//      campaign first loads) legitimately covers half a dozen status/
//      aria-live regions scattered through that same large `{:else if
//      campaign}` branch, because what's being verified is "does entering
//      this subtree coincide with a focus move", not "does this exact node
//      get read aloud".
//
//      #0299 TIGHTENED which subtree counts as "the site's nearest-
//      enclosing branch's subtree": the scan does NOT cross into a NESTED
//      {#if}/{:else}/{#each} looking for a target. A target reachable only
//      by first entering some FURTHER, inner conditional -- a modal dialog
//      gated behind its own `{#if showModal}`, say -- has its OWN, narrower
//      governing branch, which mounts and unmounts on its own trigger (the
//      modal opening), not on whatever mounts the outer branch the status
//      site is actually in. #0286's widening (below) had made this hatch
//      the largest of the three, and its review measured 5 in-branch sites
//      passing it for exactly that reason: Admin.svelte's newSlugInvalid
//      hint, createSubNotice, and the CSV-import notice (all matching a
//      modal's own tabindex="-1"+bind:this target several conditionals
//      deeper in the same large per-tab branch), plus WorkshopEditor.
//      svelte's saveNotice and unsavedInterestsHint (matching
//      transitionModalEl, similarly nested behind its own `{#if
//      transitionOpen}`). All five are unconditionally rendered themselves,
//      inside a large, stable, multi-purpose branch -- the SAME shape as
//      settingsNotice/addressNotice/CampaignEditor's audience-count in
//      check 3 below, just previously mis-credited to an unrelated modal
//      instead of to that persistence argument. All five are now named
//      KNOWN_STABLE_BRANCH_SITES entries instead (see findFocusTargetVar's
//      own doc comment, and those sets' own comments, for the full account).
//      CampaignEditor.svelte's `headingEl` case above is UNAFFECTED: every
//      status region it covers sits directly in the SAME `{:else if
//      campaign}` fragment as `headingEl` itself, with no intervening
//      IfBlock/EachBlock, so "same governing branch" is exactly what that
//      paragraph was already describing.
//
//   3. KNOWN STABLE BRANCH: neither of the above, but the site's file+text
//      pair is named in KNOWN_STABLE_BRANCH_SITES -- for sites whose OWN
//      presence is not what makes their governing branch mount or unmount
//      (settingsNotice, nested three container-levels inside Admin.svelte's
//      large, stable Settings-tab branch; CampaignEditor's audience-count,
//      nested inside a <Panel> inside `{:else if campaign}`, which renders
//      the whole rest of the editor -- the guard's own former calibration
//      examples for why a naive "does this branch have exactly one child"
//      rule was wrong) and which also have no focus-swap target nearby.
//      Each entry's `reason` restates that same argument for its own site,
//      per #0280's justification requirement -- see that set's own comment.
//
// #0242 originally reached checks 1 and 2 only for a site whose governing
// branch was "single-purpose" (found by unwrapping single-child containers
// down to the element itself, stopping at the first fork -- a fork being
// more than one significant child anywhere on the path, e.g. a heading
// ALONGSIDE the status paragraph rather than wrapping it). Any FORKED
// branch bypassed both checks entirely and passed unconditionally --
// #0242's own review measured that gap at 24 of 47 in-branch sites, this
// concrete shape included:
//
//   {#if err}
//     <div class="wrap"><h2>Oops</h2><p role="status">{err}</p></div>
//   {/if}
//
// That is the same gap #0244's own item 1 turned out to be
// (PreferenceCenter.svelte's two-child `{#if showSubscribeAgainAffordance}`
// branch had no focus management, and the old guard would not have found
// it either). #0286 closes it by running checks 1 and 2 for EVERY in-branch
// site regardless of fork shape -- both were already branch-scoped, not
// target-scoped or fork-shape-scoped, so nothing about their mechanics
// needed to change -- with check 3 as the named escape hatch for the sites
// that legitimately need one. This makes the single-child unwrap itself
// (what used to be isGoverningBranchDynamic) dead code: it never controlled
// WHAT got scanned, only WHETHER scanning happened, and now scanning always
// happens. Removed rather than kept as an inert second mechanism sitting
// beside the real one (CLAUDE.md §8's warning about two overlapping,
// unexplained mechanisms) -- see the removal note in its old location,
// just above the `Site` interface, for the fuller history.
//
// ## "Cannot classify" (#0242 criterion 5, #0243's dynamic-role finding)
//
// A site carrying a NON-STATIC role or aria-live attribute value (a
// `role={...}` or `aria-live={...}` expression, not a plain string) --
// this guard cannot statically prove what role such a site resolves to at
// runtime, so it refuses to guess. Any such site fails loudly, by name,
// rather than being silently skipped by the (necessarily string-matching)
// classifier below. KNOWN_DYNAMIC_ROLE_SITES held exactly one entry
// (Dashboard.svelte's per-warning list item, `role={w.alert ? 'alert' :
// 'status'}`) until #0279 converted it to two statically-rolled branches
// plus a persistent announcer, emptying the set; see that set's own
// comment for why it stays declared rather than deleted.
//
// ## Instrument
//
// AST only (svelte/compiler's parse, `modern: true`), no mounting -- same
// class of guard as modalFocusWiring.structuralGuard.test.ts and
// citationGuard.test.ts. #0243's Notes are explicit that the compiler, not
// a browser, is the right instrument for this exact defect class
// (SubscribeForm.svelte's original bug was a source-vs-compiled
// divergence) -- this guard parses SOURCE, which is sufficient here because
// the property under test (is this element inside an {#if}/{#each}) is
// visible in the source AST already; #0243's re-review additionally
// compiled and read `$.if`/`$.set_text` output to prove the specific ten
// role="status" conversions, which is a stronger, complementary check this
// guard does not repeat.
import { describe, expect, it } from 'vitest';
import { parse as parseSvelte } from 'svelte/compiler';

type SvelteNode = Record<string, unknown>;

// #0242 review (eba2de9): this used to glob '../views/**/*.svelte', which
// resolves to web/src/views ONLY -- so the thirteen .svelte files outside
// it (App.svelte and all twelve of web/src/lib/*.svelte) were never
// scanned. One of them held a live region: web/src/lib/SubscribeForm.svelte,
// this issue's OWN motivating example, whose original {#if}/{:else}-created
// error paragraph "looked correct by inspection" and was only proved broken
// in a real browser. That component (now converted, #0063) currently
// passes this guard's rule on its merits, but nothing would have noticed if
// it silently regressed -- criterion 4 exists verbatim because #0063 was
// bounced for exactly this shape of scoping error one directory up.
// Widened to the whole of web/src so every .svelte file in the tree is
// covered, matching criterion 4's "not a hand-maintained file list".
const SOURCE_FILES = import.meta.glob('../**/*.svelte', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

/** `globKey` is import.meta.glob's key: a path relative to THIS file's own
 * directory (web/src/lib) -- e.g. './Button.svelte' (a lib sibling),
 * '../App.svelte' (the parent), or '../views/Home.svelte' (a subdirectory
 * of the parent). A naive `slice('../'.length)` (the review's literal
 * suggestion) is wrong for the './...' sibling case -- it silently chops
 * three characters off the FILENAME instead of the prefix, e.g.
 * './Button.svelte' -> 'utton.svelte'. Resolving each '.'/'..' segment
 * against 'web/src/lib' by hand (rather than importing 'node:path', which
 * this project deliberately does without -- see nodeFsShim.d.ts's own doc
 * comment on why @types/node was removed in favor of hand-declared
 * ambients) handles all three shapes uniformly. */
function toRepoRelativePath(globKey: string): string {
  const segments = `web/src/lib/${globKey}`.split('/');
  const resolved: string[] = [];
  for (const segment of segments) {
    if (segment === '' || segment === '.') continue;
    if (segment === '..') {
      resolved.pop();
      continue;
    }
    resolved.push(segment);
  }
  return resolved.join('/');
}

/** #0298 criterion 4: the real union of scanned files, computed the same
 * way the real-tree test computes it, so a fixture proving "the current
 * real entries must all still pass" exercises the actual scan result rather
 * than a hand-typed stand-in for it. */
function scannedFileSet(): Set<string> {
  const s = new Set<string>();
  for (const globKey of Object.keys(SOURCE_FILES)) {
    if (globKey.endsWith('.test.ts')) continue;
    s.add(toRepoRelativePath(globKey));
  }
  return s;
}

// ---------------------------------------------------------------------------
// Template-side helpers
// ---------------------------------------------------------------------------

function findAttr(el: SvelteNode, name: string): SvelteNode | undefined {
  const attrs = el.attributes as SvelteNode[] | undefined;
  return attrs?.find((a) => a.type === 'Attribute' && a.name === name);
}

/** The attribute's value as: a plain string (a single static Text value),
 * 'ABSENT' (no such attribute), or 'DYNAMIC' (present, but not a single
 * static Text -- an {expression}, or Text + expression concatenation). */
function staticAttrValue(el: SvelteNode, name: string): string | 'ABSENT' | 'DYNAMIC' {
  const attr = findAttr(el, name);
  if (!attr) return 'ABSENT';
  const value = attr.value;
  if (value === true) return 'DYNAMIC'; // a bare boolean attribute, e.g. `disabled`
  if (!Array.isArray(value)) return 'DYNAMIC';
  if (value.length === 0) return ''; // e.g. attr=""
  if (value.length === 1 && (value[0] as SvelteNode).type === 'Text') {
    return (value[0] as SvelteNode).data as string;
  }
  return 'DYNAMIC';
}

function findBindThisVar(el: SvelteNode): string | undefined {
  const attrs = el.attributes as SvelteNode[] | undefined;
  const bind = attrs?.find((a) => a.type === 'BindDirective' && a.name === 'this');
  const expr = bind?.expression as SvelteNode | undefined;
  return expr?.type === 'Identifier' ? (expr.name as string) : undefined;
}

function hasTabIndexNegOne(el: SvelteNode): boolean {
  return staticAttrValue(el, 'tabindex') === '-1';
}

function isWhitespaceOnlyText(node: SvelteNode): boolean {
  return node.type === 'Text' && /^\s*$/.test((node.data as string) ?? '');
}

/** Significant (non-whitespace-Text, non-Comment) top-level nodes of a
 * Fragment -- used both to size a governing branch (see the file header)
 * and to decide whether a status/alert element itself carries real content
 * (the alert-emptiness check). */
function significantNodes(fragment: SvelteNode | undefined): SvelteNode[] {
  const nodes = (fragment?.nodes as SvelteNode[] | undefined) ?? [];
  return nodes.filter((n) => n.type !== 'Comment' && !isWhitespaceOnlyText(n));
}

// #0286 removed isGoverningBranchDynamic (the single-child-container-unwrap
// classifier that used to live here) as no longer needed -- CLAUDE.md §8:
// "the single-child unwrap is either removed as no longer needed or
// retained with its reasoning restated." It computed whether a status
// site's governing branch was "single-purpose" (unwrapping single-child
// containers reaches the element itself with no fork), and checkFile used
// that boolean to decide whether to run ANY check at all: single-purpose
// branches got the placeholder/swap-target check; the instant a branch
// forked (more than one significant child anywhere on the unwrap path --
// e.g. a heading ALONGSIDE the status paragraph, not wrapping it), the
// site was pushed straight into statusSites with NO check whatsoever. On
// the tree as of #0242's review that was 24 of 47 in-branch sites getting
// zero scrutiny -- the "disclosed boundary" this issue closes.
//
// The fix is not a bigger classifier; it's applying the SAME check
// checkFile already ran for single-purpose sites to every in-branch site,
// fork or not. That check was ALREADY branch-scoped, not target-scoped:
// findFocusTargetVar(site.governingBranch) always scanned the site's
// nearest-enclosing branch's WHOLE subtree (that's what let it find
// Unsubscribe.svelte's doneHeading, a SIBLING of the status paragraph, not
// an ancestor of it), and the placeholder lookup was always keyed on
// site.file + site.text, never on fork-ness. So isGoverningBranchDynamic
// never controlled WHAT got scanned -- only WHETHER scanning happened at
// all. Once that gate is removed and every in-branch site runs the same
// two checks, a third escape hatch (KNOWN_STABLE_BRANCH_SITES, #0280-
// shaped) covers what's left: sites whose OWN presence isn't what makes
// their branch mount/unmount (settingsNotice, CampaignEditor's
// audience-count -- the guard's own former calibration examples for why
// the naive "immediate child count" rule was wrong) but which also have no
// focus-swap target nearby. Those two examples, and the others found
// auditing every real violation this change produced, are now individual,
// justified entries instead of being silently inferred by branch shape.
// One genuine defect turned up in the same audit (not a false positive):
// Admin.svelte's CSV-import-commit notice really was created fresh by
// {#if importCommitResult} alongside siblings, dynamic text, no swap --
// fixed properly (made an unconditional, persistent node) rather than
// allowlisted, since #0280's justification field asks for a REASON, and
// "this is actually a bug" isn't one.
//
// #0286's own review measured the state its widening produced, of 48
// in-branch sites:
//
//   loading placeholders          20
//   whole-panel-swap targets      21
//   named stable-branch entries    7
//   unchecked                      0
//
// #0299 found the swap hatch -- now the largest of the three -- loose: a
// site passed it when a focus move existed ANYWHERE in its governing
// branch, including behind a nested modal's own separate {#if}, unrelated
// to the site (see findFocusTargetVar's own doc comment, and the WHOLE-
// PANEL SWAP section of this header, above, for the full account). 5 of
// the 21 swap sites were passing that way; all 5 are now named
// KNOWN_STABLE_BRANCH_SITES entries instead (the persistence argument that
// was actually true of them all along), with 0 requiring a code change and
// 0 left over needing allowlisting-without-resolution. Re-measured
// (`#0299` criterion 4) after the tightening, same 48 sites, same table
// form -- this is the tally the next widening should start from:
//
//   loading placeholders          20
//   whole-panel-swap targets      16
//   named stable-branch entries   12
//   unchecked                      0
//
// (alertSites 61, statusSites 32 -- unchanged by this issue, which only
// reclassifies WITHIN the in-branch role="status" sites; see the real-tree
// test's own floor comments for those two.)

interface Site {
  file: string;
  line: number;
  role: 'status' | 'alert';
  text: string; // best-effort human-readable label for reporting/dedup
  isStaticOnly: boolean; // no ExpressionTag children at all
  governingBranch: SvelteNode | undefined; // the branch fragment, if any
  node: SvelteNode; // the element itself
  rawSource: string; // #0286: the element's own raw source -- KNOWN_STABLE_BRANCH_SITES' match key
}

interface UnclassifiableSite {
  file: string;
  line: number;
  reason: string;
  /** #0280: the dynamic role/aria-live attribute's own raw source text
   * (e.g. `role={w.alert ? 'alert' : 'status'}`) -- KNOWN_DYNAMIC_ROLE_SITES'
   * stable match key, computed once here so the guard and its synthetic
   * fixtures derive it identically. Survives line-shifting edits above the
   * site; only goes stale if the attribute expression itself changes. */
  matchKey: string;
}

/** #0280: raw source text of the named attribute on `el`, e.g.
 * `role={w.alert ? 'alert' : 'status'}` -- used as a stable allowlist match
 * key instead of a line number. Empty string if the attribute or its
 * source span is missing (defensive; every Attribute node from svelte's
 * parser carries start/end in practice). */
function rawAttrSource(source: string, el: SvelteNode, name: string): string {
  const attr = findAttr(el, name);
  const start = attr?.start as number | undefined;
  const end = attr?.end as number | undefined;
  if (start === undefined || end === undefined) return '';
  return source.slice(start, end);
}

/** Depth-first walk collecting every classifiable/unclassifiable
 * role="status"/role="alert"/aria-live site in `source`, plus the nearest
 * enclosing {#if}/{:else}/{#each} branch fragment for each (undefined ==
 * unconditional). "Nearest" replaces, not accumulates, matching
 * modalFocusWiring's collectPanels/collectFocusCallSites -- see this file's
 * header for why a branch several levels up (settingsNotice's `{:else}`, not
 * some INNER wrapper) is what's actually being measured for each site: there
 * is no inner wrapper in any real site here, so "nearest" and "nearest
 * IfBlock/EachBlock ancestor found by walking up" coincide in practice.
 */
function collectSites(
  fileName: string,
  source: string,
): { sites: Site[]; unclassifiable: UnclassifiableSite[] } {
  const ast = parseSvelte(source, { filename: fileName, modern: true }) as unknown as SvelteNode;
  const lineOf = (offset: number): number => source.slice(0, offset).split('\n').length;

  const sites: Site[] = [];
  const unclassifiable: UnclassifiableSite[] = [];

  function walk(node: unknown, governingBranch: SvelteNode | undefined, seen = new Set<unknown>()): void {
    if (node === null || typeof node !== 'object') return;
    if (seen.has(node)) return;
    seen.add(node);
    if (Array.isArray(node)) {
      for (const item of node) walk(item, governingBranch, seen);
      return;
    }
    const obj = node as SvelteNode;

    if (obj.type === 'IfBlock') {
      const consequent = obj.consequent as SvelteNode | undefined;
      const alternate = obj.alternate as SvelteNode | undefined;
      walk(consequent, consequent, seen);
      if (alternate) walk(alternate, alternate, seen);
      return;
    }
    if (obj.type === 'EachBlock') {
      const body = obj.body as SvelteNode | undefined;
      walk(body, body, seen);
      // EachBlock also carries an optional {:else} (empty-list) fragment,
      // structurally irrelevant to live-region classification -- walked
      // generically below via the fallthrough.
    }

    if (obj.type === 'RegularElement') {
      const roleRaw = staticAttrValue(obj, 'role');
      const ariaLiveRaw = staticAttrValue(obj, 'aria-live');
      const start = obj.start as number | undefined;
      const line = start !== undefined ? lineOf(start) : 0;

      const roleIsDynamicExpr = roleRaw === 'DYNAMIC';
      const ariaLiveIsDynamicExpr = ariaLiveRaw === 'DYNAMIC';

      if (roleIsDynamicExpr || ariaLiveIsDynamicExpr) {
        // A role or aria-live value this guard cannot resolve statically.
        // Reported loudly (#0242 criterion 5) rather than silently skipped
        // by the string-equality checks below, which would otherwise just
        // never match a non-string value and fall through unnoticed.
        const dynamicAttrName = roleIsDynamicExpr ? 'role' : 'aria-live';
        unclassifiable.push({
          file: fileName,
          line,
          reason: `${dynamicAttrName} is a dynamic expression, not a static string -- this guard cannot verify which live-region rule (if any) applies`,
          matchKey: rawAttrSource(source, obj, dynamicAttrName),
        });
      } else {
        const role = roleRaw === 'status' || roleRaw === 'alert' ? roleRaw : undefined;
        const impliedStatus =
          role === undefined && (ariaLiveRaw === 'polite' || ariaLiveRaw === 'assertive');

        if (role === 'alert' || role === 'status' || impliedStatus) {
          const kind: 'status' | 'alert' = role === 'alert' ? 'alert' : 'status';
          const kids = significantNodes(obj.fragment as SvelteNode | undefined);
          const isStaticOnly = kids.every((k) => k.type === 'Text');
          const text = kids
            .map((k) => (k.type === 'Text' ? (k.data as string) : '{…}'))
            .join('')
            .trim();
          // #0286: the element's own raw source (opening tag through
          // closing tag) -- KNOWN_STABLE_BRANCH_SITES' match key. `text`
          // collapses every expression to the literal string '{…}', so two
          // purely-dynamic sites in the SAME file (e.g. Admin.svelte's
          // settingsNotice and addressNotice, both `{x ?? ''}`-shaped)
          // would collide on it; the raw source -- which differs by
          // variable name, class, etc. -- doesn't.
          const rawSource = obj.start !== undefined && obj.end !== undefined ? source.slice(obj.start as number, obj.end as number) : '';
          sites.push({ file: fileName, line, role: kind, text, isStaticOnly, governingBranch, node: obj, rawSource });
        }
      }
    }

    for (const key of Object.keys(obj)) {
      if (key === 'parent') continue;
      walk(obj[key], governingBranch, seen);
    }
  }

  walk(ast.fragment, undefined);
  return { sites, unclassifiable };
}

/** True if `root`'s subtree contains an element with BOTH tabindex="-1" and
 * bind:this={someVar} -- returns that var's name, or undefined. Used to find
 * a swap's focus target anywhere in the governing branch, not just among
 * ancestors of the status element itself (Unsubscribe.svelte's shape).
 *
 * #0299: does NOT descend past a nested {#if}/{:else}/{#each} inside `root`.
 * `root` is always a site's governingBranch fragment -- the nearest {#if}
 * (etc.) that the STATUS SITE itself sits in. A tabindex="-1"+bind:this
 * target reachable only by first entering some FURTHER, inner conditional
 * (a modal dialog gated behind its own `{#if showModal}`, say) has its own
 * narrower governing branch, distinct from `root`: that inner branch mounts
 * and unmounts on its own trigger (opening the modal), not on whatever
 * mounts `root`. Finding such a target anywhere in a large outer branch is
 * not evidence that entering THIS branch coincides with that focus move --
 * only that the branch happens to contain, somewhere deep inside it, a
 * dialog that manages its own focus for its own reason.
 *
 * Measured by #0299's review and confirmed by re-running the enumeration
 * before this change: the OLD (unrestricted) version let 5 in-branch status
 * sites -- Admin.svelte's newSlugInvalid hint, createSubNotice, and the
 * CSV-import notice (matching editInterestModalEl / subscriberDetailModalEl,
 * a modal several conditionals deeper in the same large per-tab branch, only
 * reachable via reading past the tab's OWN modal-open {#if}), plus
 * WorkshopEditor.svelte's saveNotice and unsavedInterestsHint (matching
 * transitionModalEl, similarly nested behind its own {#if transitionOpen})
 * -- pass the swap hatch for a reason that has nothing to do with them: an
 * unrelated modal, opened by an unrelated user action, happens to sit
 * somewhere else in the same branch. All five are unconditionally rendered
 * themselves (not gated by their OWN {#if}) inside a large, stable,
 * multi-purpose branch -- the SAME shape as settingsNotice/addressNotice
 * below, just previously mis-credited to the wrong mechanism. They are now
 * named KNOWN_STABLE_BRANCH_SITES entries instead (see that set), which is
 * what was actually true of them all along.
 *
 * Genuinely SAME-branch cases are unaffected, because the target sits
 * directly in `root`'s own fragment with no intervening IfBlock/EachBlock:
 * Login.svelte/Unsubscribe.svelte's self-bound or sibling notices, and
 * CampaignEditor.svelte's `headingEl`, which the file header already
 * documents as legitimately covering several status regions scattered
 * through the SAME `{:else if campaign}` fragment (not behind any further
 * conditional) -- that comment's reasoning is unchanged by this tightening,
 * because "same governing branch" is exactly what it was already describing. */
function findFocusTargetVar(root: unknown, seen = new Set<unknown>()): string | undefined {
  if (root === null || typeof root !== 'object') return undefined;
  if (seen.has(root)) return undefined;
  seen.add(root);
  if (Array.isArray(root)) {
    for (const item of root) {
      const found = findFocusTargetVar(item, seen);
      if (found) return found;
    }
    return undefined;
  }
  const obj = root as SvelteNode;
  // #0299: a nested branch has its own, narrower governing branch -- do not
  // cross into it looking for a target that "belongs" to THIS one.
  if (obj.type === 'IfBlock' || obj.type === 'EachBlock') return undefined;
  if (obj.type === 'RegularElement' && hasTabIndexNegOne(obj)) {
    const v = findBindThisVar(obj);
    if (v) return v;
  }
  for (const key of Object.keys(obj)) {
    if (key === 'parent') continue;
    const found = findFocusTargetVar(obj[key], seen);
    if (found) return found;
  }
  return undefined;
}

// ---------------------------------------------------------------------------
// Script-side helper: does <script> call `V.focus()` / `V?.focus()`
// (zero-arg) anywhere?
// ---------------------------------------------------------------------------

function unwrapChain(node: SvelteNode | undefined): SvelteNode | undefined {
  return node && node.type === 'ChainExpression' ? (node.expression as SvelteNode) : node;
}

function collectFocusedVars(node: unknown, out: Set<string>, seen = new Set<unknown>()): void {
  if (node === null || typeof node !== 'object') return;
  if (seen.has(node)) return;
  seen.add(node);
  if (Array.isArray(node)) {
    for (const item of node) collectFocusedVars(item, out, seen);
    return;
  }
  const obj = node as SvelteNode;
  if (obj.type === 'CallExpression') {
    const call = unwrapChain(obj) as SvelteNode;
    const callee = call.callee as SvelteNode | undefined;
    const args = (call.arguments as SvelteNode[] | undefined) ?? [];
    if (callee?.type === 'MemberExpression' && args.length === 0) {
      const prop = callee.property as SvelteNode | undefined;
      const objNode = unwrapChain(callee.object as SvelteNode | undefined);
      if (prop?.type === 'Identifier' && prop.name === 'focus' && objNode?.type === 'Identifier') {
        out.add(objNode.name as string);
      }
    }
  }
  for (const key of Object.keys(obj)) {
    if (key === 'parent') continue;
    collectFocusedVars(obj[key], out, seen);
  }
}

// ---------------------------------------------------------------------------
// KNOWN, NAMED exceptions -- not silently skipped (#0242 criterion 5)
// ---------------------------------------------------------------------------
//
// #0298: unjustifiedEntryViolations and staleEntryViolations below are both
// scoped to ONE file at a time (`e.file === fileName`, called once per file
// in the real-tree test's own loop), which means neither ever runs at all
// for an entry naming a file the scan never visits -- a typo'd path, or a
// component that was renamed or deleted out from under its allowlist entry.
// unscannedFileViolations (defined near findAllowlistEntry, just above
// checkFile) closes that: it runs ONCE PER RUN, after the scan loop
// completes, against the UNION of every file actually scanned, and checks
// both rules together for anything it finds -- an unscanned-file entry
// fails regardless of its reason, and if that reason is ALSO empty, that is
// reported as a second, separate violation, so a bogus path can never
// substitute for a real justification.
//
// #0280: these used to be `Set<string>` keyed by `file:line`. A line number
// shifts on ANY edit above it, so ordinary editing produced allowlist
// failures unrelated to the change -- #0125's own diff was 7 deletions + 7
// insertions of the SAME 7 KNOWN_LOADING_PLACEHOLDERS entries, renumbered,
// nothing else. Both sets are now `AllowlistEntry[]`, keyed on the site's
// own stable content (its static text, or its dynamic attribute's raw
// source) rather than its position, plus a REQUIRED, runtime-checked
// `reason` field -- criterion 2 asks for a mechanism, not a comment beside
// the entry that nothing enforces. `checkFile` below both validates every
// entry's justification and tracks which entries actually matched a real
// site, so a stale entry (naming a site that no longer exists, or whose
// text/attribute changed) still fails loudly -- criterion 3's requirement
// that fixing the churn not trade away the staleness check.

/** One allowlist row. `match` is the stable key (see each set's own
 * comment for what it is); `reason` is validated non-empty by
 * unjustifiedEntryViolations below, not merely documentation. */
interface AllowlistEntry {
  file: string;
  match: string;
  reason: string;
}

/** Sites with a dynamic role/aria-live expression this guard cannot resolve
 * statically -- see the file header's "cannot classify" section. `match` is
 * the dynamic attribute's own raw source text (e.g. `role={w.alert ? 'alert'
 * : 'status'}`), computed identically by collectSites' rawAttrSource. Adding
 * a NEW dynamic-role site anywhere in the tree fails this guard until it is
 * either made static or added here by name -- it can never silently pass. */
const KNOWN_DYNAMIC_ROLE_SITES: AllowlistEntry[] = [
  // #0279 converted Dashboard.svelte's per-warning list item -- the only
  // entry this set has ever held -- to two statically-rolled {#if w.alert}
  // branches plus a persistent role="status" announcer outside the
  // {#each}, so this set is empty as of that issue. Left declared, not
  // deleted: the mechanism above (ANY dynamic role/aria-live site anywhere
  // in the tree fails loudly unless named here) stays live and generally
  // useful, and an empty array correctly expresses "no exception is
  // currently justified" -- #0280's justification + staleness checks
  // already govern whatever gets added here next, so #0279 criterion 2's
  // "remove the set outright" alternative is not needed.
];

/** Governing-branch-dynamic, no swap target, but purely static text --
 * the shape #0063's fix pass explicitly enumerated and deliberately left
 * unconverted ("they announce an initial-load state rather than the result
 * of a user action, a smaller instance of the same defect class"). `match`
 * is the element's own static text (verified unique per file below -- two
 * placeholders in the same file always carry different copy). A NEW site of
 * this exact shape (single-child branch, static text, no focus target)
 * fails this guard until it is either converted or added here by name. */
const KNOWN_LOADING_PLACEHOLDER_REASON =
  "Single-child {#if}/{:each} branch whose sole content is this static, unvarying text (no {expression} children) -- #0063's own fix pass named this shape a deliberate, smaller-severity remainder: it announces an initial-load state, not the result of a user action, so #0063 exempted the Loading… family rather than restructuring every one into a persistent node (issues/0063.md). Not a defect; documented here so a NEW site of this exact shape must be named too, not silently pass.";

const KNOWN_LOADING_PLACEHOLDERS: AllowlistEntry[] = [
  { file: 'web/src/views/Admin.svelte', match: 'Loading settings…', reason: KNOWN_LOADING_PLACEHOLDER_REASON },
  { file: 'web/src/views/Admin.svelte', match: 'Loading users…', reason: KNOWN_LOADING_PLACEHOLDER_REASON },
  { file: 'web/src/views/Admin.svelte', match: 'Loading audit log…', reason: KNOWN_LOADING_PLACEHOLDER_REASON },
  { file: 'web/src/views/Admin.svelte', match: 'Loading interests…', reason: KNOWN_LOADING_PLACEHOLDER_REASON },
  { file: 'web/src/views/Admin.svelte', match: 'Loading subscribers…', reason: KNOWN_LOADING_PLACEHOLDER_REASON },
  { file: 'web/src/views/Admin.svelte', match: 'Loading…', reason: KNOWN_LOADING_PLACEHOLDER_REASON },
  { file: 'web/src/views/Admin.svelte', match: 'Loading suppressions…', reason: KNOWN_LOADING_PLACEHOLDER_REASON },
  { file: 'web/src/views/Account.svelte', match: 'Loading passkeys…', reason: KNOWN_LOADING_PLACEHOLDER_REASON },
  { file: 'web/src/views/admin/Workshops.svelte', match: 'Loading workshops…', reason: KNOWN_LOADING_PLACEHOLDER_REASON },
  { file: 'web/src/views/admin/WorkshopEditor.svelte', match: 'Loading workshop…', reason: KNOWN_LOADING_PLACEHOLDER_REASON },
  {
    file: 'web/src/views/admin/WorkshopEditor.svelte',
    match: 'Rendering preview…',
    reason: `${KNOWN_LOADING_PLACEHOLDER_REASON} This is WorkshopEditor.svelte's SECOND placeholder -- not named anywhere in issues/0063.md (its enumeration only counted the "Loading…"-worded ones) but structurally identical, discovered by this guard's own development run; converting it means reworking the four-branch previewLoading/previewError/hasPreviewContent/else chain it sits in, out of proportion to #0242/#0243's own scope.`,
  },
  { file: 'web/src/views/admin/CampaignEditor.svelte', match: 'Loading campaign…', reason: KNOWN_LOADING_PLACEHOLDER_REASON },
  {
    file: 'web/src/views/admin/CampaignEditor.svelte',
    match: 'Rendering…',
    reason: `${KNOWN_LOADING_PLACEHOLDER_REASON} CampaignEditor.svelte's SECOND placeholder, same reasoning as WorkshopEditor.svelte's "Rendering preview…" above -- the sole content of a three-branch previewError/preview/else preview-tab chain, and reworking that chain is out of proportion to this pass's scope.`,
  },
  { file: 'web/src/views/admin/Deliverability.svelte', match: 'Loading deliverability data…', reason: KNOWN_LOADING_PLACEHOLDER_REASON },
  { file: 'web/src/views/admin/Deliverability.svelte', match: 'Loading history…', reason: KNOWN_LOADING_PLACEHOLDER_REASON },
  { file: 'web/src/views/admin/Campaigns.svelte', match: 'Loading campaigns…', reason: KNOWN_LOADING_PLACEHOLDER_REASON },
  { file: 'web/src/views/admin/CampaignStats.svelte', match: 'Loading stats…', reason: KNOWN_LOADING_PLACEHOLDER_REASON },
  { file: 'web/src/views/admin/Dashboard.svelte', match: 'Loading overview…', reason: KNOWN_LOADING_PLACEHOLDER_REASON },
  {
    file: 'web/src/views/admin/Dashboard.svelte',
    match: 'Nothing needs attention right now.',
    reason: `${KNOWN_LOADING_PLACEHOLDER_REASON} Dashboard.svelte's SECOND placeholder -- not worded "Loading…" (it's the empty-state copy, the {:else} of {#if warnings.length > 0}) but the identical shape: single-child branch, static unvarying text, reached only from the same async overview fetch as "Loading overview…" above.`,
  },
  { file: 'web/src/views/admin/Pending.svelte', match: 'Loading pending signups…', reason: KNOWN_LOADING_PLACEHOLDER_REASON },
];

/** #0286: an in-branch role="status"/aria-live site with dynamic text, no
 * KNOWN_LOADING_PLACEHOLDERS match, and no focus-swap target -- but whose
 * OWN presence is not what makes its governing branch mount or unmount
 * (the branch is stable/multi-purpose far beyond this one element; this
 * element's own creation is effectively unconditional relative to it).
 * `match` is the element's own RAW SOURCE (Site.rawSource), not its text --
 * `site.text` collapses every {expression} to the literal string '{…}', so
 * two purely-dynamic sites in the same file (e.g. Admin.svelte's
 * settingsNotice and addressNotice, both `{x ?? ''}`-shaped) would collide
 * on it; the raw source differs by variable name, class, etc. and doesn't.
 * Each entry restates, for its own site, the same argument #0242's now-
 * removed single-child unwrap used to infer structurally: see this file's
 * header for the two calibration examples (settingsNotice, audience-count)
 * this restates. */
const KNOWN_STABLE_BRANCH_REASON_TAB_PANEL =
  "Unconditionally rendered itself (not wrapped in its OWN {#if}) inside one of Admin.svelte's per-tab sections -- its governing branch is the whole tab's content (many unrelated fields, forms, and other notices), so switching tabs is what mounts/unmounts it, not anything about this element. Exactly the settingsNotice/audience-count shape #0242's now-removed single-child unwrap used to infer structurally (see this file's header): the branch is stable and multi-purpose far beyond this one notice.";

/** #0299: newSlugInvalid, createSubNotice, and the CSV-import notice used to
 * pass the swap hatch by matching a modal's tabindex="-1"+bind:this target
 * (editInterestModalEl / subscriberDetailModalEl) that sits several
 * conditionals deeper in the SAME tab branch, behind that modal's OWN
 * `{#if editingInterest}`/`{#if viewingSubscriber || ...}` -- an unrelated
 * dialog, opened by an unrelated user action (clicking Edit / View), that
 * happens to be declared somewhere later in the same large tab section. The
 * tightened findFocusTargetVar (see its own doc comment) no longer credits
 * that: these three now rest on the SAME persistence argument as
 * settingsNotice/addressNotice above, which was always the real reason they
 * are correct, not the modal. The CSV-import notice specifically: this is
 * the ground #0286 actually made it correct on (an unconditional, persistent
 * node in a stable branch) -- it previously passed the guard for the wrong
 * reason (a coincidental modal match), which this entry corrects.*/
const KNOWN_STABLE_BRANCH_REASON_TAB_PANEL_0299 = `${KNOWN_STABLE_BRANCH_REASON_TAB_PANEL} Previously mis-credited to an unrelated modal's focus target reachable only by reading past that modal's OWN nested {#if} inside this same tab branch -- #0299 tightened the swap hatch to stop crossing that boundary, surfacing that persistence-within-the-stable-branch (not the modal) was the real, and sufficient, argument all along.`;

/** #0299: saveNotice and unsavedInterestsHint used to pass the swap hatch by
 * matching transitionModalEl, a tabindex="-1"+bind:this target that sits
 * behind its OWN nested `{#if transitionOpen}` -- a status-change dialog,
 * opened by an unrelated user action, several conditionals deeper in the
 * SAME large `{:else if workshop}` branch (the editor's whole main form,
 * which mounts once when the workshop record loads and stays mounted across
 * ordinary editing -- the same branch WorkshopEditor's "Rendering preview…"
 * and previewStale entries above already rest on). Both sites are
 * unconditionally rendered themselves (no OWN {#if}), so what actually makes
 * them sound is that stable, multi-purpose branch, not transitionModalEl's
 * unrelated focus move -- the tightened findFocusTargetVar (see its own doc
 * comment) no longer credits the latter. */
const KNOWN_STABLE_BRANCH_REASON_MAIN_FORM =
  "Unconditionally rendered itself (not wrapped in its OWN {#if}) inside the editor's main form ({:else if workshop}), which mounts once when the workshop record loads and stays mounted across ordinary editing -- the same stable branch this file's \"Rendering preview…\"/previewStale entries above already rest on. Previously mis-credited (#0299) to transitionModalEl, a status-change dialog's own focus target several conditionals deeper behind its OWN {#if transitionOpen} -- an unrelated dialog, not evidence about this branch's own mount. Persistence within the stable main-form branch is the real, sufficient argument.";

const KNOWN_STABLE_BRANCH_SITES: AllowlistEntry[] = [
  {
    file: 'web/src/views/Admin.svelte',
    match: "<p class=\"text-notice\" role=\"status\">{settingsNotice ?? ''}</p>",
    reason: `${KNOWN_STABLE_BRANCH_REASON_TAB_PANEL} This is settingsNotice itself -- #0242's ORIGINAL calibration example for why a naive "branch's own immediate child count" rule was wrong.`,
  },
  {
    file: 'web/src/views/Admin.svelte',
    match: "<p class=\"text-notice\" role=\"status\">{addressNotice ?? ''}</p>",
    reason: KNOWN_STABLE_BRANCH_REASON_TAB_PANEL,
  },
  {
    file: 'web/src/views/Admin.svelte',
    match:
      "<p class=\"text-warn\" role=\"status\">\n              {newSlugInvalid\n                ? 'Lowercase letters, numbers, and single hyphens only (e.g. \"home-automation\").'\n                : ''}\n            </p>",
    reason: `${KNOWN_STABLE_BRANCH_REASON_TAB_PANEL_0299} This is the Interests tab's new-slug validation hint.`,
  },
  {
    file: 'web/src/views/Admin.svelte',
    match: "<p class=\"text-notice\" role=\"status\">{createSubNotice ?? ''}</p>",
    reason: `${KNOWN_STABLE_BRANCH_REASON_TAB_PANEL_0299} This is the Subscribers tab's create-subscriber notice.`,
  },
  {
    file: 'web/src/views/Admin.svelte',
    match: "<p class=\"text-notice\" role=\"status\">{importCommitNoticeText(importCommitResult, importRevokedCount)}</p>",
    reason: `${KNOWN_STABLE_BRANCH_REASON_TAB_PANEL_0299} This is the CSV-import notice #0286 made unconditional -- #0299 criterion 3: it now passes on that persistence ground, not via subscriberDetailModalEl.`,
  },
  {
    file: 'web/src/views/PreferenceCenter.svelte',
    match: "<p class={saveError ? 'text-error' : 'sr-only'} aria-live=\"polite\">{saveError ?? ''}</p>",
    reason:
      "Unconditionally rendered (not wrapped in its own {#if}) inside the 'active' branch of loadState's if/else chain -- a large, stable branch covering the whole preference-management section (topic checkboxes, save button, leave-the-list section), not created/destroyed on account of this one error paragraph. Its own doc comment already argues this at length (#0063).",
  },
  {
    file: 'web/src/views/PreferenceCenter.svelte',
    match: "<p class={saveMessage ? 'text-notice' : 'sr-only'} role=\"status\">{saveMessage ?? ''}</p>",
    reason:
      "Same 'active' branch as saveError immediately above it, same reasoning: unconditional within a large, stable, multi-purpose branch.",
  },
  {
    file: 'web/src/views/PreferenceCenter.svelte',
    match: "<p class={unsubscribeError ? 'text-error' : 'sr-only'} aria-live=\"polite\">{unsubscribeError ?? ''}</p>",
    reason:
      "Same 'active' branch as saveError/saveMessage above (the pref-leave section within it), same reasoning: unconditional within a large, stable, multi-purpose branch, not created/destroyed by this element's own presence.",
  },
  {
    file: 'web/src/views/admin/Pending.svelte',
    match:
      "<p class={resendNoticeByID[row.id] ? 'text-muted' : 'sr-only'} role=\"status\">\n                  {resendNoticeByID[row.id] ?? ''}\n                </p>",
    reason:
      "Unconditionally rendered per row (not wrapped in an inner {#if}) inside a keyed {#each pendingRows as row}'s <td> -- its own comment already states this (#0242/#0243). A brand-new row's copy of this node starts empty/sr-only (nothing to announce yet); it only becomes non-empty later via a genuine already-present-node mutation after a resend action, so a fresh row's insertion never needs to announce anything on its own.",
  },
  {
    file: 'web/src/views/admin/WorkshopEditor.svelte',
    match:
      "<p class=\"text-warn\" role=\"status\">\n                {previewStale\n                  ? \"Showing the last saved version — your edits since then aren't included. Save to update the preview.\"\n                  : ''}\n              </p>",
    reason:
      "Unconditionally rendered (not wrapped in its own {#if previewStale} -- only its TEXT is a ternary) inside the editor's main form, which mounts once when the workshop record loads and stays mounted across ordinary editing. Its own doc comment already argues this at length (#0063): this <p> stays mounted for as long as hasPreviewContent is true rather than being created fresh by an inner {#if previewStale}.",
  },
  {
    file: 'web/src/views/admin/WorkshopEditor.svelte',
    match: "<p class=\"text-notice\" role=\"status\">{saveNotice ?? ''}</p>",
    reason: KNOWN_STABLE_BRANCH_REASON_MAIN_FORM,
  },
  {
    file: 'web/src/views/admin/WorkshopEditor.svelte',
    match: '<p class="text-warn" role="status">{unsavedInterestsHint}</p>',
    reason: KNOWN_STABLE_BRANCH_REASON_MAIN_FORM,
  },
];

// ---------------------------------------------------------------------------
// The guard itself
// ---------------------------------------------------------------------------

interface Violation {
  file: string;
  line: number;
  reason: string;
}

/** #0280 criterion 2: every entry in `list` whose `file` is the one being
 * checked must carry non-empty justification text -- validated at runtime,
 * not merely documented. Scoped to `fileName` so checkFile (called once per
 * scanned file by the real-tree test's own loop) reports each malformed
 * entry once, not once per file in the tree. */
function unjustifiedEntryViolations(list: AllowlistEntry[], fileName: string): Violation[] {
  return list
    .filter((e) => e.file === fileName && e.reason.trim().length === 0)
    .map((e) => ({
      file: e.file,
      line: 0,
      reason: `allowlist entry match=${JSON.stringify(e.match)} carries no justification text -- a non-empty reason is a required field of an entry, not a comment beside it (#0280)`,
    }));
}

/** #0280 criterion 3: an entry that named `fileName` but never matched any
 * real site while that file was scanned is stale -- it must still fail
 * loudly rather than silently doing nothing, the same as before the
 * file:line churn fix, just keyed differently now. `used` is built by the
 * caller as it walks this file's sites/unclassifiable list. */
function staleEntryViolations(list: AllowlistEntry[], fileName: string, used: Set<AllowlistEntry>): Violation[] {
  return list
    .filter((e) => e.file === fileName && !used.has(e))
    .map((e) => ({
      file: e.file,
      line: 0,
      reason: `stale allowlist entry -- no site in ${e.file} matches match=${JSON.stringify(e.match)} (#0280); its recorded justification was: ${e.reason}`,
    }));
}

function findAllowlistEntry(list: AllowlistEntry[], file: string, match: string): AllowlistEntry | undefined {
  return list.find((e) => e.file === file && e.match === match);
}

/** #0298: unjustifiedEntryViolations and staleEntryViolations above both
 * filter on `e.file === fileName`, so each only ever runs for entries whose
 * file the CALLER happens to be scanning at that moment. An entry naming a
 * file the scan never visits at all -- a typo'd path, a deleted/renamed
 * component -- is therefore examined by NEITHER check, for any fileName in
 * the loop: it is not inert by design, it is a hole in coverage. Measured
 * (#0298's Description): an entry with an empty `reason` and a bogus path
 * left the whole suite green.
 *
 * This runs ONCE PER RUN (per allowlist, called after the scan loop
 * completes -- not per file) against `scannedFiles`, the UNION of every
 * file the scan actually visited. An entry whose `file` is not in that set
 * fails here, by name, regardless of its `reason` -- and (criterion 2) an
 * entry that ALSO carries an empty/missing reason gets a SECOND, separate
 * violation for that, so a bogus path can never smuggle in an unjustified
 * entry merely by also being stale in a way nothing was checking.
 *
 * Fails closed (criterion 5, #0275's lesson on the Go side) if
 * `scannedFiles` is empty: an empty set most likely means the scan itself
 * collapsed to zero files (the SOURCE_FILES glob broke, or the caller
 * forgot to build the set), and treating that as "every entry passes" would
 * hide exactly the failure this function exists to catch. */
function unscannedFileViolations(
  list: AllowlistEntry[],
  allowlistName: string,
  scannedFiles: ReadonlySet<string>,
): Violation[] {
  if (scannedFiles.size === 0) {
    return [
      {
        file: '(no files scanned)',
        line: 0,
        reason: `${allowlistName}: the scanned-file set passed to unscannedFileViolations is empty -- cannot validate any entry's file against it, so this fails rather than silently treating every entry as fine (#0298 criterion 5, #0275's lesson)`,
      },
    ];
  }
  const violations: Violation[] = [];
  for (const e of list) {
    if (scannedFiles.has(e.file)) continue;
    violations.push({
      file: e.file,
      line: 0,
      reason: `${allowlistName} entry match=${JSON.stringify(e.match)} names a file the scan never visited (${e.file}) -- an entry naming a nonexistent or unscanned path is examined by neither the justification nor the staleness check (both are keyed on the file being scanned), so it must fail here instead (#0298)`,
    });
    if (e.reason.trim().length === 0) {
      violations.push({
        file: e.file,
        line: 0,
        reason: `${allowlistName} entry match=${JSON.stringify(e.match)} ALSO carries no justification text -- an unscanned file must not become a way to smuggle in an unjustified entry (#0298 criterion 2)`,
      });
    }
  }
  return violations;
}

/** The one real implementation. `scriptAst` is optional (undefined ==
 * "this fixture has no <script>, or its script is irrelevant") so the
 * synthetic fixtures below that don't care about focus-wiring can call
 * `checkFile(name, source)` without extracting it themselves; fixtures that
 * DO need the focus-call check pass it explicitly via `checkFile(name,
 * source, scriptAst)`. The real-tree enumeration test always passes it.
 * `dynamicRoleAllowlist`/`loadingPlaceholderAllowlist` default to the real
 * KNOWN_* sets above but are overridable (#0280 criterion 4) so the
 * synthetic fixtures proving the justification/staleness rules can supply
 * their own small, throwaway entries instead of mutating the real ones. */
function checkFile(
  fileName: string,
  source: string,
  scriptAst?: SvelteNode,
  dynamicRoleAllowlist: AllowlistEntry[] = KNOWN_DYNAMIC_ROLE_SITES,
  loadingPlaceholderAllowlist: AllowlistEntry[] = KNOWN_LOADING_PLACEHOLDERS,
  stableBranchAllowlist: AllowlistEntry[] = KNOWN_STABLE_BRANCH_SITES,
): { violations: Violation[]; statusSites: Site[]; alertSites: Site[]; loadingPlaceholders: Site[] } {
  const { sites, unclassifiable } = collectSites(fileName, source);
  const violations: Violation[] = [];

  violations.push(...unjustifiedEntryViolations(dynamicRoleAllowlist, fileName));
  violations.push(...unjustifiedEntryViolations(loadingPlaceholderAllowlist, fileName));
  violations.push(...unjustifiedEntryViolations(stableBranchAllowlist, fileName));

  const usedDynamicRoleEntries = new Set<AllowlistEntry>();
  const usedLoadingPlaceholderEntries = new Set<AllowlistEntry>();
  const usedStableBranchEntries = new Set<AllowlistEntry>();

  for (const u of unclassifiable) {
    const entry = findAllowlistEntry(dynamicRoleAllowlist, u.file, u.matchKey);
    if (entry) {
      usedDynamicRoleEntries.add(entry);
    } else {
      violations.push({ file: u.file, line: u.line, reason: `UNCLASSIFIABLE: ${u.reason}` });
    }
  }

  const statusSites: Site[] = [];
  const alertSites: Site[] = [];
  const loadingPlaceholders: Site[] = [];

  const focusedVars = new Set<string>();
  if (scriptAst) collectFocusedVars(scriptAst, focusedVars);

  for (const site of sites) {
    if (site.role === 'alert') {
      alertSites.push(site);
      if (site.text.length === 0) {
        violations.push({
          file: site.file,
          line: site.line,
          reason: 'role="alert" region has no content (no Text or expression children) -- nothing would be announced on insertion either',
        });
      }
      continue;
    }

    // role === 'status'. #0286: EVERY in-branch site runs the same checks
    // below, not just the ones whose branch used to unwrap to a single
    // purpose -- see this file's header for why that gate is gone. A site
    // with no governing branch at all (unconditional) is the sound
    // "persistent node" case and needs no check.
    if (site.governingBranch === undefined) {
      statusSites.push(site);
      continue;
    }

    if (site.isStaticOnly) {
      const entry = findAllowlistEntry(loadingPlaceholderAllowlist, site.file, site.text);
      if (entry) {
        usedLoadingPlaceholderEntries.add(entry);
        loadingPlaceholders.push(site);
        continue;
      }
    }

    const focusVar = findFocusTargetVar(site.governingBranch);
    if (focusVar !== undefined && focusedVars.has(focusVar)) {
      statusSites.push(site); // legitimate whole-panel swap
      continue;
    }

    const stableBranchEntry = findAllowlistEntry(stableBranchAllowlist, site.file, site.rawSource);
    if (stableBranchEntry) {
      usedStableBranchEntries.add(stableBranchEntry);
      statusSites.push(site); // named, justified: own presence doesn't govern the branch
      continue;
    }

    violations.push({
      file: site.file,
      line: site.line,
      reason: `role="status" element sits inside an {#if}/{:each} branch${
        site.isStaticOnly ? ' -- not a KNOWN_LOADING_PLACEHOLDER, and' : ', with dynamic text --'
      } no sibling element in the same branch carries tabindex="-1" + bind:this with a matching .focus() call in <script> (the whole-panel-swap alternative), and it is not named in KNOWN_STABLE_BRANCH_SITES`,
    });
  }

  violations.push(...staleEntryViolations(dynamicRoleAllowlist, fileName, usedDynamicRoleEntries));
  violations.push(...staleEntryViolations(loadingPlaceholderAllowlist, fileName, usedLoadingPlaceholderEntries));
  violations.push(...staleEntryViolations(stableBranchAllowlist, fileName, usedStableBranchEntries));

  return { violations, statusSites, alertSites, loadingPlaceholders };
}

describe('live-region structural guard (#0242, #0243): role="status" persists or swaps focus; role="alert" is enumerated and always has content', () => {
  it('every classifiable role="status"/aria-live site in web/src (not just web/src/views) is either persistent, a documented loading placeholder, or a wired whole-panel-swap focus target -- and every role="alert" site has real content', () => {
    const allViolations: Violation[] = [];
    const statusSites: Array<Site & { file: string }> = [];
    const alertSites: Array<Site & { file: string }> = [];
    const loadingPlaceholders: Array<Site & { file: string }> = [];
    let filesScanned = 0;
    const scannedFiles = new Set<string>();

    for (const [globKey, source] of Object.entries(SOURCE_FILES)) {
      if (globKey.endsWith('.test.ts')) continue;
      filesScanned++;
      const file = toRepoRelativePath(globKey);
      scannedFiles.add(file);

      // Parsed once here (rather than inside checkFile) so the <script> AST
      // is available for the focus-call check -- checkFile's own re-parse of
      // the template (inside collectSites) is intentionally kept separate
      // and cheap; only the outer <script> half is threaded through.
      const ast = parseSvelte(source, { filename: file, modern: true }) as unknown as SvelteNode;
      const scriptAst = (ast.instance as SvelteNode | undefined)?.content as SvelteNode | undefined;

      const result = checkFile(file, source, scriptAst);
      allViolations.push(...result.violations);
      statusSites.push(...result.statusSites);
      alertSites.push(...result.alertSites);
      loadingPlaceholders.push(...result.loadingPlaceholders);
    }

    // #0298: once per run (not once per file, unlike unjustifiedEntryViolations
    // and staleEntryViolations above, which only ever run for entries whose
    // file happens to be the one currently being scanned) -- covers every
    // allowlist the guard carries, per criterion 3.
    allViolations.push(...unscannedFileViolations(KNOWN_DYNAMIC_ROLE_SITES, 'KNOWN_DYNAMIC_ROLE_SITES', scannedFiles));
    allViolations.push(...unscannedFileViolations(KNOWN_LOADING_PLACEHOLDERS, 'KNOWN_LOADING_PLACEHOLDERS', scannedFiles));
    allViolations.push(...unscannedFileViolations(KNOWN_STABLE_BRANCH_SITES, 'KNOWN_STABLE_BRANCH_SITES', scannedFiles));

    if (allViolations.length > 0) {
      const detail = allViolations.map((v) => `  ${v.file}:${v.line}: ${v.reason}`).join('\n');
      throw new Error(`live-region violation(s) found (#0242, #0243):\n${detail}`);
    }
    expect(allViolations).toHaveLength(0);

    // #0242 review (eba2de9): criterion 4 requires the scan run over the
    // whole tree, not a hand-maintained (or accidentally re-narrowed) file
    // list. A COUNT floor on filesScanned makes a future narrowing of the
    // glob (back to '../views/**/*.svelte', or anything else that drops
    // App.svelte or web/src/lib's twelve components) fail loudly here
    // instead of silently passing with fewer files scanned -- the same
    // failure mode criterion 4 was written to close. 38 is the real count
    // of every .svelte file in web/src as of this pass (`find web/src -name
    // '*.svelte' | wc -l`); 35 leaves headroom for a handful of new files
    // without the floor itself needing to move on every unrelated commit.
    expect(filesScanned).toBeGreaterThanOrEqual(35);

    // Enumeration floors (#0243 criterion 1: "derived from the tree"), not
    // magic totals -- if any of these drops, either a site was converted
    // (update the count/allowlist deliberately) or the scan itself broke.
    // Re-derived 2026-08-27 (#0279), by running this exact test with a
    // temporary throw reporting the live counts, both BEFORE and AFTER
    // #0279's change (each measured by swapping Dashboard.svelte, and this
    // file's own KNOWN_DYNAMIC_ROLE_SITES entry, back to their prior
    // committed content -- git-hash-verified restored afterwards, per
    // CLAUDE.md §8a). BEFORE (i.e. the tree as #0242's review last measured
    // it, PLUS whatever unrelated work landed since -- #0244, and the
    // #0233/#0270/#0274 session): alertSites 60, statusSites 31 -- already
    // 4 and 1 higher than the "56"/"30" this comment previously recorded,
    // confirming these are living floors that drift upward with ordinary
    // work, not fixed totals. AFTER #0279 (Dashboard.svelte's per-warning
    // `role={w.alert ? 'alert' : 'status'}` split into a static
    // role="alert" branch -- +1 alertSites -- plus a new persistent
    // role="status" announcer outside the {#each} -- +1 statusSites):
    // alertSites 61, statusSites 32. loadingPlaceholders 20, unchanged by
    // #0279 (Dashboard.svelte's two existing placeholders are untouched).
    expect(alertSites.length).toBeGreaterThanOrEqual(61);
    expect(statusSites.length).toBeGreaterThanOrEqual(32);
    expect(loadingPlaceholders.length).toBeGreaterThanOrEqual(20);
  });
});

describe('checkFile (synthetic fixtures)', () => {
  it('passes a persistent role="status" node (unconditional, mutating text)', () => {
    const src = `<p class="text-notice" role="status">{msg ?? ''}</p>`;
    const { violations, statusSites } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(0);
    expect(statusSites).toHaveLength(1);
  });

  it('flags a role="status" node created fresh by an {#if}, with dynamic text and no swap target', () => {
    const src = `{#if loadState === 'error'}<p role="status">{errorMessage}</p>{/if}`;
    const { violations } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(1);
    expect(violations[0].reason).toContain('sits inside an {#if}/{:each} branch');
  });

  // #0286 criterion 4: proves the guard NOW catches the shape it used to
  // wave through unconditionally -- a role="status" site inside a branch
  // with multiple significant siblings (a fork), which used to bypass
  // every check (the "disclosed boundary" this file's header describes).
  // This is the PreferenceCenter.svelte item-1 shape #0244 fixed and
  // #0242's own guard would not have caught (its own Description).
  it('#0286: flags a role="status" node in a forked branch (multiple significant siblings) that has neither a swap target nor a KNOWN_STABLE_BRANCH_SITES entry', () => {
    const src = `{#if x}<p>other</p>{:else}
      <div class="row-a"></div>
      <p role="status">{notice ?? ''}</p>
      <div class="row-b"></div>
    {/if}`;
    const { violations, statusSites } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(1);
    expect(violations[0].reason).toContain('not named in KNOWN_STABLE_BRANCH_SITES');
    expect(statusSites).toHaveLength(0);
  });

  // Same shape, but now WITH a stable-branch allowlist entry naming this
  // exact site's raw source -- proves the escape hatch itself works, and
  // (CLAUDE.md §8) that its oracle isn't just "any non-empty entry": a
  // stale/mismatched match still fails (proved separately, #0280-style,
  // below), matching the settingsNotice/audience-count shape #0242's now-
  // removed single-child unwrap used to infer structurally instead.
  it('#0286: does not flag the same forked-branch role="status" node once it is named in KNOWN_STABLE_BRANCH_SITES', () => {
    const src = `{#if x}<p>other</p>{:else}
      <div class="row-a"></div>
      <p role="status">{notice ?? ''}</p>
      <div class="row-b"></div>
    {/if}`;
    const stable: AllowlistEntry[] = [
      { file: 'fixture.svelte', match: `<p role="status">{notice ?? ''}</p>`, reason: 'settingsNotice/audience-count-shaped: this element is not what makes the {:else} branch mount or unmount.' },
    ];
    const { violations, statusSites } = checkFile('fixture.svelte', src, undefined, undefined, undefined, stable);
    expect(violations).toHaveLength(0);
    expect(statusSites).toHaveLength(1);
  });

  it('recognizes a self-bound whole-panel-swap notice (the Login.svelte registerSentNotice shape)', () => {
    const src = `<script>
  let el = $state(null);
  async function go() {
    await tick();
    el?.focus();
  }
</script>
{#if sent}
  <p role="status" tabindex="-1" bind:this={el}>Done</p>
{/if}`;
    const ast = parseSvelte(src, { filename: 'fixture.svelte', modern: true }) as unknown as SvelteNode;
    const scriptAst = (ast.instance as SvelteNode | undefined)?.content as SvelteNode | undefined;
    const { violations, statusSites } = checkFile('fixture.svelte', src, scriptAst);
    expect(violations).toHaveLength(0);
    expect(statusSites).toHaveLength(1);
  });

  it('recognizes a sibling whole-panel-swap notice (the Unsubscribe.svelte doneMessage shape -- focus target is a SIBLING, not an ancestor)', () => {
    const src = `<script>
  async function onConfirm() {
    status = 'done';
    await tick();
    doneHeading?.focus();
  }
</script>
{#if status === 'done'}
  <h1 tabindex="-1" bind:this={doneHeading}>Done</h1>
  <p role="status">{doneMessage}</p>
{/if}`;
    const ast = parseSvelte(src, { filename: 'fixture.svelte', modern: true }) as unknown as SvelteNode;
    const scriptAst = (ast.instance as SvelteNode | undefined)?.content as SvelteNode | undefined;
    const { violations, statusSites } = checkFile('fixture.svelte', src, scriptAst);
    expect(violations).toHaveLength(0);
    expect(statusSites).toHaveLength(1);
  });

  // #0299 criterion 5: proves the tightened rule fires against the shape it
  // was written to close -- a status site whose ONLY nearby focus move is a
  // target several conditionals deeper in the same outer branch (a modal
  // gated behind its own {#if showModal}), not in the site's OWN governing
  // branch. This is exactly the Admin.svelte/WorkshopEditor.svelte shape
  // #0299's Description measured: 5 sites passing via an unrelated modal's
  // .focus() call. Before this change, findFocusTargetVar found `modalEl`
  // anywhere in the outer branch and this fixture passed; now it must not.
  it('#0299: flags a status site whose only nearby focus target is behind a NESTED {#if} (an unrelated modal), not in its own governing branch', () => {
    const src = `<script>
  let modalEl = $state(null);
  function openModal() {
    showModal = true;
    tick().then(() => modalEl?.focus());
  }
</script>
{#if section === 'tab'}
  <p class="text-notice" role="status">{notice ?? ''}</p>
  <button onclick={openModal}>Open</button>
  {#if showModal}
    <div class="modal" tabindex="-1" bind:this={modalEl}>...</div>
  {/if}
{/if}`;
    const ast = parseSvelte(src, { filename: 'fixture.svelte', modern: true }) as unknown as SvelteNode;
    const scriptAst = (ast.instance as SvelteNode | undefined)?.content as SvelteNode | undefined;
    const { violations, statusSites } = checkFile('fixture.svelte', src, scriptAst);
    expect(violations).toHaveLength(1);
    expect(violations[0].reason).toContain('not named in KNOWN_STABLE_BRANCH_SITES');
    expect(statusSites).toHaveLength(0);
  });

  // Same shape, but the target is a DIRECT sibling in the SAME governing
  // branch (no intervening {#if}) -- must still pass. Guards against an
  // overcorrection that would also reject the legitimate sibling/self-bound
  // shapes the two tests immediately below (and above) already cover.
  it('#0299: still recognizes a swap target that is a direct sibling in the SAME governing branch, no nested {#if} in between', () => {
    const src = `<script>
  let panelEl = $state(null);
  function onEnter() {
    tick().then(() => panelEl?.focus());
  }
</script>
{#if section === 'tab'}
  <h1 tabindex="-1" bind:this={panelEl}>Tab</h1>
  <p class="text-notice" role="status">{notice ?? ''}</p>
{/if}`;
    const ast = parseSvelte(src, { filename: 'fixture.svelte', modern: true }) as unknown as SvelteNode;
    const scriptAst = (ast.instance as SvelteNode | undefined)?.content as SvelteNode | undefined;
    const { violations, statusSites } = checkFile('fixture.svelte', src, scriptAst);
    expect(violations).toHaveLength(0);
    expect(statusSites).toHaveLength(1);
  });

  it('still flags a claimed swap when the focus target exists but nothing in <script> ever calls .focus() on it (vacuous binding)', () => {
    const src = `<script>
  // el is bound but never focused anywhere
</script>
{#if sent}
  <p role="status" tabindex="-1" bind:this={el}>Done</p>
{/if}`;
    const ast = parseSvelte(src, { filename: 'fixture.svelte', modern: true }) as unknown as SvelteNode;
    const scriptAst = (ast.instance as SvelteNode | undefined)?.content as SvelteNode | undefined;
    const { violations } = checkFile('fixture.svelte', src, scriptAst);
    expect(violations).toHaveLength(1);
  });

  it('treats an implicit aria-live="polite" (no role) the same as role="status"', () => {
    const src = `{#if x}<p aria-live="polite">{msg}</p>{/if}`;
    const { violations } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(1);
  });

  it('ignores aria-live="off" entirely -- not a live region at all', () => {
    const src = `{#if x}<span aria-live="off">Saving…</span>{/if}`;
    const { violations, statusSites, alertSites } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(0);
    expect(statusSites).toHaveLength(0);
    expect(alertSites).toHaveLength(0);
  });

  it('never flags role="alert" for being created by an {#if} -- #0243\'s decision', () => {
    const src = `{#if err}<p role="alert">{err}</p>{/if}`;
    const { violations, alertSites } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(0);
    expect(alertSites).toHaveLength(1);
  });

  it('flags an EMPTY role="alert" region -- content-free either way', () => {
    const src = `{#if err}<p role="alert"></p>{/if}`;
    const { violations } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(1);
    expect(violations[0].reason).toContain('no content');
  });

  it('flags a non-string role attribute as unclassifiable rather than silently skipping it', () => {
    const src = `{#each items as it}<li role={it.alert ? 'alert' : 'status'}>{it.message}</li>{/each}`;
    const { violations } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(1);
    expect(violations[0].reason).toContain('UNCLASSIFIABLE');
  });

  it('flags a non-string aria-live attribute as unclassifiable', () => {
    const src = `{#if x}<p aria-live={mode}>{msg}</p>{/if}`;
    const { violations } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(1);
    expect(violations[0].reason).toContain('UNCLASSIFIABLE');
  });

  // #0280 criterion 4: both new allowlist rules proven with their own
  // throwaway entries, distinct bytes from checkFile's own violation-message
  // template (CLAUDE.md §8) -- these fixtures assert on independently
  // written expectation text, not a copy of the string checkFile emits.
  it('#0280: fails a dynamic-role allowlist entry that carries no justification text, even though it matches a real site', () => {
    const src = `{#each items as it}<li role={it.alert ? 'alert' : 'status'}>{it.message}</li>{/each}`;
    const unjustified: AllowlistEntry[] = [
      { file: 'fixture.svelte', match: "role={it.alert ? 'alert' : 'status'}", reason: '   ' },
    ];
    const { violations } = checkFile('fixture.svelte', src, undefined, unjustified, []);
    expect(violations.some((v) => v.reason.includes('no justification text'))).toBe(true);
    // The entry DID match the real site -- this must be the justification
    // rule firing, not a fallback "unclassifiable, no matching entry" report.
    expect(violations.some((v) => v.reason.startsWith('UNCLASSIFIABLE'))).toBe(false);
  });

  it('#0280: fails a loading-placeholder allowlist entry that carries no justification text', () => {
    const src = `{#if loading}<p role="status">Loading…</p>{/if}`;
    const unjustified: AllowlistEntry[] = [{ file: 'fixture.svelte', match: 'Loading…', reason: '' }];
    const { violations } = checkFile('fixture.svelte', src, undefined, [], unjustified);
    expect(violations.some((v) => v.reason.includes('no justification text'))).toBe(true);
  });

  it('#0280: fails a stale dynamic-role entry whose match no longer corresponds to any site in the file', () => {
    const src = `<p role="status">{msg}</p>`; // no dynamic-role attribute anywhere
    const stale: AllowlistEntry[] = [
      { file: 'fixture.svelte', match: "role={x ? 'alert' : 'status'}", reason: 'a real, non-empty reason' },
    ];
    const { violations } = checkFile('fixture.svelte', src, undefined, stale, []);
    expect(violations.some((v) => v.reason.includes('stale allowlist entry'))).toBe(true);
  });

  it('#0280: fails a stale loading-placeholder entry whose text no longer appears in the file', () => {
    const src = `{#if loading}<p role="status">Loading…</p>{/if}`;
    const stale: AllowlistEntry[] = [
      { file: 'fixture.svelte', match: 'This text does not appear anywhere above', reason: 'a real, non-empty reason' },
    ];
    const { violations } = checkFile('fixture.svelte', src, undefined, [], stale);
    // The real "Loading…" site is unnamed and still fails on its own merits
    // (no matching entry); the STALE entry must ALSO be reported separately.
    expect(violations.some((v) => v.reason.includes('stale allowlist entry'))).toBe(true);
  });

  // #0298: unscannedFileViolations proven directly, distinct from checkFile's
  // own per-file checks -- this is a once-per-run, cross-file pass, so it
  // cannot be exercised through checkFile (which only ever sees one file at
  // a time). Assertions read for independently written substrings (CLAUDE.md
  // §8: oracle != subject), not a copy of the message the function emits.
  it('#0298: fails an allowlist entry naming a file the scan never visited, even with a bogus match and an empty reason', () => {
    const scanned = new Set(['web/src/views/Real.svelte']);
    const bogus: AllowlistEntry[] = [
      { file: 'web/src/views/DoesNotExistAnywhere.svelte', match: 'nothing', reason: '' },
    ];
    const violations = unscannedFileViolations(bogus, 'TEST_ALLOWLIST', scanned);
    // Criterion 1: the unscanned-file rule fires, naming the entry and path.
    expect(violations.some((v) => v.reason.includes('never visited') && v.reason.includes('DoesNotExistAnywhere.svelte'))).toBe(
      true,
    );
    // Criterion 2: the empty-reason rule ALSO fires in the same pass -- a
    // bogus path is not a way to dodge the justification requirement.
    expect(violations.some((v) => v.reason.includes('justification'))).toBe(true);
  });

  it('#0298: an entry naming a scanned file, with a real reason, produces no violation', () => {
    const scanned = new Set(['web/src/views/Real.svelte']);
    const ok: AllowlistEntry[] = [{ file: 'web/src/views/Real.svelte', match: 'x', reason: 'a real, non-empty reason' }];
    expect(unscannedFileViolations(ok, 'TEST_ALLOWLIST', scanned)).toHaveLength(0);
  });

  it('#0298 criterion 5: fails closed (rather than passing everything) when the scanned-file set is empty', () => {
    const violations = unscannedFileViolations(KNOWN_LOADING_PLACEHOLDERS, 'KNOWN_LOADING_PLACEHOLDERS', new Set());
    expect(violations.length).toBeGreaterThan(0);
    expect(violations[0].reason).toContain('empty');
  });

  // #0298 criterion 4: every entry in every real allowlist names a file the
  // real scan actually visits -- proven against the ACTUAL scanned-file set
  // (scannedFileSet(), the same computation the real-tree test uses), not a
  // hand-typed stand-in that could drift from it.
  it('#0298 criterion 4: every real allowlist entry names a file the real scan visits', () => {
    const scanned = scannedFileSet();
    expect(scanned.size).toBeGreaterThanOrEqual(35); // sanity: didn't just get an empty set
    expect(unscannedFileViolations(KNOWN_DYNAMIC_ROLE_SITES, 'KNOWN_DYNAMIC_ROLE_SITES', scanned)).toHaveLength(0);
    expect(unscannedFileViolations(KNOWN_LOADING_PLACEHOLDERS, 'KNOWN_LOADING_PLACEHOLDERS', scanned)).toHaveLength(0);
    expect(unscannedFileViolations(KNOWN_STABLE_BRANCH_SITES, 'KNOWN_STABLE_BRANCH_SITES', scanned)).toHaveLength(0);
  });
});

