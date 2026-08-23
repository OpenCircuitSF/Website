// #0120: "Escape does not close any admin modal" was one root cause
// reproduced at 8 sites across 4 files (Admin.svelte x5, CampaignEditor
// .svelte x1, Campaigns.svelte x1, CampaignSendDialog.svelte x1) -- three
// more sites, in WorkshopEditor.svelte (x2) and Workshops.svelte (x1), had
// already been fixed under #0052 before this issue was even filed, so they
// are not part of that count -- every `role="dialog" aria-modal="true"` panel's own
// `onkeydown={(e) => e.stopPropagation()}`,
// added to mirror the click guard that keeps a click inside the panel from
// reaching the backdrop's click-outside-to-dismiss `onclick`. There is no
// keyboard analogue of "inside vs. outside the panel", so that handler did
// nothing but blindly eat every keydown -- including Escape -- before it
// could bubble to the backdrop's own Escape handler.
//
// This is a REPO-WIDE structural guard, not a per-component regression test
// enumerating the 8 sites by name: it walks every .svelte file under
// web/src/views via svelte/compiler's parse({ modern: true }) AST (the same
// technique citationGuard.test.ts and CampaignSendDialog.structuralGuard
// .test.ts already use for the same reason -- at the time this guard was
// written, #0094 had not yet landed, so there was no DOM harness and no
// .svelte file was imported by any test; #0094 has since added one, see
// ../views/admin/CampaignSendDialog.behavior.test.ts and this directory's
// modalFocusWiring.structuralGuard.test.ts), finds every
// `role="dialog"` panel, and fails if that panel's `onkeydown` is exactly
// the blind-stopPropagation shape. A future modal built by copying the old
// markup (the way #0047's three components did, per this issue's own Notes)
// will fail THIS test the moment it lands, rather than waiting for another
// review pass to notice by inspection.
//
// What this guard does NOT assert: it does not require the panel to contain
// its own Escape-handling logic, because the fixes in this codebase are not
// uniform on that point and both are correct --
//   - Admin.svelte / CampaignEditor.svelte / Campaigns.svelte / WorkshopEditor
//     .svelte / Workshops.svelte: the panel's onkeydown now itself closes on
//     Escape (calling lib/modalKeydown.ts's isModalEscape, or -- for the two
//     already-fixed pre-#0120 sites -- an inline `e.key === 'Escape'` check
//     Escape kept as-is rather than migrated, out of caution about touching
//     code already verified working in a real browser with no way to
//     re-verify here).
//   - CampaignSendDialog.svelte: the panel's blind-stopPropagation onkeydown
//     was replaced by binding the SAME `handleKeydown` reference already on
//     the backdrop (which already checked `e.key === 'Escape' && !sending`
//     correctly) to the panel too, deliberately, rather than rewriting
//     `handleKeydown` itself -- because #0197 built a structural guard
//     (CampaignSendDialog.structuralGuard.test.ts) that reads that exact
//     expression shape out of this component's own AST; changing the shape
//     would mean deliberately updating that guard too, out of proportion to
//     this fix. (An earlier attempt did delete the panel's onkeydown outright
//     rather than rebind it, but that attempt was abandoned -- it tripped
//     svelte-check's a11y_click_events_have_key_events on the panel's
//     onclick, since a11y rules require an interactive div to carry ITS OWN
//     keydown handler. What shipped binds handleKeydown to both nodes, so a
//     real Escape keypress on the focused panel fires handleKeydown twice
//     per press -- harmless, since every close function here is idempotent;
//     see #0094's Implementation notes for the executed proof.)
// The one thing both shapes have in common, and the only thing worth
// asserting structurally without re-deriving each component's specific
// close function, is the ABSENCE of the blind-stopPropagation shape.
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

