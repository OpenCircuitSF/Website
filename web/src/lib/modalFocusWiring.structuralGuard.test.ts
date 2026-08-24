// #0216: a positive-wiring guard for the equivalence #0120's review checked
// by hand for exactly one site (CampaignSendDialog.svelte) and stated, but
// never asserted, for the other seven: that a modal panel's `onkeydown`
// lives on the SAME DOM node its focus effect moves focus to. That
// equivalence is not decorative -- it is what makes Escape work at all. A
// focus effect that targets one element while the keydown handler sits on a
// different one is a fix that silently does nothing, and nothing before
// this file would catch that shape landing.
//
// modalEscapeGuard.test.ts (also in this directory) proves the NEGATIVE
// half only: that no role="dialog" panel still carries the blind
// `onkeydown={(e) => e.stopPropagation()}` shape #0120 removed. It says
// nothing about whether the replacement handler is wired to the right node.
// This file is the positive half, proved structurally (AST only -- no
// mounting) rather than behaviourally, because most of the modals in scope
// -- Admin.svelte's five, CampaignEditor.svelte's one, Campaigns.svelte's
// one -- live inside components too large and too network-dependent
// (Admin.svelte is 2200+ lines; Campaigns.svelte's "New campaign" modal
// opens from a component whose own onMount calls listCampaigns() over the
// network) to mount cheaply with #0094's jsdom harness for every one of five
// open-state branches. CampaignSendDialog's equivalence for this same class
// of bug IS proved behaviourally, by mounting: see
// ./views/admin/CampaignSendDialog.behavior.test.ts. The two files overlap
// on that one component deliberately -- behavioural evidence is the
// stronger kind, and this guard additionally covers the TEN sites nothing
// mounts today: the seven above (Admin.svelte x5, CampaignEditor.svelte x1,
// Campaigns.svelte x1) plus #0052's three earlier sites (WorkshopEditor
// .svelte x2, Workshops.svelte x1). (An earlier version of this comment said
// "six sites," undercounting by four -- #0222 caught it; CampaignSendDialog
// .behavior.test.ts had the same undercount, corrected there too.)
//
// #0216's review bounced this file's first version (it built and ran, but
// its own header overclaimed what it checked). Two of its three regex false
// passes are fixed here by construction, not by patching the regex:
//
//   - Comment / string false pass: gone, because condition (2) below is no
//     longer "does this text appear anywhere in source" -- it walks the
//     `<script>` block's own parsed AST (svelte/compiler's `ast.instance
//     .content`, an ESTree Program) looking for an actual
//     `tick().then(() => V?.focus())` CallExpression. Comments and string
//     literals are not part of that AST, so a `//` comment or an HTML
//     comment (the review's live counter-example,
//     web/src/views/admin/Workshops.svelte's leading doc comment, which
//     used to satisfy the old regex on its own) cannot satisfy this one.
//   - Symmetric-swap false pass: closed for the shape the review
//     demonstrated. Repointing one panel's focus effect at a DIFFERENT
//     panel's ref while leaving each effect's own `if (...)` guard in place
//     (the review's Admin.svelte probe: `if (deactivatingUser) …
//     editInterestModalEl?.focus()` swapped against `if (editingInterest) …
//     deactivateModalEl?.focus()`) is now caught, because a focus call is no
//     longer credited to a panel just for matching its bound identifier
//     somewhere in the file -- it must ALSO sit under the same guard
//     condition as the `{#if …}` that renders that specific panel. The
//     guard condition is compared as source text (whitespace REMOVED
//     entirely, not just collapsed -- see guardKey below) on both sides: the
//     template's `{#if COND}` wrapping the panel, and the script's nearest
//     enclosing `if (COND) { … }` around the `tick().then()` call. A panel
//     rendered with no `{#if}` wrapper at all (CampaignSendDialog.svelte --
//     it is always mounted; its caller gates the mount) is matched against
//     an unconditional (no enclosing `if`) focus call the same way. Verified
//     against the real tree: all 11 sites pair up correctly, including the
//     one compound condition in the set (Admin.svelte's subscriber-detail
//     modal, guarded by `viewingSubscriber || viewingLoading ||
//     viewingError` on both the template and the effect side -- matched as
//     text, not by a single identifier).
//
// #0222 closed three more gaps the second phase-3 review found, all
// mutation-proved the same way as the two above (construct the shape,
// confirm the guard fails at the expected site, revert, confirm
// `shasum -a 256` byte-identity and an empty `git diff --stat` -- recorded
// in issues/0222.md's `## Verification`):
//
//   - {:else} inversion (the one with teeth): a panel rendered in `{:else}`
//     of `{#if a}` was previously matched against the SAME guard key as the
//     `{#if a}` branch, because the old code recorded one span (and one
//     guard) for the whole IfBlock, start to end -- consequent AND
//     alternate together. That let a focus call sitting under `if (a)` (dead
//     wiring for the else-branch panel, since the effect never runs when the
//     panel is actually visible) satisfy the check, while the correct
//     `if (!a)` / `else`-branch wiring was flagged as a false violation --
//     inverted in exactly the direction that matters, per this repo's
//     asymmetry rule (a false pass gets trusted; a false failure gets
//     silenced). Fixed by walking the template's IfBlock/ElseBlock structure
//     and the script's IfStatement/alternate structure the same way: each
//     REPLACES (does not stack onto) the guard it hands to its consequent
//     with the affirmative condition, and to its alternate with the NEGATED
//     one (`!(COND)`). "Replaces" rather than "accumulates" is deliberate,
//     not an oversight -- see the GuardConstraint comment below for why
//     accumulating every ancestor `{#if}` would make every real site fail.
//     See collectPanels and collectFocusCallSites below -- the same
//     GuardConstraint shape is threaded through both, so a template
//     else-branch and a script else-branch of the textually same condition
//     produce the identical key and nothing else does.
//   - guardKey was collapsing whitespace RUNS to a single space
//     (`.replace(/\s+/g, ' ')`) but not removing the SINGLE spaces already
//     present, so `{#if a&&b}` (key "a&&b") and `if (a && b)` (key
//     "a && b") produced different keys and the guard flagged genuinely
//     correct wiring as broken -- a false failure, the kind that gets a
//     guard edited or silenced rather than trusted, per this issue's own
//     framing. guardKey now strips ALL whitespace from the condition's
//     source span rather than collapsing it, so both forms normalize to the
//     literal string "a&&b". Newlines, indentation, and parenthesization
//     were never the problem (a genuinely different expression, like
//     `(a&&b)` vs `a&&b`, is still -- correctly -- a different key); only
//     whitespace runs INSIDE an otherwise-identical condition were.
//   - Reactivity: nothing previously required the matched
//     `tick().then(() => V?.focus())` call to be reactive at all -- a call
//     sitting in a plain function that nothing ever invokes satisfied
//     condition (2) just as well as one inside a real `$effect`. Every one
//     of the 11 real sites already lives inside `$effect(() => { … })`
//     (confirmed by inspection while fixing this: `grep -n 'effect(' ` over
//     all six host files finds an `$effect(() => {` wrapping each `tick()
//     .then()` call, with no exception), so requiring it costs nothing on
//     the real tree. collectFocusCallSites now threads an `inEffect` flag,
//     set true only while walking inside a `$effect(...)` call's own
//     argument, and a focus call outside one is no longer eligible to
//     satisfy a panel's requirement.
//
// Two gaps the review found are named here rather than closed, because
// closing them is out of proportion to what #0222 asked for (its own
// acceptance criteria allow "closed or named" for these two, reserving
// "must be closed" for the {:else} inversion above):
//
//   - Two panels in the SAME file gated by TEXTUALLY IDENTICAL conditions
//     (e.g. both `{#if open}`) would be indistinguishable to this guard --
//     it would accept either panel's onkeydown paired with either one's
//     focus call, because the (focusVar, guard) pair is looked up in a flat
//     list, not tied to a single specific `{#if}` block instance. This does
//     not occur anywhere in the current tree -- every one of the 11 sites'
//     guard conditions names a state variable unique to that one modal --
//     but a future modal that copy-pasted a generic condition name would not
//     be caught by this file alone. Closing it fully would mean matching by
//     the `{#if}` block's own identity (position), not just its condition
//     text, and re-deriving that the SAME `if` statement (not just an
//     identically-worded one) encloses both the panel's row in a
//     synthesized tree and the just-in-scope effect -- out of proportion to
//     what either #0216 or #0222 ask for. Recorded here in the same
//     register as modalKeydown.test.ts's argument-order admission: an
//     honest boundary, not a silent gap.
//   - `{#each}`-rendered panels are not given any special handling. A panel
//     inside an `{#each items as item}` block would still be visited (the
//     generic recursive walk descends into EachBlock bodies like any other
//     child), but it would be checked against whatever guard was active
//     when the `{#each}` was entered, with no per-item distinction -- every
//     iteration's panel would share one guard key regardless of `item`'s
//     identity, and a single module-level `bind:this` var could not
//     meaningfully target one specific iteration's DOM node anyway (Svelte's
//     `bind:this` on a var referenced inside `{#each}` binds to whichever
//     iteration last ran the binding). This is purely theoretical today --
//     no modal panel in the tree is `{#each}`-rendered, and none of the 11
//     sites are inside one -- so it is recorded rather than built for.
//
// The check, per `role="dialog" aria-modal="true"` panel that ALSO carries
// `bind:this={someVar}` (i.e. every panel wired for focus management by
// #0120 or #0052's earlier WorkshopEditor/Workshops fix):
//
//   1. that SAME element must also carry an `onkeydown` attribute -- so the
//      handler is not sitting on some sibling or the backdrop only; and
//   2. the component's own `<script>` AST must contain a
//      `tick().then(() => someVar?.focus())` call, inside a `$effect(...)`
//      call (allowing the `void …` wrapper and `?.`/`.` variants the
//      codebase actually uses), whose nearest enclosing `if (...)`
//      condition -- or absence of one -- has the SAME normalized source
//      text as the `{#if …}` / `{:else}` condition (or absence of one) that
//      renders THIS panel in the template, negation included.
//
// Mutation-proved repeatedly (see issues/0216.md and issues/0222.md
// `## Verification`): by repointing a single focus effect at a different
// modal's ref, by the review's symmetric-swap shape, by an inverted
// `{:else}` panel, by an out-of-`$effect` focus call, and by a
// whitespace-only condition rewrite -- each fails at the expected site, each
// reverted with `shasum -a 256` byte-identity confirmed and `git diff
// --stat` empty throughout.
import { describe, it, expect } from 'vitest';
import { parse as parseSvelte } from 'svelte/compiler';

