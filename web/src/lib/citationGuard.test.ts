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
import ts from 'typescript';
import { parse as parseSvelte } from 'svelte/compiler';

// The pattern requires a word boundary after the three digits so it cannot
// match inside e.g. a hex color (app.css's --series-6: #008300 style
// values, if one ever ended up in a .ts token file, would contain "#008"
// but "\b" fails between the '8' and the '3' that follows it).
//
// #0193: "PRD\s*§" used to mean two different things in the two guards.
// JavaScript's \s is Unicode-aware by spec (ECMA-262's WhiteSpace and
// LineTerminator productions) regardless of the u/v flag: it matches tab,
// newline, vertical tab, form feed, carriage return, regular space, NBSP
// (U+00A0), Ogham space mark (U+1680), the eleven general-punctuation
// spaces U+2000-U+200A (including thin space, U+2009), the line/paragraph
// separators U+2028 and U+2029, narrow no-break space (U+202F), medium
// mathematical space (U+205F), ideographic space (U+3000), and ZWNBSP/BOM
// (U+FEFF). Go's regexp/syntax \s is exactly [\t\n\f\r ] — five ASCII
// characters, and notably not even \v. So a non-breaking space before "§"
// — exactly what pasting out of a word processor produces — was caught
// here and silently missed by the Go half, the half that covers handler
// code (#0187's phase-3 review, item 1; filed as #0193).
//
// Decided: widen Go to match this half, rather than narrow both to a
// smaller explicit set. This guard's coverage was already correct and
// live; narrowing it to make the comparison easier would be removing real
// coverage, not fixing a bug. The class below spells out, by codepoint,
// every character this engine's \s matches, so this pattern and the Go
// guard's (internal/handlers/citation_guard_test.go) now say the identical
// thing rather than one leaning on V8's \s and the other on regexp/syntax's
// — verified by running both patterns over the same codepoint cases (see
// the whitespace-shape tests in this file and in citation_guard_test.go).
// "PRD followed by whitespace then §" (rather than a single literal space)
// originally closed the "PRD  §6.6" (two spaces) and "PRD§6.6" (no space)
// variants (#0181 review, pass 2); the explicit class still closes both,
// plus everything listed above. Issues.md, HANDOFF.md, and docs/*.md join
// CLAUDE.md and PRD § as documents an admin cannot read either — #0178's
// own whole-tree grep already included Issues.md and HANDOFF, so there is
// no principled reason to guard one internal document and not the others.
//
// #0187, item 2: issues/NNNN.md (a path to one tracker file, e.g.
// "issues/0138.md") was missing even though docs/*.md was added — an
// asymmetry, not a decision, since a tracker-file path is the least
// readable citation of the whole set to an admin. issues\/\d{4}\.md
// closes it; the repo's own #0NNN convention already guarantees the
// four-digit form.
//
// #0187, item 3: docs/\S*\.md (and issues\/\d{4}\.md) also fire on an
// external URL that happens to contain a matching path segment, e.g.
// "https://example.com/docs/guide.md". Decided: accepted rather than
// tightened (see "documents the accepted docs/*.md external-URL false
// positive" below, which pins it).
//
// #0193 corrects the reasoning recorded here for that decision. It used to
// say tightening this side would leave the two guards silently diverged,
// since the Go half's RE2 engine has no lookbehind and couldn't mirror a
// (?<!\w+:\/\/\S*) exclusion. RE2 lacking lookbehind is true; the
// inference is false — a scheme exclusion does not need lookbehind, only a
// consumed leading boundary, e.g. `(?:^|[^:/\w])docs/\S*\.md`, which
// compiles and matches correctly in both V8 and RE2 verbatim (#0187's
// phase-3 review built and ran it, 13/13 on intent, in both engines — not
// adopted here; #0193 reports it as a filing candidate rather than taking
// on that change). The actual reason to accept, once that claim falls
// away, is what the review gave: the false positive is nil in the tree
// today, and the cost of tightening the pattern is real against that nil
// risk. It is not "tightening would diverge the two guards" — the
// PRD-whitespace fix above exists precisely because the two guards were
// already diverged on a different term, so "the guards must share one
// pattern shape" was never a fact this decision could rest on. The false
// positive is also structurally unlikely to appear: CLAUDE.md §9 forbids
// external CDNs, and no docs/*.md-shaped string exists anywhere in
// web/src outside comments (#0181 review, pass 2).
const citationPattern = /CLAUDE\.md|PRD[\t\n\v\f\r \u00A0\u1680\u2000-\u200A\u2028\u2029\u202F\u205F\u3000\uFEFF]*§|#0\d{3}\b|Issues\.md|HANDOFF\.md|docs\/\S*\.md|issues\/\d{4}\.md/;

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

