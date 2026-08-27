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
// ## The role="status" rule (#0242)
//
// A role="status" (or an explicit aria-live="polite"/"assertive" with no
// role="alert") element is a violation if it is BOTH:
//
//   1. "governing-branch dynamic" (isGoverningBranchDynamic): the NEAREST
//      enclosing {#if}/{:else}/{#each} branch exists, and UNWRAPPING it
//      through a chain of containers that each have exactly ONE significant
//      child (whitespace-only Text and HTML comments don't count) leads all
//      the way down to the element itself, with no fork along the way --
//      i.e. the branch's entire reason for existing IS this one node (or a
//      chain of single-purpose containers around only it, e.g. the
//      demoted-banner shape this issue's implementation pass converted:
//      `{#if demoted}<div role="status">...</div>{/if}`, where the branch
//      IS the div, so unwrapping stops immediately).
//
//      The unwrap step is NOT optional. Every branch of a multi-state view
//      like PreferenceCenter.svelte's loadState if/else chain wraps its
//      ENTIRE content in one `<div class="...-content">`, so a NAIVE "does
//      the branch's own immediate child count equal 1" check is true for
//      EVERY branch regardless of size -- it would misclassify
//      `settingsNotice` (nested three container-levels inside a large,
//      stable `{:else}`) and CampaignEditor's `audience-count` (nested
//      inside a `<Panel>` inside `{:else if campaign}`, which renders the
//      whole rest of the editor) as governed by that huge branch, when
//      neither notice's OWN presence is what makes it mount or unmount.
//      Unwrapping single-child containers FIRST, and stopping the instant a
//      level has more than one significant child (a fork -- these are the
//      "counterexamples in the tree" #0242's title names) or zero, finds
//      the actual smallest branch whose sole purpose is this one element.
//      #0063's own fix commits already established the "unconditionally
//      rendered" characterization for exactly the sites this correctly
//      excludes. This is a real, disclosed approximation, not a proof: a
//      two-sibling branch that toggles in lockstep with the notice would
//      still slip past it. None exist in the tree today (verified by
//      inspecting every violation this guard's development run produced
//      before landing, including working through two prior, broken versions
//      of this exact rule); a future one would not be caught by this rule
//      alone.
//
//      ## Disclosed boundary (#0242 review, eba2de9): the fork case gets NO
//      check at all, not merely an approximate one
//
//      The above is understated by calling this "a real, disclosed
//      approximation" -- the review measured the actual size of the gap and
//      it is not a narrow edge case. isGoverningBranchDynamic examines a
//      role="status" site AT ALL only when unwrapping its governing branch
//      reaches the element itself with no fork along the way. The instant a
//      branch forks (more than one significant child anywhere on the
//      unwrap path -- e.g. a heading ALONGSIDE the status paragraph, not
//      wrapping it), this function returns false and the site is pushed
//      straight into `statusSites` as if it were unconditionally rendered:
//      NO persistence check, NO loading-placeholder classification, and NO
//      focus-swap-target check. On the tree as of this pass that is 24 of
//      the 47 status/aria-live sites that sit inside a branch at all --
//      measured by the review, not estimated. This concrete shape passes
//      today with zero scrutiny:
//
//        {#if err}
//          <div class="wrap"><h2>Oops</h2><p role="status">{err}</p></div>
//        {/if}
//
//      That is criterion 1's literal subject (a role="status" node created
//      and destroyed by an {#if}, dynamic text, no focus target) and it is
//      the same gap #0244's own item 1 turned out to be
//      (PreferenceCenter.svelte's two-child `{#if
//      showSubscribeAgainAffordance}` branch had no focus management, and
//      this guard would not have found it either). Judged, on the #0242
//      re-implementation pass that added this paragraph, to be out of
//      proportion to a bounce that was solely about criterion 4's glob
//      scope: closing it means extending isGoverningBranchDynamic to
//      require a focus-swap target for ANY in-branch status site (not just
//      the single-child-unwrap case) and building a
//      KNOWN_LOADING_PLACEHOLDERS-shaped named allowlist for whichever of
//      the 24 are genuinely stable, multi-purpose branches -- auditing 24
//      real sites across several files is comparable in size to this
//      guard's own original construction, not a follow-on fix. Left
//      open and reported for its own issue rather than folded in here.
//
//   2. NOT a legitimate whole-panel swap: scanning that SAME governing
//      branch's entire subtree (not just ancestors of the status element --
//      Unsubscribe.svelte's `{doneMessage}` status paragraph is a SIBLING
//      of the `<h1 tabindex="-1" bind:this={doneHeading}>` that actually
//      receives focus, inside the same `{:else}` branch, not an ancestor of
//      it) for an element carrying BOTH `tabindex="-1"` and `bind:this={V}`,
//      and the component's <script> for a `V.focus()` / `V?.focus()` call
//      (zero arguments) anywhere -- not required to sit inside `$effect`,
//      unlike modalFocusWiring's stricter check, because this codebase's
//      real sites (Login.svelte, Unsubscribe.svelte, PreferenceCenter.svelte)
//      call it from a plain `await tick(); V?.focus();` inside an async
//      handler, not a reactive effect. This is the "legitimate alternative"
//      #0242's criterion 2 asks the guard to distinguish rather than flag --
//      a whole-panel swap that moves focus somewhere in its own branch
//      instead of trying to make an inserted role="status" node announce
//      itself.
//
// A role="status"/aria-live element whose governing branch has exactly one
// significant child, has NO swap target, but is STATICALLY-worded (no
// {expression} children at all -- the text never varies) is classified
// separately as a LOADING PLACEHOLDER rather than a violation: #0063's own
// fix pass explicitly enumerated 17 (at the time of writing; see
// pageTitle.ts's sibling comment on drift and WorkshopEditor.svelte:636 for
// today's count) of exactly this shape -- `{#if loading}<p role="status">
// Loading…</p>{/if}` -- and reported them as a deliberate, smaller-severity
// remainder rather than converting them ("they announce an initial-load
// state rather than the result of a user action"). This guard's job is to
// hold that decision in code instead of prose (#0242's own Description
// makes exactly this argument about "a console-wide decision" living only
// in comments), not to silently relitigate it -- but a STATIC-worded
// element inside a single-child branch is categorically the shape #0063
// already reviewed and named, so it is counted (KNOWN_LOADING_PLACEHOLDER
// floor below) and reported, not silently passed.
//
// ## "Cannot classify" (#0242 criterion 5, #0243's dynamic-role finding)
//
// Two sites in the tree carry a NON-STATIC role or aria-live attribute
// value (a `role={...}` or `aria-live={...}` expression, not a plain string)
// -- this guard cannot statically prove what role such a site resolves to
// at runtime, so it refuses to guess. Any such site fails loudly, by name,
// rather than being silently skipped by the (necessarily string-matching)
// classifier below. See KNOWN_DYNAMIC_ROLE_SITES for the one currently in
// the tree (Dashboard.svelte's per-warning list item) and why it is named
// rather than fixed here.
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