// Mirrors CampaignSendDialog.structuralGuard.test.ts's attrExpression: an
// Attribute's `.value` is either an array with one ExpressionTag, or (probed
// directly against real parsed output) a bare ExpressionTag object.
function attrExpr(attr: SvelteNode | undefined): SvelteNode | undefined {
  const v = attr?.value;
  if (v && typeof v === 'object' && !Array.isArray(v) && (v as SvelteNode).type === 'ExpressionTag') {
    return (v as SvelteNode).expression as SvelteNode;
  }
  if (Array.isArray(v) && v.length === 1 && (v[0] as SvelteNode)?.type === 'ExpressionTag') {
    return (v[0] as SvelteNode).expression as SvelteNode;
  }
  return undefined;
}

// True exactly for `(e) => e.stopPropagation()` (concise body) or
// `(e) => { e.stopPropagation(); }` (block body, single statement) -- the
// shape this issue is about. Anything else (no handler at all, a handler
// that also checks the key, a handler with additional statements) is fine.
function isBlindStopPropagation(expr: SvelteNode | undefined): boolean {
  if (!expr || expr.type !== 'ArrowFunctionExpression') return false;
  const params = expr.params as SvelteNode[] | undefined;
  const param = params?.[0];
  if (param?.type !== 'Identifier') return false;
  const paramName = param.name as string;

  const isStopPropCall = (n: SvelteNode | undefined): boolean => {
    if (!n || n.type !== 'CallExpression') return false;
    const args = n.arguments as SvelteNode[] | undefined;
    if (args && args.length > 0) return false;
    const callee = n.callee as SvelteNode | undefined;
    return (
      callee?.type === 'MemberExpression' &&
      (callee.object as SvelteNode | undefined)?.type === 'Identifier' &&
      ((callee.object as SvelteNode).name as string) === paramName &&
      (callee.property as SvelteNode | undefined)?.type === 'Identifier' &&
      ((callee.property as SvelteNode).name as string) === 'stopPropagation'
    );
  };

  const body = expr.body as SvelteNode;
  if (body.type === 'CallExpression') return isStopPropCall(body);
  if (body.type === 'BlockStatement') {
    const stmts = (body.body as SvelteNode[] | undefined) ?? [];
    if (stmts.length !== 1) return false;
    const stmt = stmts[0];
    if (stmt.type !== 'ExpressionStatement') return false;
    return isStopPropCall(stmt.expression as SvelteNode | undefined);
  }
  return false;
}

interface Violation {
  file: string;
  line: number;
  ariaLabel: string;
}

function scanSvelteForBlindStopPropagation(fileName: string, source: string): Violation[] {
  const violations: Violation[] = [];
  const ast = parseSvelte(source, { filename: fileName, modern: true }) as unknown as SvelteNode;
  const lineOf = (offset: number): number => source.slice(0, offset).split('\n').length;

  walk(ast.fragment, (n) => {
    if (n.type !== 'RegularElement') return;
    const roleAttr = findAttr(n, 'role');
    const ariaModalAttr = findAttr(n, 'aria-modal');
    if (!attrTextEquals(roleAttr, 'dialog')) return;
    if (!attrTextEquals(ariaModalAttr, 'true')) return;

    const onkeydownAttr = findAttr(n, 'onkeydown');
    const expr = attrExpr(onkeydownAttr);
    if (isBlindStopPropagation(expr)) {
      const labelAttr = findAttr(n, 'aria-label');
      const start = n.start as number | undefined;
      violations.push({
        file: fileName,
        line: start !== undefined ? lineOf(start) : 0,
        ariaLabel: (labelAttr && (labelAttr.value as SvelteNode[] | undefined)?.[0]?.type === 'Text'
          ? ((labelAttr.value as SvelteNode[])[0] as SvelteNode).data as string
          : '(no aria-label)'),
      });
    }
  });

  return violations;
}