type SvelteNode = Record<string, unknown>;

function findAttr(el: SvelteNode, name: string): SvelteNode | undefined {
  const attrs = el.attributes as SvelteNode[] | undefined;
  return attrs?.find((a) => a.type === 'Attribute' && a.name === name);
}

function attrTextEquals(attr: SvelteNode | undefined, want: string): boolean {
  const v = attr?.value;
  return Array.isArray(v) && v.length === 1 && (v[0] as SvelteNode)?.type === 'Text' && (v[0] as SvelteNode).data === want;
}

function findBindThis(el: SvelteNode): string | undefined {
  const attrs = el.attributes as SvelteNode[] | undefined;
  const bind = attrs?.find((a) => a.type === 'BindDirective' && a.name === 'this');
  const expr = bind?.expression as SvelteNode | undefined;
  return expr?.type === 'Identifier' ? (expr.name as string) : undefined;
}

// Normalized source text of a node's own span -- used as (half of) a guard
// "key" so that a compound condition (`viewingSubscriber || viewingLoading
// || viewingError`) can be compared as text, not just a single Identifier
// name. All whitespace is stripped, not just collapsed, so that `a&&b` and
// `a && b` normalize to the same key (#0222 -- see the header above).
// undefined means "no such node" (i.e. no wrapping condition at all).
function guardKey(source: string, node: SvelteNode | undefined): string | undefined {
  if (!node) return undefined;
  const start = node.start as number | undefined;
  const end = node.end as number | undefined;
  if (start === undefined || end === undefined) return undefined;
  return source.slice(start, end).replace(/\s+/g, '');
}

