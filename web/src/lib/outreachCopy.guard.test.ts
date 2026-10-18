// #0591: the outreach pages say "post", "link", "Announcement / Reminder /
// Last call" and "Bluesky server", never the internal words "campaign",
// "Prepare", "tracked link", "wave", "Held" or "PDS" (review L1).
//
// Method (so a false positive is a bug in this file, not in the copy). Only
// text a person can read is checked, found by parsing, not by grepping source:
//   - web/src/views/admin/Outreach*.svelte are parsed with svelte/compiler.
//     Checked: template text nodes; static attribute text except the
//     non-visible attributes in HIDDEN_ATTRS (class, id, href, ...); and every
//     string / template literal in script code, template expressions and
//     attribute expressions. HTML comments, <style>, identifiers, property
//     names and CSS classes are never visited.
//   - web/src/lib/outreach*.ts (the copy helpers) are parsed with TypeScript;
//     the same string literals are checked.
//   - A literal is skipped where it is code, not copy: an import/export
//     source, an object key, a type position, an element-access key, a switch
//     case, or either side of ===, !==, == and !=.
// The words match case-insensitively on word boundaries, so utm_campaign,
// prepareOutreachPost, wave_label and 'prepared' (an API field) do not match.
import { describe, expect, it } from 'vitest';
import { parse } from 'svelte/compiler';
import ts from 'typescript';

const svelteSources = import.meta.glob('../views/admin/Outreach*.svelte', { query: '?raw', import: 'default', eager: true }) as Record<string, string>;
const libSources = import.meta.glob(['./outreach*.ts', '!./**/*.test.ts'], { query: '?raw', import: 'default', eager: true }) as Record<string, string>;

export const BANNED = /\bcampaigns?\b|\bprepar(?:e|es|ed|ing)\b|\btracked links?\b|\bwaves?\b|\bheld\b|\bPDS\b/i;

const HIDDEN_ATTRS = new Set(['class', 'id', 'name', 'for', 'type', 'href', 'src', 'role', 'style', 'rel', 'target', 'accept', 'autocomplete', 'inputmode', 'min', 'max', 'step', 'rows', 'cols', 'method', 'action', 'tabindex', 'variant', 'size', 'layout', 'mode']);
const COMPARE = new Set(['===', '!==', '==', '!=']);

// ---- ESTree (svelte) ----
type Node = { type: string; [k: string]: unknown };
function esLiterals(node: unknown, out: string[], parent: Node | null = null, key = ''): void {
  if (Array.isArray(node)) {
    for (const n of node) esLiterals(n, out, parent, key);
    return;
  }
  if (!node || typeof node !== 'object') return;
  const n = node as Node;
  if (typeof n.type !== 'string') return;
  if (n.type.startsWith('TS')) return;
  if (n.type === 'ImportDeclaration' || n.type === 'ExportAllDeclaration') return;
  if (n.type === 'Literal' && typeof n.value === 'string') {
    const skip =
      (parent?.type === 'Property' && key === 'key' && parent.computed !== true) ||
      (parent?.type === 'MemberExpression' && key === 'property') ||
      (parent?.type === 'BinaryExpression' && COMPARE.has(String(parent.operator))) ||
      (parent?.type === 'SwitchCase' && key === 'test');
    if (!skip) out.push(n.value);
    return;
  }
  if (n.type === 'TemplateElement') {
    const v = n.value as { cooked?: string };
    if (v.cooked) out.push(v.cooked);
    return;
  }
  for (const [k, v] of Object.entries(n)) {
    if (k === 'loc' || k === 'start' || k === 'end') continue;
    esLiterals(v, out, n, k);
  }
}

function svelteVisible(src: string): string[] {
  const ast = parse(src, { modern: true }) as unknown as Node;
  const out: string[] = [];
  const walk = (node: unknown): void => {
    if (Array.isArray(node)) return void node.forEach(walk);
    if (!node || typeof node !== 'object') return;
    const n = node as Node;
    switch (n.type) {
      case 'Text':
        out.push(String(n.data));
        return;
      case 'Comment':
        return;
      case 'Attribute': {
        const parts = n.value === true ? [] : Array.isArray(n.value) ? (n.value as Node[]) : [n.value as Node];
        for (const p of parts) {
          if (p.type === 'Text') {
            if (!HIDDEN_ATTRS.has(String(n.name)) && !String(n.name).startsWith('data-')) out.push(String(p.data));
          } else if (p.type === 'ExpressionTag') esLiterals(p.expression, out);
        }
        return;
      }
      case 'ExpressionTag':
      case 'RenderTag':
      case 'ConstTag':
        esLiterals(n, out);
        return;
    }
    for (const [k, v] of Object.entries(n)) {
      if (k === 'css' || k === 'loc') continue;
      if (k === 'instance' || k === 'module') {
        esLiterals((v as Node | null)?.content, out);
        continue;
      }
      // Block headers ({#if x}, {#each xs as x}) carry code: literals in them are checked too.
      if (['test', 'expression', 'argument', 'declaration', 'key'].includes(k) && v && typeof v === 'object' && !Array.isArray(v) && typeof (v as Node).type === 'string' && !['Text', 'Fragment'].includes((v as Node).type)) {
        esLiterals(v, out);
        continue;
      }
      walk(v);
    }
  };
  walk(ast);
  return out;
}