// extractLiteralValue reads the string a Text/Literal/TemplateElement node
// carries. Shared between the fragment walk and the script walk so both
// extract identically.
function extractLiteralValue(n: Record<string, unknown>): string | undefined {
  if (n.type === 'Text') {
    return n.data as string | undefined;
  }
  if (n.type === 'Literal' && typeof n.value === 'string') {
    return n.value;
  }
  if (n.type === 'TemplateElement') {
    const v = n.value as { cooked?: string; raw?: string } | undefined;
    return v?.cooked ?? v?.raw;
  }
  return undefined;
}

function scanSvelteSource(fileName: string, source: string): Violation[] {
  const violations: Violation[] = [];
  const ast = parseSvelte(source, { filename: fileName, modern: true }) as unknown as Record<string, unknown>;

  const lineOf = (offset: number): number => source.slice(0, offset).split('\n').length;

  // Rendered template text, AND string/template literals nested inside a
  // markup expression ({'...'}, {cond ? '...' : ''}, {`...`}, {@html '...'}).
  // #0181 review, pass 2 (the bounce): an ExpressionTag in the markup
  // carries its own ESTree subtree, which belongs to the fragment tree, not
  // the instance script — the original scan collected only Text from
  // ast.fragment, so a citation reachable only through a template
  // expression was never seen. Collecting Literal/TemplateElement here too
  // closes that gap; Comment nodes remain a distinct type never in this
  // set, so HTML comments stay excluded structurally.
  for (const n of collectByType(ast.fragment, new Set(['Text', 'Literal', 'TemplateElement']))) {
    const value = extractLiteralValue(n);
    const start = n.start as number | undefined;
    if (value && citationPattern.test(value)) {
      violations.push({ file: fileName, line: start !== undefined ? lineOf(start) : 0, text: value });
    }
  }

  // The instance (<script>) and module (<script module>) blocks, walked
  // as plain ESTree trees. Literal/TemplateElement only — JS comments live
  // on ast.comments, a separate array, never inside these nodes.
  for (const root of [ast.instance, ast.module]) {
    if (!root) continue;
    for (const n of collectByType((root as Record<string, unknown>).content, new Set(['Literal', 'TemplateElement']))) {
      const value = extractLiteralValue(n);
      const start = n.start as number | undefined;
      if (value && citationPattern.test(value)) {
        violations.push({ file: fileName, line: start !== undefined ? lineOf(start) : 0, text: value });
      }
    }
  }

  return violations;
}