// The NEAREST enclosing `{#if COND}` / `if (COND)`'s contribution to a
// panel's or a focus call's guard: the condition's normalized text, plus
// whether this branch is the negated (else) side of it. Deliberately only
// the nearest one, not a combined stack of every ancestor -- the real tree
// nests each modal's own `{#if creating}`-style condition inside outer
// admin-section routing conditions (`{#if section === 'users'}`, `{#if
// !$currentUser?.is_admin}`, …) that the corresponding `$effect`'s own `if`
// never repeats, so combining them would make every real site fail. This
// mirrors the pre-#0222 behaviour (which found the narrowest SPAN
// containing an offset and used only that block's own guard) exactly,
// except that the else side is now correctly negated instead of reusing the
// if-branch's guard verbatim (#0222's `{:else}` inversion fix).
interface GuardConstraint {
  text: string;
  negated: boolean;
}

// Canonical string form of a (possibly absent) guard, compared for equality
// between the template side and the script side. `undefined` (unconditional
// -- no enclosing `{#if}`/`if` at all) maps to the empty string on both
// sides, so "always rendered" pairs with "always runs" consistently.
function guardKeyFromConstraint(constraint: GuardConstraint | undefined): string {
  if (!constraint) return '';
  return constraint.negated ? `!(${constraint.text})` : constraint.text;
}

