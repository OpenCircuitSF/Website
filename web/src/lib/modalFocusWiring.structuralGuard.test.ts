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
// stronger kind, and this guard additionally covers the six sites nothing
// mounts today.
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
//     guard condition is compared as source text (normalized for
//     whitespace) on both sides: the template's `{#if COND}` wrapping the
//     panel, and the script's nearest enclosing `if (COND) { … }` around the
//     `tick().then()` call. A panel rendered with no `{#if}` wrapper at all
//     (CampaignSendDialog.svelte -- it is always mounted; its caller gates
//     the mount) is matched against an unconditional (no enclosing `if`)
//     focus call the same way. Verified against the real tree: all 11 sites
//     pair up correctly, including the one compound condition in the set
//     (Admin.svelte's subscriber-detail modal, guarded by `viewingSubscriber
//     || viewingLoading || viewingError` on both the template and the
//     effect side -- matched as text, not by a single identifier).
//
// Named residual gap, not closed here: two panels in the SAME file gated by
// TEXTUALLY IDENTICAL conditions (e.g. both `{#if open}`) would be
// indistinguishable to this guard -- it would accept either panel's onkeydown
// paired with either one's focus call, because the (focusVar, guard) pair is
// looked up in a flat list, not tied to a single specific `{#if}` block
// instance. This does not occur anywhere in the current tree -- every one of
// the 11 sites' guard conditions names a state variable unique to that one
// modal -- but a future modal that copy-pasted a generic condition name would
// not be caught by this file alone. Closing it fully would mean matching by
// the `{#if}` block's own identity (position), not just its condition text,
// and re-deriving that the SAME `if` statement (not just an
// identically-worded one) encloses both the panel's row in a synthesized
// tree and the just-in-scope effect -- out of proportion to what #0216 asks
// for. Recorded here in the same register as modalKeydown.test.ts's
// argument-order admission: an honest boundary, not a silent gap.
//
// The check, per `role="dialog" aria-modal="true"` panel that ALSO carries
// `bind:this={someVar}` (i.e. every panel wired for focus management by
// #0120 or #0052's earlier WorkshopEditor/Workshops fix):
//
//   1. that SAME element must also carry an `onkeydown` attribute -- so the
//      handler is not sitting on some sibling or the backdrop only; and
//   2. the component's own `<script>` AST must contain a
//      `tick().then(() => someVar?.focus())` call (allowing the `void …`
//      wrapper and `?.`/`.` variants the codebase actually uses) whose
//      nearest enclosing `if (...)` condition -- or absence of one -- has
//      the SAME source text as the `{#if …}` condition (or absence of one)
//      that renders THIS panel in the template.
//
// Mutation-proved twice (see issues/0216.md `## Verification`): once by
// repointing a single focus effect at a different modal's ref (the shape
// this guard's predecessor's own proof used), and once by the review's
// symmetric-swap shape described above -- both fail at the expected site,
// both reverted with `shasum -a 256` byte-identity confirmed.
import { describe, it, expect } from 'vitest';
import { parse as parseSvelte } from 'svelte/compiler';

type SvelteNode = Record<string, unknown>;

function walk(node: unknown, visit: (n: SvelteNode) => void, seen = new Set<unknown>()): void {
  if (node === null || typeof node !== 'object') return;
  if (seen.has(node)) return;
  seen.add(node);
  if (Array.isArray(node)) {
    for (const item of node) walk(item, visit, seen);
    return;
  }
  const obj = node as SvelteNode;
  if (typeof obj.type === 'string') visit(obj);
  for (const key of Object.keys(obj)) {
    if (key === 'parent') continue;
    walk(obj[key], visit, seen);
  }
}

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

// Normalized source text of a node's own span -- used as a guard "key" so
// that a compound condition (`viewingSubscriber || viewingLoading ||
// viewingError`) can be compared as text, not just a single Identifier
// name. undefined means "no such node" (i.e. no wrapping condition at all).
function guardKey(source: string, node: SvelteNode | undefined): string | undefined {
  if (!node) return undefined;
  const start = node.start as number | undefined;
  const end = node.end as number | undefined;
  if (start === undefined || end === undefined) return undefined;
  return source.slice(start, end).replace(/\s+/g, ' ').trim();
}

