// #0181: the same defect — an internal document citation (CLAUDE.md, PRD,
// or a tracker issue number) leaking into copy an admin actually reads —
// has been found three separate times on three separate axes: #0172 swept
// this directory (web/), #0175's review swept Go strings that render
// verbatim, and #0178's review swept backwards from the SPA. Each sweep
// was thorough within its method and blind outside it.
//
// This is the web/ half of the guard (see
// internal/handlers/citation_guard_test.go for the Go half — CLAUDE.md §9
// and #0172's two original findings, both in Svelte, mean a Go-only guard
// would not have caught the case that started this). It fails if any
// string literal or static template text under web/src — outside a
// comment, and outside a test/type-declaration file — cites CLAUDE.md, a
// PRD section, or a tracker issue number.
//
// Parser-based, not a text grep: comments are structurally excluded rather
// than allowlisted, which is what lets the same citations stay correct and
// wanted *in* comments (#0172 deliberately moved them there).
//   - .ts files: the TypeScript compiler's own AST (already a devDependency
//     via svelte-check) — comments are trivia, never part of a
//     StringLiteral/NoSubstitutionTemplateLiteral/TemplateHead/Middle/Tail
//     node's .text, so walking only those node kinds excludes them for
//     free.
//   - .svelte files: svelte/compiler's parse({ modern: true }) AST.
//     Rendered text lives in `fragment` Text nodes and in the `instance`
//     script's ESTree Literal/TemplateElement nodes; HTML comments are a
//     distinct `Comment` node type in the fragment tree and JS comments in
//     the instance script are collected separately on ast.comments, so
//     neither is ever visited by collectByType below.
//
// CAN-SPAM §7704 and the EMAIL_REPLY_TO / EMAIL_LIST_DOMAIN env var names
// need no special-casing: neither matches citationPattern, so both pass by
// construction. See the "exclusions" describe block below for a direct,
// synthetic-source proof of both properties plus the comment exclusion,
// and the "catches" block for proof the guard actually fires.
import { describe, it, expect } from 'vitest';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, dirname, relative } from 'node:path';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';
import { parse as parseSvelte } from 'svelte/compiler';

// The pattern requires a word boundary after the three digits so it cannot
// match inside e.g. a hex color (app.css's --series-6: #008300 style
// values, if one ever ended up in a .ts token file, would contain "#008"
// but "\b" fails between the '8' and the '3' that follows it).
const citationPattern = /CLAUDE\.md|PRD §|#0\d{3}\b/;

interface Violation {
  file: string;
  line: number;
  text: string;
}

function scanTsSource(fileName: string, source: string): Violation[] {
  const sourceFile = ts.createSourceFile(fileName, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const violations: Violation[] = [];

  function visit(node: ts.Node) {
    let text: string | undefined;
    switch (node.kind) {
      case ts.SyntaxKind.StringLiteral:
      case ts.SyntaxKind.NoSubstitutionTemplateLiteral:
      case ts.SyntaxKind.TemplateHead:
      case ts.SyntaxKind.TemplateMiddle:
      case ts.SyntaxKind.TemplateTail:
        text = (node as ts.LiteralLikeNode).text;
        break;
    }
    if (text !== undefined && citationPattern.test(text)) {
      const { line } = sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile));
      violations.push({ file: fileName, line: line + 1, text });
    }
    ts.forEachChild(node, visit);
  }
  visit(sourceFile);
  return violations;
}

// collectByType walks a plain-object AST (svelte/compiler's output, or one
// of its ESTree-shaped subtrees) and returns every node whose `type` field
// is in `types`. It does not recurse past a matched node's own value
// fields we care about, but it does keep walking the whole tree otherwise
// — deliberately not selective, since the only thing that determines
// whether a node is "read" is its `type`, and non-matching types (crucially
// including `Comment`) are simply never reported.
function collectByType(node: unknown, types: Set<string>, out: Record<string, unknown>[] = [], seen = new Set<unknown>()): Record<string, unknown>[] {
  if (node === null || typeof node !== 'object') return out;
  if (seen.has(node)) return out; // guard against any accidental cycle
  seen.add(node);

  if (Array.isArray(node)) {
    for (const item of node) collectByType(item, types, out, seen);
    return out;
  }

  const obj = node as Record<string, unknown>;
  if (typeof obj.type === 'string' && types.has(obj.type)) {
    out.push(obj);
  }
  for (const key of Object.keys(obj)) {
    if (key === 'parent') continue; // avoid the parent-pointer cycle
    collectByType(obj[key], types, out, seen);
  }
  return out;
}

