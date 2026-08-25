// #0220: `tabindex="-1"` on a modal panel is a silent prerequisite for
// `.focus()` to do anything at all -- a plain `<div role="dialog">` is not
// natively focusable, so a programmatic `V?.focus()` call on one with no
// `tabindex` is a no-op, and the handler-host === focus-target equivalence
// modalFocusWiring.structuralGuard.test.ts proves would still not make
// Escape work. #0216's own Notes name this exactly: "All five Admin.svelte
// panels carry tabindex="-1" ... That prerequisite is currently unguarded
// too." All 11 sites modalFocusWiring.structuralGuard.test.ts finds carry
// it today (confirmed by the tree-wide test below, which finds zero
// violations) -- this file is what keeps that true.
//
// Deliberately its own file rather than folded into
// modalFocusWiring.structuralGuard.test.ts's existing per-panel loop: that
// file's 14 synthetic fixtures assert EXACT violation counts
// (`toHaveLength(1)`, etc.) for a completely different defect
// (onkeydown/focus-effect wiring), and none of them carries `tabindex="-1"`
// today -- adding a second violation class to that same loop would either
// break every one of those counts or require editing all fourteen fixtures
// for no benefit to what they are proving. This file reuses the same
// AST-walk technique (svelte/compiler's parse, RegularElement nodes) but
// keeps its own, separate violation list, so the two guards can never
// interact with each other's counts.
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

interface Violation {
  file: string;
  line: number;
  reason: string;
}

// collectRegularElements walks the template AST collecting every
// RegularElement -- no guard-tracking needed here, unlike
// modalFocusWiring.structuralGuard.test.ts's collectPanels, since
// tabindex="-1" is an unconditional structural property of the element
// itself, not something that depends on which {#if} branch renders it.
function collectRegularElements(node: unknown, out: SvelteNode[], seen = new Set<unknown>()): SvelteNode[] {
  if (node === null || typeof node !== 'object') return out;
  if (seen.has(node)) return out;
  seen.add(node);
  if (Array.isArray(node)) {
    for (const item of node) collectRegularElements(item, out, seen);
    return out;
  }
  const obj = node as SvelteNode;
  if (obj.type === 'RegularElement') out.push(obj);
  for (const key of Object.keys(obj)) {
    if (key === 'parent') continue;
    collectRegularElements(obj[key], out, seen);
  }
  return out;
}

function checkFile(fileName: string, source: string): Violation[] {
  const violations: Violation[] = [];
  const ast = parseSvelte(source, { filename: fileName, modern: true }) as unknown as SvelteNode;
  const lineOf = (offset: number): number => source.slice(0, offset).split('\n').length;

  for (const n of collectRegularElements(ast.fragment, [])) {
    const roleAttr = findAttr(n, 'role');
    const ariaModalAttr = findAttr(n, 'aria-modal');
    if (!attrTextEquals(roleAttr, 'dialog')) continue;
    if (!attrTextEquals(ariaModalAttr, 'true')) continue;

    const tabindexAttr = findAttr(n, 'tabindex');
    if (!attrTextEquals(tabindexAttr, '-1')) {
      const start = n.start as number | undefined;
      violations.push({
        file: fileName,
        line: start !== undefined ? lineOf(start) : 0,
        reason: 'role="dialog" aria-modal="true" panel has no tabindex="-1" -- without it, a programmatic .focus() call on this element is a silent no-op',
      });
    }
  }
  return violations;
}

const SOURCE_FILES = import.meta.glob('../views/**/*.svelte', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

function toRepoRelativePath(globKey: string): string {
  return `web/src/views/${globKey.slice('../views/'.length)}`;
}

describe('modal tabindex guard (#0220): every role="dialog" aria-modal="true" panel carries tabindex="-1"', () => {
  it('every modal panel is programmatically focusable', () => {
    const violations: Violation[] = [];
    for (const [path, source] of Object.entries(SOURCE_FILES)) {
      if (path.endsWith('.test.ts')) continue;
      const rel = toRepoRelativePath(path);
      violations.push(...checkFile(rel, source));
    }

    if (violations.length > 0) {
      const detail = violations.map((v) => `  ${v.file}:${v.line}: ${v.reason}`).join('\n');
      throw new Error(`modal panel(s) missing tabindex="-1" (#0220):\n${detail}`);
    }
    expect(violations).toHaveLength(0);
  });
});

describe('checkFile (synthetic fixtures)', () => {
  it('passes a panel with tabindex="-1"', () => {
    const src = `<div role="dialog" aria-modal="true" tabindex="-1">hi</div>`;
    expect(checkFile('fixture.svelte', src)).toHaveLength(0);
  });

  it('flags a panel missing tabindex entirely -- the mutation this issue asks for', () => {
    const src = `<div role="dialog" aria-modal="true">hi</div>`;
    const violations = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(1);
    expect(violations[0].reason).toContain('tabindex="-1"');
  });

  it('flags a panel with the wrong tabindex value (0, not -1)', () => {
    const src = `<div role="dialog" aria-modal="true" tabindex="0">hi</div>`;
    const violations = checkFile('fixture.svelte', src);
    expect(violations).toHaveLength(1);
  });

  it('ignores an element that is not a modal panel (missing role or aria-modal), even with no tabindex', () => {
    const src = `<div>hi</div>`;
    expect(checkFile('fixture.svelte', src)).toHaveLength(0);
  });

  it('ignores role="dialog" alone, without aria-modal="true"', () => {
    const src = `<div role="dialog">hi</div>`;
    expect(checkFile('fixture.svelte', src)).toHaveLength(0);
  });
});