function unwrapVoid(node: SvelteNode | undefined): SvelteNode | undefined {
  let n = node;
  while (n && n.type === 'UnaryExpression' && n.operator === 'void') {
    n = n.argument as SvelteNode | undefined;
  }
  return n;
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

interface FocusCallSite {
  focusVar: string;
  guard: string | undefined;
}

// Walks the <script> AST (not raw text) collecting every
// `tick().then(() => V?.focus())` call together with the source text of its
// nearest enclosing `if (...)` condition, tracked properly per-branch (an
// `if`'s consequent inherits the new guard; its alternate keeps the outer
// one). `void <expr>` wrappers are transparent to the walk itself, since
// only matchTickThenFocus needs to see through them.
function collectFocusCallSites(
  node: unknown,
  guard: string | undefined,
  source: string,
  out: FocusCallSite[],
  seen = new Set<unknown>(),
): void {
  if (node === null || typeof node !== 'object') return;
  if (seen.has(node)) return;
  seen.add(node);
  if (Array.isArray(node)) {
    for (const item of node) collectFocusCallSites(item, guard, source, out, seen);
    return;
  }
  const obj = node as SvelteNode;
  if (obj.type === 'IfStatement') {
    const consequentGuard = guardKey(source, obj.test as SvelteNode | undefined);
    collectFocusCallSites(obj.consequent, consequentGuard, source, out, seen);
    if (obj.alternate) collectFocusCallSites(obj.alternate, guard, source, out, seen);
    return;
  }
  if (obj.type === 'CallExpression') {
    const focusVar = matchTickThenFocus(obj);
    if (focusVar !== undefined) {
      out.push({ focusVar, guard });
      return;
    }
  }
  for (const key of Object.keys(obj)) {
    if (key === 'parent') continue;
    collectFocusCallSites(obj[key], guard, source, out, seen);
  }
}

interface IfBlockSpan {
  start: number;
  end: number;
  guard: string | undefined;
}

// The innermost {#if ...} (by narrowest span) whose range contains `offset`,
// i.e. the condition that gates whether this panel is even in the DOM.
function enclosingGuard(blocks: IfBlockSpan[], offset: number): string | undefined {
  let best: IfBlockSpan | undefined;
  for (const b of blocks) {
    if (b.start <= offset && offset < b.end) {
      if (!best || b.end - b.start < best.end - best.start) best = b;
    }
  }
  return best?.guard;
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

  const ifBlocks: IfBlockSpan[] = [];
  const panels: SvelteNode[] = [];
  walk(ast.fragment, (n) => {
    if (n.type === 'IfBlock') {
      const start = n.start as number | undefined;
      const end = n.end as number | undefined;
      if (start !== undefined && end !== undefined) {
        ifBlocks.push({ start, end, guard: guardKey(source, n.test as SvelteNode | undefined) });
      }
      return;
    }
    if (n.type === 'RegularElement') panels.push(n);
  });

  const scriptProgram = (ast.instance as SvelteNode | undefined)?.content;
  const focusSites: FocusCallSite[] = [];
  if (scriptProgram) collectFocusCallSites(scriptProgram, undefined, source, focusSites);

  for (const n of panels) {
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

    const panelGuard = enclosingGuard(ifBlocks, start ?? 0);
    const match = focusSites.find((s) => s.focusVar === focusVar && s.guard === panelGuard);
    if (!match) {
      const guardDesc = panelGuard === undefined ? 'no {#if} guard (always rendered)' : `condition "${panelGuard}"`;
      violations.push({
        file: fileName,
        line,
        reason: `panel's onkeydown lives on the element bound to "${focusVar}" (rendered under ${guardDesc}), but no "tick().then(() => ${focusVar}?.focus())" call guarded the same way was found in the component's script -- the focus effect does not provably target this same node when this panel actually opens`,
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

describe('modal focus-wiring guard (#0216): onkeydown node === focus-effect target node, per-panel guard-condition matched', () => {
  it('every role="dialog" panel with bind:this also carries onkeydown on the same node, guarded the same way as a same-condition focus effect', () => {
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
        `modal panel(s) with focus management wired to a node other than the one carrying onkeydown, or guarded differently from their own {#if} (#0216):\n${detail}`,
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
  it('passes a correctly wired panel: bind:this and onkeydown on the same node, focus effect targets the bound var under the same {#if}', () => {
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
});