function unwrapChain(node: SvelteNode | undefined): SvelteNode | undefined {
  return node && node.type === 'ChainExpression' ? (node.expression as SvelteNode) : node;
}

// `V?.focus()` or `V.focus()`, no arguments.
function matchFocusTarget(bodyExpr: SvelteNode | undefined): string | undefined {
  const call = unwrapChain(bodyExpr);
  if (!call || call.type !== 'CallExpression') return undefined;
  const args = call.arguments as SvelteNode[] | undefined;
  if (args && args.length > 0) return undefined;
  const callee = call.callee as SvelteNode | undefined;
  if (!callee || callee.type !== 'MemberExpression') return undefined;
  const prop = callee.property as SvelteNode | undefined;
  if (prop?.type !== 'Identifier' || prop.name !== 'focus') return undefined;
  const objNode = callee.object as SvelteNode | undefined;
  if (objNode?.type !== 'Identifier') return undefined;
  return objNode.name as string;
}

// `tick().then(() => V?.focus())`, allowing a block body with a single
// expression statement (`tick().then(() => { V?.focus(); })`).
function matchTickThenFocus(node: SvelteNode): string | undefined {
  const call = unwrapChain(node);
  if (!call || call.type !== 'CallExpression') return undefined;
  const callee = call.callee as SvelteNode | undefined;
  if (!callee || callee.type !== 'MemberExpression') return undefined;
  const prop = callee.property as SvelteNode | undefined;
  if (prop?.type !== 'Identifier' || prop.name !== 'then') return undefined;
  const tickCall = unwrapChain(callee.object as SvelteNode | undefined);
  if (!tickCall || tickCall.type !== 'CallExpression') return undefined;
  const tickCallee = tickCall.callee as SvelteNode | undefined;
  if (tickCallee?.type !== 'Identifier' || tickCallee.name !== 'tick') return undefined;
  const args = call.arguments as SvelteNode[] | undefined;
  if (!args || args.length !== 1) return undefined;
  const arrow = args[0];
  if (arrow.type !== 'ArrowFunctionExpression') return undefined;
  let bodyExpr = arrow.body as SvelteNode;
  if (bodyExpr.type === 'BlockStatement') {
    const stmts = (bodyExpr.body as SvelteNode[] | undefined) ?? [];
    if (stmts.length !== 1 || stmts[0].type !== 'ExpressionStatement') return undefined;
    bodyExpr = stmts[0].expression as SvelteNode;
  }
  return matchFocusTarget(bodyExpr);
}

// True for a CallExpression whose callee is the bare identifier `$effect`
// (the only form this codebase uses -- confirmed by `grep -n 'effect('`
// across every host file with a focus-wiring site) or the property access
// `$effect.pre` (not used today, but structurally the same reactive
// primitive, so treated the same rather than silently excluded).
function isEffectCall(call: SvelteNode): boolean {
  const callee = call.callee as SvelteNode | undefined;
  if (!callee) return false;
  if (callee.type === 'Identifier') return callee.name === '$effect';
  if (callee.type === 'MemberExpression') {
    const obj = callee.object as SvelteNode | undefined;
    const prop = callee.property as SvelteNode | undefined;
    return obj?.type === 'Identifier' && obj.name === '$effect' && prop?.type === 'Identifier' && prop.name === 'pre';
  }
  return false;
}

interface FocusCallSite {
  focusVar: string;
  guard: string;
  inEffect: boolean;
}