// #0181 review, pass 2: import.meta.glob replaces the readdirSync/statSync
// file walk. It is strictly narrower — it needed no @types/node dependency
// and no "node" entry in tsconfig.json's types array (both reverted in this
// pass; vite/client already declares import.meta.glob) — and it deletes the
// walk/fileURLToPath machinery outright. Keys are paths relative to *this
// file's own directory* (web/src/lib/): a file in that same directory
// yields "./foo.ts", and a file reached by going up first (everything else
// under web/src) yields "../App.svelte" or
// "../views/admin/WorkshopEditor.svelte" — measured directly rather than
// assumed, since Vite's own docs example only shows the single-prefix
// case. { eager: true } resolves all matches at module-load time so this
// is a plain object, not a set of loader functions.
const SOURCE_FILES = import.meta.glob('../**/*.{ts,svelte}', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

// #0187, item 4: the failure message exists to be pasted into a search, so
// every reported path must be one consistent form. The pre-#0187 code only
// stripped a leading "../", which left the two glob-key shapes above
// printing inconsistently — "./branding.ts" (a file in web/src/lib/,
// untouched by the strip) alongside "views/admin/WorkshopEditor.svelte" (a
// file reached via "../", stripped down to no prefix at all). Neither of
// those is a path anyone could paste into a repo-rooted search or `grep`
// invocation unambiguously. toRepoRelativePath produces one form for both:
// repo-relative from the checkout root, e.g. "web/src/lib/branding.ts" and
// "web/src/views/admin/WorkshopEditor.svelte" — matching the form the Go
// half's own file paths are unambiguous in (absolute), just shorter and
// pasteable without a machine-specific prefix.
function toRepoRelativePath(globKey: string): string {
  const fromSrc = globKey.startsWith('../') ? globKey.slice(3) : `lib/${globKey.slice(2)}`;
  return `web/src/${fromSrc}`;
}

describe('citation guard (web/src)', () => {
  it('no source file contains an admin-facing string that cites CLAUDE.md, PRD, or a tracker issue', () => {
    const violations: Violation[] = [];
    for (const [path, source] of Object.entries(SOURCE_FILES)) {
      if (path.endsWith('.test.ts') || path.endsWith('.d.ts')) continue;
      const rel = toRepoRelativePath(path);
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

  it('does not trip on a code comment citing Issues.md, HANDOFF.md, docs/*.md, or issues/NNNN.md', () => {
    const src = `
      // See Issues.md for status, HANDOFF.md for environment notes,
      // docs/architecture.md for the package map, and issues/0138.md for
      // the original report.
      export function ok(): string {
        return 'nothing to see here';
      }
    `;
    expect(scanTsSource('fixture.ts', src)).toHaveLength(0);
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

  // #0181 review, pass 2: the four template-expression shapes the bounce
  // named explicitly — a string literal expression, a ternary, a template
  // literal, and {@html ...} — none of these tripped the pre-fix scanner
  // because the ExpressionTag's ESTree subtree lives in the fragment tree,
  // which the scan only walked for Text nodes.
  it('fires on a string literal expression: {\'...\'}', () => {
    const src = `<p>{'see PRD §6.6'}</p>`;
    const found = scanSvelteSource('fixture.svelte', src);
    expect(found.some((v) => v.text.includes('PRD §6.6'))).toBe(true);
  });

  it('fires on a ternary expression citing CLAUDE.md', () => {
    const src = `<script lang="ts">let cond = true;</script>\n<p>{cond ? 'see CLAUDE.md §9' : ''}</p>`;
    const found = scanSvelteSource('fixture.svelte', src);
    expect(found.some((v) => v.text.includes('CLAUDE.md'))).toBe(true);
  });

  it('fires on a template literal expression citing an issue number', () => {
    const src = '<p>{`see #0138`}</p>';
    const found = scanSvelteSource('fixture.svelte', src);
    expect(found.some((v) => v.text.includes('#0138'))).toBe(true);
  });

  it('fires on {@html \'...\'} citing PRD §', () => {
    const src = `{@html 'see PRD §6.6'}`;
    const found = scanSvelteSource('fixture.svelte', src);
    expect(found.some((v) => v.text.includes('PRD §6.6'))).toBe(true);
  });

  it('fires on Issues.md, HANDOFF.md, and docs/*.md citations', () => {
    expect(scanTsSource('fixture.ts', `export const a = 'see Issues.md for status';`).length).toBe(1);
    expect(scanTsSource('fixture.ts', `export const b = 'see HANDOFF.md for context';`).length).toBe(1);
    expect(scanTsSource('fixture.ts', `export const c = 'see docs/architecture.md';`).length).toBe(1);
  });

  // #0187, item 2: issues/NNNN.md was the one form of "internal document an
  // admin cannot read" missing from the pattern even though docs/*.md was
  // added — the least readable citation of the set, and the asymmetry was
  // not deliberate.
  it('fires on an issues/NNNN.md citation', () => {
    const found = scanTsSource('fixture.ts', `export const a = 'see issues/0138.md for the acceptance criteria';`);
    expect(found).toHaveLength(1);
    expect(found[0].text).toContain('issues/0138.md');
  });

  // #0187, item 3: documents the decision to accept, rather than tighten,
  // the one false-positive shape docs/\S*\.md (and issues\/\d{4}\.md)
  // introduces — an external URL containing a matching path segment. See
  // the comment above citationPattern for the full reasoning: the false
  // positive is nil in the tree today and the cost of tightening is real
  // against that nil risk — not, as an earlier version of this comment
  // said, that only V8 could express a scheme exclusion (#0193 corrected
  // that; a consumed-boundary form is valid in both engines, just not
  // adopted here). This test exists so a future change that silently
  // closes or widens the gap shows up in a diff.
  it('documents the accepted docs/*.md external-URL false positive (#0187 item 3)', () => {
    const found = scanTsSource('fixture.ts', `export const a = 'see https://example.com/docs/guide.md for details';`);
    expect(found).toHaveLength(1);
  });

  // #0193: the five whitespace shapes the phase-3 review of #0187 found
  // caught here and missed by the Go guard's Go \s (NBSP, thin space, VT,
  // ZWNBSP, ideographic space), plus the rest of the codepoints this
  // engine's \s matches by spec that the Go pattern was widened to include.
  // Built from codepoints via String.fromCodePoint, not pasted characters,
  // so what the test asserts is provably what it contains.
  it('fires on every whitespace shape JavaScript\'s \\s matches between PRD and §', () => {
    const section = String.fromCodePoint(0x00a7); // §
    // Two of these codepoints (LF, CR) are LineTerminator characters that
    // ECMA-262 forbids raw and unescaped inside a single-quoted string
    // literal -- embedding them directly as characters would make the
    // *fixture* itself invalid source, not exercise the pattern. A
    // \uXXXX escape sequence, built here from a codepoint (never pasted as
    // a literal character), is valid for every one of these codepoints
    // regardless of category, so the fixture is always well-formed and
    // uniform across all 17 cases.
    const backslash = String.fromCharCode(92);
    const uEscape = (cp: number) => backslash + 'u' + cp.toString(16).toUpperCase().padStart(4, '0');

    const mustMatch: [string, number][] = [
      ['NBSP', 0x00a0],
      ['thin space', 0x2009],
      ['vertical tab', 0x000b],
      ['ZWNBSP/BOM', 0xfeff],
      ['ideographic space', 0x3000],
      ['ogham space mark', 0x1680],
      ['line separator', 0x2028],
      ['paragraph separator', 0x2029],
      ['narrow no-break space', 0x202f],
      ['medium mathematical space', 0x205f],
      ['en quad', 0x2000],
      ['hair space', 0x200a],
      ['tab', 0x0009],
      ['newline', 0x000a],
      ['carriage return', 0x000d],
      ['form feed', 0x000c],
      ['regular space', 0x0020],
    ];
    for (const [name, cp] of mustMatch) {
      const src = `export const a = 'PRD${uEscape(cp)}§6.6';`;
      const found = scanTsSource('fixture.ts', src);
      expect(found, `expected a match for PRD + ${name} (U+${cp.toString(16).toUpperCase().padStart(4, '0')}) + section 6.6, fixture: ${src}`).toHaveLength(1);
      expect(found[0]?.text).toBe(`PRD${String.fromCodePoint(cp)}${section}6.6`);
    }

    expect(scanTsSource('fixture.ts', `export const a = 'PRD${section}6.6';`)).toHaveLength(1);
    expect(scanTsSource('fixture.ts', `export const a = 'PRD  ${section}6.6';`)).toHaveLength(1);
    expect(scanTsSource('fixture.ts', `export const a = 'PRDA${section}6.6';`)).toHaveLength(0);
  });
});