/**
 * True iff `branch` (the target's nearest enclosing {#if}/{:else}/{#each}
 * fragment, or undefined for "unconditional") exists is "single-purpose":
 * unwrapping `branch` through a chain of container elements that each have
 * EXACTLY ONE significant child leads all the way down to `target` itself,
 * with no fork (a level with >1 significant children, or 0) along the way.
 *
 * This is NOT the same as "branch's own top-level child count === 1" --
 * that naive version breaks on real markup, because every branch of
 * PreferenceCenter.svelte's loadState if/else chain (and most of this
 * project's other multi-state views) wraps its ENTIRE content in one
 * `<div class="...-content">`, so the branch's OWN immediate child count is
 * always 1 regardless of how much unrelated content that div holds --
 * `settingsNotice`-shaped sites nested three levels deeper inside would be
 * wrongly flagged. Unwrapping single-child containers first (stopping the
 * instant we hit `target` itself, or a fork) finds the actual smallest
 * conditional whose entire purpose is this one element -- see the two
 * calibration cases in this file's header comment (demoted-banner: branch
 * IS `target`, stops immediately, dynamic; settingsNotice: branch unwraps
 * through div.pref-content-like wrappers into a 3+-child fork before
 * reaching the target, not dynamic).
 */
function isGoverningBranchDynamic(target: SvelteNode, branch: SvelteNode | undefined): boolean {
  if (!branch) return false;
  let current: SvelteNode | undefined = branch;
  const seen = new Set<SvelteNode>();
  while (current) {
    if (seen.has(current)) return false; // defensive: never loop forever
    seen.add(current);
    const kids = significantNodes(current);
    if (kids.length !== 1) return false;
    const only = kids[0];
    if (only === target) return true;
    const childFragment = only.fragment as SvelteNode | undefined;
    if (!childFragment || !Array.isArray(childFragment.nodes)) return false;
    current = childFragment;
  }
  return false;
}