// Walks the <script> AST (not raw text) collecting every
// `tick().then(() => V?.focus())` call together with the source text of its
// NEAREST enclosing `if (...)` guard (negated when the call sits in that
// `if`'s `else`) and whether it is reactive (nested inside a `$effect(...)`
// call). Each `IfStatement` REPLACES the guard passed down (not accumulates
// it), matching collectPanels below. `void <expr>` wrappers are transparent
// to the walk itself, since only matchTickThenFocus needs to see through
// them.
function collectFocusCallSites(
  node: unknown,
  guard: GuardConstraint | undefined,
  inEffect: boolean,
  source: string,
  out: FocusCallSite[],
  seen = new Set<unknown>(),
): void {
  if (node === null || typeof node !== 'object') return;
  if (seen.has(node)) return;
  seen.add(node);
  if (Array.isArray(node)) {
    for (const item of node) collectFocusCallSites(item, guard, inEffect, source, out, seen);
    return;
  }
  const obj = node as SvelteNode;
  if (obj.type === 'IfStatement') {
    const condText = guardKey(source, obj.test as SvelteNode | undefined) ?? '';
    collectFocusCallSites(obj.consequent, { text: condText, negated: false }, inEffect, source, out, seen);
    if (obj.alternate) {
      collectFocusCallSites(obj.alternate, { text: condText, negated: true }, inEffect, source, out, seen);
    }
    return;
  }
  if (obj.type === 'CallExpression') {
    const focusVar = matchTickThenFocus(obj);
    if (focusVar !== undefined) {
      out.push({ focusVar, guard: guardKeyFromConstraint(guard), inEffect });
      return;
    }
    if (isEffectCall(obj)) {
      const args = (obj.arguments as SvelteNode[] | undefined) ?? [];
      for (const arg of args) collectFocusCallSites(arg, guard, true, source, out, seen);
      return;
    }
  }
  for (const key of Object.keys(obj)) {
    if (key === 'parent') continue;
    collectFocusCallSites(obj[key], guard, inEffect, source, out, seen);
  }
}

interface PanelGuard {
  node: SvelteNode;
  guard: string;
}

// Walks the template <fragment> AST collecting every RegularElement together
// with its NEAREST enclosing `{#if}` guard, mirroring collectFocusCallSites'
// handling of `if`/`else` exactly: an IfBlock's consequent Fragment REPLACES
// the guard with the affirmative condition, its alternate Fragment (whether
// a plain `{:else}` or a nested `{:else if}` IfBlock) REPLACES it with the
// NEGATED condition (#0222 -- previously both branches were recorded under
// one span with one un-negated guard, which is the {:else} inversion this
// issue closed). Deliberately narrowest-wins, not accumulated across nested
// `{#if}`s -- see the GuardConstraint comment above for why.
function collectPanels(
  node: unknown,
  guard: GuardConstraint | undefined,
  source: string,
  out: PanelGuard[],
  seen = new Set<unknown>(),
): void {
  if (node === null || typeof node !== 'object') return;
  if (seen.has(node)) return;
  seen.add(node);
  if (Array.isArray(node)) {
    for (const item of node) collectPanels(item, guard, source, out, seen);
    return;
  }
  const obj = node as SvelteNode;
  if (obj.type === 'IfBlock') {
    const condText = guardKey(source, obj.test as SvelteNode | undefined) ?? '';
    collectPanels(obj.consequent, { text: condText, negated: false }, source, out, seen);
    if (obj.alternate) {
      collectPanels(obj.alternate, { text: condText, negated: true }, source, out, seen);
    }
    return;
  }
  if (obj.type === 'RegularElement') {
    out.push({ node: obj, guard: guardKeyFromConstraint(guard) });
    // Fall through (not `return`) -- a panel can contain further markup,
    // including nested {#if} blocks that are irrelevant to THIS panel's own
    // guard (they gate content inside it, not the panel itself), which the
    // generic walk below still needs to traverse in case of a further
    // nested modal-shaped element.
  }
  for (const key of Object.keys(obj)) {
    if (key === 'parent') continue;
    collectPanels(obj[key], guard, source, out, seen);
  }
}

interface Violation {
  file: string;
  line: number;
  reason: string;
}

interface Site {
  file: string;
  line: number;
  focusVar: string;
}