function scanSvelteSource(fileName: string, source: string): Violation[] {
  const violations: Violation[] = [];
  const ast = parseSvelte(source, { filename: fileName, modern: true }) as unknown as Record<string, unknown>;

  const lineOf = (offset: number): number => source.slice(0, offset).split('\n').length;

  // Rendered template text — Comment nodes are a distinct type and are
  // never in this set, so HTML comments are excluded structurally.
  for (const n of collectByType(ast.fragment, new Set(['Text']))) {
    const data = n.data as string | undefined;
    const start = n.start as number | undefined;
    if (data && citationPattern.test(data)) {
      violations.push({ file: fileName, line: start !== undefined ? lineOf(start) : 0, text: data });
    }
  }

  // The instance (<script>) and module (<script module>) blocks, walked
  // as plain ESTree trees. Literal/TemplateElement only — JS comments live
  // on ast.comments, a separate array, never inside these nodes.
  for (const root of [ast.instance, ast.module]) {
    if (!root) continue;
    for (const n of collectByType((root as Record<string, unknown>).content, new Set(['Literal', 'TemplateElement']))) {
      let value: string | undefined;
      if (n.type === 'Literal' && typeof n.value === 'string') {
        value = n.value;
      } else if (n.type === 'TemplateElement') {
        const v = n.value as { cooked?: string; raw?: string } | undefined;
        value = v?.cooked ?? v?.raw;
      }
      const start = n.start as number | undefined;
      if (value && citationPattern.test(value)) {
        violations.push({ file: fileName, line: start !== undefined ? lineOf(start) : 0, text: value });
      }
    }
  }

  return violations;
}

const THIS_FILE = fileURLToPath(import.meta.url);
const SRC_ROOT = dirname(dirname(THIS_FILE)); // web/src/lib/.. -> web/src

function walk(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const path = join(dir, entry);
    const stat = statSync(path);
    if (stat.isDirectory()) {
      walk(path, out);
      continue;
    }
    if (path === THIS_FILE) continue;
    if (entry.endsWith('.test.ts') || entry.endsWith('.d.ts')) continue;
    if (entry.endsWith('.ts') || entry.endsWith('.svelte')) out.push(path);
  }
  return out;
}

describe('citation guard (web/src)', () => {
  it('no source file contains an admin-facing string that cites CLAUDE.md, PRD, or a tracker issue', () => {
    const violations: Violation[] = [];
    for (const path of walk(SRC_ROOT)) {
      const source = readFileSync(path, 'utf8');
      const rel = relative(SRC_ROOT, path);
      const found = path.endsWith('.svelte') ? scanSvelteSource(rel, source) : scanTsSource(rel, source);
      violations.push(...found);
    }

    if (violations.length > 0) {
      const detail = violations.map((v) => `  ${v.file}:${v.line}: ${JSON.stringify(v.text)}`).join('\n');
      throw new Error(
        `admin-facing string(s) cite an internal document an admin cannot read — move the citation to a code comment beside the string (see #0172, #0175, #0178, #0181):\n${detail}`,
      );
    }
    expect(violations).toHaveLength(0);
  });
});

describe('citation guard exclusions (synthetic fixtures)', () => {
  it('does not trip on a code comment citing PRD §/CLAUDE.md/an issue number', () => {
    const src = `
      // This comment cites PRD §6.6, CLAUDE.md §9, and #0181 — all fine.
      export function ok(): string {
        return 'nothing to see here';
      }
    `;
    expect(scanTsSource('fixture.ts', src)).toHaveLength(0);
  });

  it('does not trip on CAN-SPAM §7704', () => {
    const src = `export const msg = 'Physical mailing address is not set. CAN-SPAM §7704 requires it in every campaign email.';`;
    expect(scanTsSource('fixture.ts', src)).toHaveLength(0);
  });

  it('does not trip on the EMAIL_REPLY_TO / EMAIL_LIST_DOMAIN env var names', () => {
    const src = `
      export const a = 'Reply-To address is not configured (EMAIL_REPLY_TO).';
      export const b = 'EMAIL_LIST_DOMAIN is not configured.';
    `;
    expect(scanTsSource('fixture.ts', src)).toHaveLength(0);
  });

  it('does not trip on a hex-color-shaped string', () => {
    const src = `export const color = '#008300';`;
    expect(scanTsSource('fixture.ts', src)).toHaveLength(0);
  });

  it('does not trip on a Svelte HTML comment citing an issue number, only real text', () => {
    const src = `
<!-- #0138: no upload endpoint exists, by design -- see PRD §5.2 -->
<p>A site-relative path to an image already hosted on this site.</p>
`;
    expect(scanSvelteSource('fixture.svelte', src)).toHaveLength(0);
  });
});

describe('citation guard catches (synthetic fixtures)', () => {
  it('fires on a .ts string literal citing PRD §', () => {
    const src = `export const msg = 'Reply-To address is not configured. PRD §6.6 requires a monitored reply address, not noreply@.';`;
    const found = scanTsSource('fixture.ts', src);
    expect(found).toHaveLength(1);
    expect(found[0].text).toContain('PRD §6.6');
  });

  it('fires on a .ts template literal citing an issue number', () => {
    const src = 'export const msg = `See #0138 for why.`;';
    const found = scanTsSource('fixture.ts', src);
    expect(found).toHaveLength(1);
  });

  it('fires on rendered Svelte template text citing CLAUDE.md', () => {
    const src = `<p>This is disallowed per CLAUDE.md §9.</p>`;
    const found = scanSvelteSource('fixture.svelte', src);
    expect(found).toHaveLength(1);
  });

  it('fires on a Svelte <script> string literal citing an issue number', () => {
    const src = `
<script lang="ts">
  const hint = 'See #0138 for context.';
</script>
<p>{hint}</p>
`;
    const found = scanSvelteSource('fixture.svelte', src);
    expect(found.some((v) => v.text.includes('#0138'))).toBe(true);
  });
});