// ---- TypeScript (lib) ----
function tsVisible(src: string): string[] {
  const sf = ts.createSourceFile('x.ts', src, ts.ScriptTarget.Latest, true);
  const out: string[] = [];
  const visit = (n: ts.Node): void => {
    if (ts.isImportDeclaration(n) || ts.isExportDeclaration(n) || ts.isTypeNode(n) || ts.isInterfaceDeclaration(n) || ts.isTypeAliasDeclaration(n)) return;
    if (ts.isStringLiteral(n) || ts.isNoSubstitutionTemplateLiteral(n) || ts.isTemplateHead(n) || ts.isTemplateMiddle(n) || ts.isTemplateTail(n)) {
      const p = n.parent;
      const skip =
        (ts.isPropertyAssignment(p) && p.name === n) ||
        (ts.isElementAccessExpression(p) && p.argumentExpression === n) ||
        (ts.isBinaryExpression(p) && [ts.SyntaxKind.EqualsEqualsEqualsToken, ts.SyntaxKind.ExclamationEqualsEqualsToken, ts.SyntaxKind.EqualsEqualsToken, ts.SyntaxKind.ExclamationEqualsToken].includes(p.operatorToken.kind)) ||
        ts.isCaseClause(p) ||
        ts.isEnumMember(p);
      if (!skip) out.push(n.text);
      return;
    }
    ts.forEachChild(n, visit);
  };
  visit(sf);
  return out;
}

const hits = (strings: string[]): string[] => strings.map((s) => s.trim()).filter((s) => BANNED.test(s));

describe('outreach copy has no internal jargon (#0591)', () => {
  it('scans a non-trivial number of files', () => {
    expect(Object.keys(svelteSources).length).toBeGreaterThanOrEqual(20);
    expect(Object.keys(libSources).length).toBeGreaterThanOrEqual(8);
  });

  it('no Outreach*.svelte shows a banned word', () => {
    const offenders = Object.entries(svelteSources).flatMap(([path, src]) => hits(svelteVisible(src)).map((s) => `${path}: ${s}`));
    expect(offenders).toEqual([]);
  });

  it('no lib/outreach*.ts copy helper produces a banned word', () => {
    const offenders = Object.entries(libSources).flatMap(([path, src]) => hits(tsVisible(src)).map((s) => `${path}: ${s}`));
    expect(offenders).toEqual([]);
  });

  it('the detector flags what it is written for, and only that', () => {
    const svelte = (body: string) => hits(svelteVisible(body));
    expect(svelte('<p>Tracked link created.</p>')).toHaveLength(1);
    expect(svelte('<button aria-label="Prepare the post">x</button>')).toHaveLength(1);
    expect(svelte('<input placeholder="the PDS URL">')).toHaveLength(1);
    expect(svelte('<script lang="ts">let n = $state(1); const s = "Held for review"; </script><p>{s}</p>')).toHaveLength(1);
    expect(svelte('<p>{ok ? "a wave goes out" : "b"}</p>')).toHaveLength(1);
    // Not visible, or not copy.
    expect(svelte('<p class="wave campaign">ok</p>')).toEqual([]);
    expect(svelte('<!-- a wave of campaign copy --><p>ok</p>')).toEqual([]);
    expect(svelte('<style>.wave { color: red; }</style><p>ok</p>')).toEqual([]);
    expect(svelte('<script lang="ts">const w = post.wave === "wave"; const x = { wave: 1 }; prepareOutreachPost(1);</script><p>ok</p>')).toEqual([]);
    expect(svelte('<p>{chip.label}</p><td>{row.wave_label}</td>')).toEqual([]);
    expect(hits(tsVisible("export const a = 'utm_campaign'; const b = { wave: 'x' }; if (p.wave === 'wave') {}"))).toEqual([]);
    expect(hits(tsVisible("export const a = 'There is no time for this wave.';"))).toHaveLength(1);
    expect(hits(tsVisible('export const a = `Held: ${x}`;'))).toHaveLength(1);
  });
});