function checkFile(fileName: string, source: string): { violations: Violation[]; sites: Site[] } {
  const violations: Violation[] = [];
  const sites: Site[] = [];
  const ast = parseSvelte(source, { filename: fileName, modern: true }) as unknown as SvelteNode;
  const lineOf = (offset: number): number => source.slice(0, offset).split('\n').length;

  const panelGuards: PanelGuard[] = [];
  collectPanels(ast.fragment, undefined, source, panelGuards);

  const scriptProgram = (ast.instance as SvelteNode | undefined)?.content;
  const focusSites: FocusCallSite[] = [];
  if (scriptProgram) collectFocusCallSites(scriptProgram, undefined, false, source, focusSites);

  for (const { node: n, guard: panelGuard } of panelGuards) {
    const roleAttr = findAttr(n, 'role');
    const ariaModalAttr = findAttr(n, 'aria-modal');
    if (!attrTextEquals(roleAttr, 'dialog')) continue;
    if (!attrTextEquals(ariaModalAttr, 'true')) continue;

    const focusVar = findBindThis(n);
    // Panels not yet wired for focus management at all are a different,
    // already-recorded gap (issues/0216.md's Notes: "that prerequisite is
    // currently unguarded too") -- out of scope here. This guard only
    // fires for panels that DO claim focus management via bind:this, and
    // checks that the claim is wired correctly.
    if (focusVar === undefined) continue;

    const start = n.start as number | undefined;
    const line = start !== undefined ? lineOf(start) : 0;

    const onkeydownAttr = findAttr(n, 'onkeydown');
    if (!onkeydownAttr) {
      violations.push({
        file: fileName,
        line,
        reason: `panel binds focus to "${focusVar}" via bind:this but carries no onkeydown of its own -- Escape can only reach it by bubbling, if at all`,
      });
      continue;
    }

    const match = focusSites.find((s) => s.focusVar === focusVar && s.guard === panelGuard && s.inEffect);
    if (!match) {
      const guardDesc = panelGuard === '' ? 'no {#if} guard (always rendered)' : `condition "${panelGuard}"`;
      violations.push({
        file: fileName,
        line,
        reason: `panel's onkeydown lives on the element bound to "${focusVar}" (rendered under ${guardDesc}), but no "tick().then(() => ${focusVar}?.focus())" call inside a $effect(...), guarded the same way, was found in the component's script -- the focus effect does not provably target this same node when this panel actually opens`,
      });
      continue;
    }

    sites.push({ file: fileName, line, focusVar });
  }

  return { violations, sites };
}