const SOURCE_FILES = import.meta.glob('../views/**/*.svelte', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

function toRepoRelativePath(globKey: string): string {
  // Every key from this glob starts with "../views/" (relative to this
  // file's own directory, web/src/lib/) since the glob root is fixed.
  return `web/src/views/${globKey.slice('../views/'.length)}`;
}

describe('modal Escape guard (#0120): no role="dialog" panel blindly eats keydown', () => {
  it('no admin modal panel has onkeydown={(e) => e.stopPropagation()}', () => {
    const violations: Violation[] = [];
    for (const [path, source] of Object.entries(SOURCE_FILES)) {
      if (path.endsWith('.test.ts')) continue;
      const rel = toRepoRelativePath(path);
      violations.push(...scanSvelteForBlindStopPropagation(rel, source));
    }

    if (violations.length > 0) {
      const detail = violations
        .map((v) => `  ${v.file}:${v.line}: aria-label="${v.ariaLabel}"`)
        .join('\n');
      throw new Error(
        `modal panel(s) blindly stopPropagation() on every keydown, which eats Escape before it can bubble to the backdrop's own handler (#0120):\n${detail}`,
      );
    }
    expect(violations).toHaveLength(0);
  });
});

describe('scanSvelteForBlindStopPropagation (synthetic fixtures)', () => {
  it('catches the exact pre-#0120 shape: concise-body onkeydown={(e) => e.stopPropagation()}', () => {
    const src = `
<div role="presentation" onclick={close}>
  <div role="dialog" aria-modal="true" aria-label="Example" onkeydown={(e) => e.stopPropagation()}>
    hi
  </div>
</div>`;
    const found = scanSvelteForBlindStopPropagation('fixture.svelte', src);
    expect(found).toHaveLength(1);
    expect(found[0].ariaLabel).toBe('Example');
  });

  it('catches the block-body variant: onkeydown={(e) => { e.stopPropagation(); }}', () => {
    const src = `
<div role="dialog" aria-modal="true" onkeydown={(e) => { e.stopPropagation(); }}>hi</div>`;
    const found = scanSvelteForBlindStopPropagation('fixture.svelte', src);
    expect(found).toHaveLength(1);
  });

  it('does not catch a panel with no onkeydown attribute at all (letting the key bubble)', () => {
    const src = `<div role="dialog" aria-modal="true" onclick={(e) => e.stopPropagation()}>hi</div>`;
    expect(scanSvelteForBlindStopPropagation('fixture.svelte', src)).toHaveLength(0);
  });

  it('does not catch a panel whose onkeydown actually checks the key', () => {
    const src = `<div role="dialog" aria-modal="true" onkeydown={(e) => { if (e.key === 'Escape') close(); }}>hi</div>`;
    expect(scanSvelteForBlindStopPropagation('fixture.svelte', src)).toHaveLength(0);
  });

  it('does not catch a panel whose onkeydown calls the shared isModalEscape helper', () => {
    const src = `<div role="dialog" aria-modal="true" onkeydown={(e) => { if (isModalEscape(e)) close(); }}>hi</div>`;
    expect(scanSvelteForBlindStopPropagation('fixture.svelte', src)).toHaveLength(0);
  });

  it('does not catch e.stopPropagation() on an element that is not role="dialog"', () => {
    const src = `<div role="presentation" onkeydown={(e) => e.stopPropagation()}>hi</div>`;
    expect(scanSvelteForBlindStopPropagation('fixture.svelte', src)).toHaveLength(0);
  });

  it('does not catch a role="dialog" element missing aria-modal="true"', () => {
    const src = `<div role="dialog" onkeydown={(e) => e.stopPropagation()}>hi</div>`;
    expect(scanSvelteForBlindStopPropagation('fixture.svelte', src)).toHaveLength(0);
  });

  it('does not catch stopPropagation on a DIFFERENT event param name', () => {
    // Still the same bug shape in substance, but this guard identifies the
    // handler by its own parameter binding stopPropagation to ITSELF, so a
    // differently-named param is handled correctly too (positive case) --
    // this fixture checks the negative: calling some OTHER identifier's
    // stopPropagation is not a false catch.
    const src = `<div role="dialog" aria-modal="true" onkeydown={(evt) => other.stopPropagation()}>hi</div>`;
    expect(scanSvelteForBlindStopPropagation('fixture.svelte', src)).toHaveLength(0);
  });
});
