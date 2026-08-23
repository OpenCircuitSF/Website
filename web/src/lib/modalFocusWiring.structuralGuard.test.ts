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
// This file is the positive half, proved structurally (AST + a script-body
// pattern match) rather than behaviourally, because most of the modals in
// scope -- Admin.svelte's five, CampaignEditor.svelte's one,
// Campaigns.svelte's one -- live inside components too large and too
// network-dependent (Admin.svelte is 2200+ lines; Campaigns.svelte's "New
// campaign" modal opens from a component whose own onMount calls
// listCampaigns() over the network) to mount cheaply with #0094's jsdom
// harness for every one of five open-state branches. CampaignSendDialog's
// equivalence for this same class of bug IS proved behaviourally, by
// mounting: see ./views/admin/CampaignSendDialog.behavior.test.ts. The two
// files overlap on that one component deliberately -- behavioural evidence
// is the stronger kind, and this guard additionally covers the six sites
// nothing mounts today.
//
// The check, per `role="dialog" aria-modal="true"` panel that ALSO carries
// `bind:this={someVar}` (i.e. every panel wired for focus management by
// #0120 or #0052's earlier WorkshopEditor/Workshops fix):
//
//   1. that SAME element must also carry an `onkeydown` attribute -- so the
//      handler is not sitting on some sibling or the backdrop only; and
//   2. somewhere in the component's own source there must be a
//      `tick().then(() => someVar?.focus())` call using that EXACT bound
//      variable -- the convention every fixed site follows (#0052's
//      original shape, reproduced by #0120 at the other seven sites) -- so
//      the node that gets focused is provably the same node bound by
//      `bind:this`, and therefore the same node the onkeydown in (1) is on.
//
// Chaining (1) and (2) gives node-identity without parsing the effect body
// itself: (1) is an AST fact (two attributes, one element); (2) is a text
// match anchored to the specific identifier `bind:this` just gave us, not a
// generic "does .focus() appear anywhere" check, so it cannot be satisfied
// by an unrelated focus() call elsewhere in the file.
//
// Mutation-proved (see issues/0216.md `## Verification`): pointing
// Admin.svelte's Deactivate-user focus effect at a different modal's ref
// variable makes this guard fail at that exact site, restored afterwards
// and confirmed byte-identical with `shasum -a 256`.
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

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
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

  walk(ast.fragment, (n) => {
    if (n.type !== 'RegularElement') return;
    const roleAttr = findAttr(n, 'role');
    const ariaModalAttr = findAttr(n, 'aria-modal');
    if (!attrTextEquals(roleAttr, 'dialog')) return;
    if (!attrTextEquals(ariaModalAttr, 'true')) return;

    const focusVar = findBindThis(n);
    // Panels not yet wired for focus management at all are a different,
    // already-recorded gap (issues/0216.md's Notes: "that prerequisite is
    // currently unguarded too") -- out of scope here. This guard only
    // fires for panels that DO claim focus management via bind:this, and
    // checks that the claim is wired correctly.
    if (focusVar === undefined) return;

    const start = n.start as number | undefined;
    const line = start !== undefined ? lineOf(start) : 0;

    const onkeydownAttr = findAttr(n, 'onkeydown');
    if (!onkeydownAttr) {
      violations.push({
        file: fileName,
        line,
        reason: `panel binds focus to "${focusVar}" via bind:this but carries no onkeydown of its own -- Escape can only reach it by bubbling, if at all`,
      });
      return;
    }

    const focusPattern = new RegExp(
      `tick\\(\\)\\.then\\(\\(\\)\\s*=>\\s*${escapeRegExp(focusVar)}\\??\\.focus\\(\\)\\)`,
    );
    if (!focusPattern.test(source)) {
      violations.push({
        file: fileName,
        line,
        reason: `panel's onkeydown lives on the element bound to "${focusVar}", but no "tick().then(() => ${focusVar}?.focus())" call was found anywhere in the file -- the focus effect does not provably target this same node`,
      });
      return;
    }

    sites.push({ file: fileName, line, focusVar });
  });

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

describe('modal focus-wiring guard (#0216): onkeydown node === focus-effect target node', () => {
  it('every role="dialog" panel with bind:this also carries onkeydown on the same node, and is the node a tick().then() focus call actually targets', () => {
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
        `modal panel(s) with focus management wired to a node other than the one carrying onkeydown (#0216):\n${detail}`,
      );
    }
    expect(violations).toHaveLength(0);

    // Sanity floor, not a magic total: at minimum the 8 sites #0120's fix
    // and CampaignSendDialog's pre-existing wiring account for
    // (Admin.svelte x5, CampaignEditor.svelte x1, Campaigns.svelte x1,
    // CampaignSendDialog.svelte x1) plus #0052's 3 earlier sites
    // (WorkshopEditor.svelte x2, Workshops.svelte x1) should all be found
    // and pass -- if this count drops, either a modal lost its focus
    // wiring or the scan itself broke.
    expect(sites.length).toBeGreaterThanOrEqual(11);
  });
});

describe('checkFile (synthetic fixtures)', () => {
  it('passes a correctly wired panel: bind:this and onkeydown on the same node, focus effect targets the bound var', () => {
    const src = `<script>
  function open() { void tick().then(() => panelEl?.focus()); }
</script>
<div role="dialog" aria-modal="true" bind:this={panelEl} onkeydown={(e) => { if (isModalEscape(e)) close(); }}>hi</div>`;
    const { violations, sites } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(0);
    expect(sites).toHaveLength(1);
    expect(sites[0].focusVar).toBe('panelEl');
  });

  it('flags a panel with bind:this but no onkeydown of its own', () => {
    const src = `<script>
  function open() { void tick().then(() => panelEl?.focus()); }
</script>
<div role="dialog" aria-modal="true" bind:this={panelEl}>hi</div>`;
    const { violations } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(1);
    expect(violations[0].reason).toContain('no onkeydown');
  });

  it('flags a panel whose bound var is never the target of a tick().then() focus call (the vacuousness case: the effect targets a DIFFERENT node)', () => {
    const src = `<script>
  // Mistake: the effect focuses a sibling modal's ref, not this panel's.
  function open() { void tick().then(() => otherPanelEl?.focus()); }
</script>
<div role="dialog" aria-modal="true" bind:this={panelEl} onkeydown={(e) => { if (isModalEscape(e)) close(); }}>hi</div>`;
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

  it('accepts the CampaignSendDialog shape: onkeydown={handleKeydown} (identifier reference, not an inline arrow)', () => {
    const src = `<script>
  function handleKeydown(e) { if (e.key === 'Escape' && !sending) onClose(); }
  $effect(() => { void tick().then(() => dialogEl?.focus()); });
</script>
<div role="dialog" aria-modal="true" bind:this={dialogEl} tabindex="-1" onkeydown={handleKeydown}>hi</div>`;
    const { violations, sites } = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(0);
    expect(sites).toHaveLength(1);
  });
});