interface Site {
  file: string;
  line: number;
  role: 'status' | 'alert';
  text: string; // best-effort human-readable label for reporting/dedup
  isStaticOnly: boolean; // no ExpressionTag children at all
  governingBranch: SvelteNode | undefined; // the branch fragment, if any
  node: SvelteNode; // the element itself -- needed by isGoverningBranchDynamic
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
          sites.push({ file: fileName, line, role: kind, text, isStaticOnly, governingBranch, node: obj });
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
 * ancestors of the status element itself (Unsubscribe.svelte's shape). */
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
  {
    file: 'web/src/views/admin/Dashboard.svelte',
    match: "role={w.alert ? 'alert' : 'status'}",
    reason:
      "Dashboard.svelte's per-warning list item toggles between the two roles this decision governs, inside a KEYED {#each} -- Svelte reuses the same DOM node for an existing key across a re-render (mutating its role/text in place), but a warning appearing for the FIRST time is a genuine insertion. For 'alert' that's the sound, relied-upon case (#0243's decision); for 'status' it is the same initial-appearance gap the loading placeholders already carry. Reported here rather than redesigned, matching #0063's own treatment of that shape; #0279 fixes this properly and empties this set.",
  },
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
): { violations: Violation[]; statusSites: Site[]; alertSites: Site[]; loadingPlaceholders: Site[] } {
  const { sites, unclassifiable } = collectSites(fileName, source);
  const violations: Violation[] = [];

  violations.push(...unjustifiedEntryViolations(dynamicRoleAllowlist, fileName));
  violations.push(...unjustifiedEntryViolations(loadingPlaceholderAllowlist, fileName));

  const usedDynamicRoleEntries = new Set<AllowlistEntry>();
  const usedLoadingPlaceholderEntries = new Set<AllowlistEntry>();

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

    // role === 'status'
    const governingDynamic = isGoverningBranchDynamic(site.node, site.governingBranch);

    if (!governingDynamic) {
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

    violations.push({
      file: site.file,
      line: site.line,
      reason: `role="status" element is the sole content of an {#if}/{:each} branch (created and destroyed with it)${
        site.isStaticOnly ? ' -- not a KNOWN_LOADING_PLACEHOLDER, and' : ', with dynamic text --'
      } no sibling element in the same branch carries tabindex="-1" + bind:this with a matching .focus() call in <script> (the whole-panel-swap alternative)`,
    });
  }

  violations.push(...staleEntryViolations(dynamicRoleAllowlist, fileName, usedDynamicRoleEntries));
  violations.push(...staleEntryViolations(loadingPlaceholderAllowlist, fileName, usedLoadingPlaceholderEntries));

  return { violations, statusSites, alertSites, loadingPlaceholders };
}

describe('live-region structural guard (#0242, #0243): role="status" persists or swaps focus; role="alert" is enumerated and always has content', () => {
  it('every classifiable role="status"/aria-live site in web/src (not just web/src/views) is either persistent, a documented loading placeholder, or a wired whole-panel-swap focus target -- and every role="alert" site has real content', () => {
    const allViolations: Violation[] = [];
    const statusSites: Array<Site & { file: string }> = [];
    const alertSites: Array<Site & { file: string }> = [];
    const loadingPlaceholders: Array<Site & { file: string }> = [];
    let filesScanned = 0;

    for (const [globKey, source] of Object.entries(SOURCE_FILES)) {
      if (globKey.endsWith('.test.ts')) continue;
      filesScanned++;
      const file = toRepoRelativePath(globKey);

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
    // Re-derived 2026-08-27 (#0242 review) against the WIDENED scan (whole
    // of web/src, not just web/src/views): alertSites 56 (unchanged --
    // no role="alert" sites exist outside web/src/views), statusSites 30
    // (was 29 over views only; SubscribeForm.svelte's persistent
    // aria-live="polite" error region -- this issue's own motivating
    // example, previously invisible to this guard -- adds the 30th),
    // loadingPlaceholders 20 (unchanged -- no loading-placeholder shape
    // exists outside web/src/views either).
    expect(alertSites.length).toBeGreaterThanOrEqual(56);
    expect(statusSites.length).toBeGreaterThanOrEqual(30);
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
    expect(violations[0].reason).toContain('created and destroyed');
  });

  it('does not flag a role="status" node in a branch with multiple significant siblings (the settingsNotice/audience-count shape)', () => {
    const src = `{:else}
      <div class="row-a"></div>
      <p role="status">{notice ?? ''}</p>
      <div class="row-b"></div>
    {/if}`;
    // {:else} alone isn't valid Svelte outside an {#if}; wrap it properly:
    const wrapped = `{#if x}<p>other</p>{:else}
      <div class="row-a"></div>
      <p role="status">{notice ?? ''}</p>
      <div class="row-b"></div>
    {/if}`;
    const { violations, statusSites } = checkFile('fixture.svelte', wrapped);
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
});