const SOURCE_FILES = import.meta.glob('../views/**/*.svelte', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

function toRepoRelativePath(globKey: string): string {
  return `web/src/views/${globKey.slice('../views/'.length)}`;
}

describe('modal focus-wiring guard (#0216, #0222): onkeydown node === focus-effect target node, per-panel guard-condition matched (including {:else}, negated)', () => {
  it('every role="dialog" panel with bind:this also carries onkeydown on the same node, guarded the same way (else-negated) as a same-condition reactive focus effect', () => {
    const violations: Violation[] = [];
    const sites: Site[] = [];
    for (const [path, source] of Object.entries(SOURCE_FILES)) {
      if (path.endsWith('.test.ts')) continue;
      const rel = toRepoRelativePath(path);
      const result = checkFile(rel, source);
      violations.push(...result.violations);
      sites.push(...result.sites.map((s) => ({ ...s, file: rel })));
    }

    if (violations.length > 0) {
      const detail = violations.map((v) => `  ${v.file}:${v.line}: ${v.reason}`).join('\n');
      throw new Error(
        `modal panel(s) with focus management wired to a node other than the one carrying onkeydown, guarded differently from their own {#if}/{:else}, or not reactive (#0216, #0222):\n${detail}`,
      );
    }
    expect(violations).toHaveLength(0);

    // Sanity floor, not a magic total: 8 sites from #0120's fix and
    // CampaignSendDialog's pre-existing wiring (Admin.svelte x5,
    // CampaignEditor.svelte x1, Campaigns.svelte x1, CampaignSendDialog
    // .svelte x1 -- CampaignSendDialog is INSIDE this count of 8, not
    // additional to it) plus #0052's 3 earlier sites (WorkshopEditor.svelte
    // x2, Workshops.svelte x1) = 11 total. If this count drops, either a
    // modal lost its focus wiring or the scan itself broke.
    expect(sites.length).toBeGreaterThanOrEqual(11);
  });
});

describe('checkFile (synthetic fixtures)', () => {
  it('passes a correctly wired panel: bind:this and onkeydown on the same node, focus effect targets the bound var under the same {#if}, inside $effect', () => {
    const src = `<script>
  $effect(() => {
    if (open) void tick().then(() => panelEl?.focus());
  });
</script>
{#if open}
  <div role="dialog" aria-modal="true" bind:this={panelEl} onkeydown={(e) => { if (isModalEscape(e)) close(); }}>hi</div>
{/if}`;
    const { violations, sites } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(0);
    expect(sites).toHaveLength(1);
    expect(sites[0].focusVar).toBe('panelEl');
  });

  it('passes an unconditionally-rendered panel (the CampaignSendDialog shape: onkeydown={handleKeydown}, no wrapping {#if}, no wrapping if)', () => {
    const src = `<script>
  function handleKeydown(e) { if (e.key === 'Escape' && !sending) onClose(); }
  $effect(() => { void tick().then(() => dialogEl?.focus()); });
</script>
<div role="dialog" aria-modal="true" bind:this={dialogEl} tabindex="-1" onkeydown={handleKeydown}>hi</div>`;
    const { violations, sites } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(0);
    expect(sites).toHaveLength(1);
  });

  it('passes a panel guarded by a compound condition, matched as text against the identical script-side condition (the Admin.svelte subscriber-detail shape)', () => {
    const src = `<script>
  $effect(() => {
    if (a || b || c) {
      void tick().then(() => panelEl?.focus());
    }
  });
</script>
{#if a || b || c}
  <div role="dialog" aria-modal="true" bind:this={panelEl} onkeydown={(e) => { if (isModalEscape(e)) close(); }}>hi</div>
{/if}`;
    const { violations, sites } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(0);
    expect(sites).toHaveLength(1);
  });

  it('passes a panel guarded by whitespace inside the condition differently on each side (#0222: {#if a&&b} vs if (a && b))', () => {
    const src = `<script>
  $effect(() => {
    if (a && b) void tick().then(() => panelEl?.focus());
  });
</script>
{#if a&&b}
  <div role="dialog" aria-modal="true" bind:this={panelEl} onkeydown={(e) => { if (isModalEscape(e)) close(); }}>hi</div>
{/if}`;
    const { violations, sites } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(0);
    expect(sites).toHaveLength(1);
  });

  it('flags a panel with bind:this but no onkeydown of its own', () => {
    const src = `<script>
  $effect(() => { if (open) void tick().then(() => panelEl?.focus()); });
</script>
{#if open}
  <div role="dialog" aria-modal="true" bind:this={panelEl}>hi</div>
{/if}`;
    const { violations } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(1);
    expect(violations[0].reason).toContain('no onkeydown');
  });

  it('flags a panel whose bound var is never the target of a tick().then() focus call (the vacuousness case: the effect focuses a DIFFERENT node)', () => {
    const src = `<script>
  // Mistake: the effect focuses a sibling modal's ref, not this panel's.
  $effect(() => { if (open) void tick().then(() => otherPanelEl?.focus()); });
</script>
{#if open}
  <div role="dialog" aria-modal="true" bind:this={panelEl} onkeydown={(e) => { if (isModalEscape(e)) close(); }}>hi</div>
{/if}`;
    const { violations } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(1);
    expect(violations[0].reason).toContain('no "tick().then(() => panelEl?.focus())"');
  });

  it('flags the symmetric-swap shape (#0216 review, probe B): two panels each keep their OWN {#if} guard, but each effect focuses the OTHER panel\'s ref', () => {
    // deactivatingUser's own effect now focuses editInterestModalEl, and
    // editingInterest's own effect now focuses deactivateModalEl -- both
    // identifiers still appear somewhere in the file, and (under the old
    // file-wide-search check) both panels would have satisfied condition
    // (2). Guard-matching closes this: deactivateModalEl's panel is
    // rendered under "deactivatingUser", and no tick().then() call guarded
    // by "deactivatingUser" targets deactivateModalEl anymore.
    const src = `<script>
  $effect(() => {
    if (deactivatingUser) void tick().then(() => editInterestModalEl?.focus());
  });
  $effect(() => {
    if (editingInterest) void tick().then(() => deactivateModalEl?.focus());
  });
</script>
{#if deactivatingUser}
  <div role="dialog" aria-modal="true" bind:this={deactivateModalEl} onkeydown={(e) => { if (isModalEscape(e)) close(); }}>hi</div>
{/if}
{#if editingInterest}
  <div role="dialog" aria-modal="true" bind:this={editInterestModalEl} onkeydown={(e) => { if (isModalEscape(e)) close(); }}>hi</div>
{/if}`;
    const { violations } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(2);
    expect(violations.map((v) => v.reason).join('\n')).toContain('deactivateModalEl');
    expect(violations.map((v) => v.reason).join('\n')).toContain('editInterestModalEl');
  });

  it('does not accept a comment-only match (#0216 review, probe C): the real focus call is gone, only a // comment quotes the old shape', () => {
    const src = `<script>
  // #0120 used to do: void tick().then(() => panelEl?.focus());
  // (removed during a refactor -- focus is now handled elsewhere)
</script>
{#if open}
  <div role="dialog" aria-modal="true" bind:this={panelEl} onkeydown={(e) => { if (isModalEscape(e)) close(); }}>hi</div>
{/if}`;
    const { violations } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(1);
    expect(violations[0].reason).toContain('no "tick().then(() => panelEl?.focus())"');
  });

  it('does not accept an HTML-comment-only match either (#0216 review\'s live Workshops.svelte:24 counter-example, reproduced as a fixture)', () => {
    const src = `<!--
  bind:this={panelEl} plus a $effect that calls tick().then(() =>
  panelEl?.focus()) once open goes true
-->
<script>
</script>
{#if open}
  <div role="dialog" aria-modal="true" bind:this={panelEl} onkeydown={(e) => { if (isModalEscape(e)) close(); }}>hi</div>
{/if}`;
    const { violations } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(1);
    expect(violations[0].reason).toContain('no "tick().then(() => panelEl?.focus())"');
  });

  it('does not fire on a role="dialog" panel with no bind:this at all (a different, already-recorded gap)', () => {
    const src = `<div role="dialog" aria-modal="true" onkeydown={(e) => { if (isModalEscape(e)) close(); }}>hi</div>`;
    const { violations, sites } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(0);
    expect(sites).toHaveLength(0);
  });

  it('ignores an element that is not a modal panel (missing role or aria-modal) even with bind:this', () => {
    const src = `<div bind:this={panelEl} onkeydown={(e) => e.stopPropagation()}>hi</div>`;
    const { violations, sites } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(0);
    expect(sites).toHaveLength(0);
  });

  it('#0222: flags an inverted {:else} panel -- the panel renders when the condition is FALSE, but the focus call sits under the un-negated condition (dead wiring)', () => {
    const src = `<script>
  $effect(() => {
    if (a) void tick().then(() => panelEl?.focus());
  });
</script>
{#if a}
  <p>not a modal</p>
{:else}
  <div role="dialog" aria-modal="true" bind:this={panelEl} onkeydown={(e) => { if (isModalEscape(e)) close(); }}>hi</div>
{/if}`;
    const { violations } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(1);
    expect(violations[0].reason).toContain('panelEl');
  });

  it('#0222: passes a correctly wired {:else} panel -- the focus call sits under the NEGATED condition, matching the else-branch panel', () => {
    const src = `<script>
  $effect(() => {
    if (a) {
      // nothing to focus on this branch
    } else {
      void tick().then(() => panelEl?.focus());
    }
  });
</script>
{#if a}
  <p>not a modal</p>
{:else}
  <div role="dialog" aria-modal="true" bind:this={panelEl} onkeydown={(e) => { if (isModalEscape(e)) close(); }}>hi</div>
{/if}`;
    const { violations, sites } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(0);
    expect(sites).toHaveLength(1);
  });

  it('#0222: flags a focus call that matches everything EXCEPT that it sits outside any $effect -- reactivity is required', () => {
    const src = `<script>
  // Never actually called by anything reactive -- a plain function
  // definition that nothing invokes.
  function neverCalled() {
    if (open) void tick().then(() => panelEl?.focus());
  }
</script>
{#if open}
  <div role="dialog" aria-modal="true" bind:this={panelEl} onkeydown={(e) => { if (isModalEscape(e)) close(); }}>hi</div>
{/if}`;
    const { violations } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(1);
    expect(violations[0].reason).toContain('$effect(...)');
  });
});
