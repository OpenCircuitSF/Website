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
//
// #0194: the else branch above assumed every non-"../" key had a "./"
// prefix and unconditionally sliced off two characters (`globKey.slice(2)`).
// Vite emits none today — the 83-key measurement in #0187's phase-3 review
// found exactly two prefix shapes, "../" and "./" — but a bare key (no
// prefix at all, which import.meta.glob would emit for a file glob-matched
// in its own directory under some Vite configurations) would have two of
// its own characters mangled off the front by that slice rather than
// reported as-is. toRepoRelativePath now checks for "./" explicitly and
// falls back to treating an unprefixed key as already relative to this
// file's directory (web/src/lib/), matching what "./" means, instead of
// slicing blind.
function toRepoRelativePath(globKey: string): string {
  if (globKey.startsWith('../')) {
    return `web/src/${globKey.slice(3)}`;
  }
  if (globKey.startsWith('./')) {
    return `web/src/lib/${globKey.slice(2)}`;
  }
  return `web/src/lib/${globKey}`;
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

// #0198: the two guards' whitespace classes are proven equivalent in
// *meaning* by #0193's exhaustive sweep, but nothing keeps them in step —
// editing either citationPattern's whitespace class does not fail the
// other guard's tests. WHITESPACE_MUST_MATCH/WHITESPACE_MUST_NOT_MATCH
// below and the "citation guard whitespace class stays coupled with the Go
// guard" describe block, plus their counterparts in
// internal/handlers/citation_guard_test.go (whitespaceClassMustMatch /
// whitespaceClassMustNotMatch and
// TestCitationPatternWhitespaceClassStaysCoupledWithWebGuard), close that
// gap the way #0198's notes recommend: not by comparing pattern text (the
// two are legitimately spelled differently — \u00A0 here, \x{00A0}
// there — and always will be) and not by generating one pattern from the
// other (RE2 and V8 read different escape and range syntaxes even for an
// identical class, and there is no third file in this issue's scope to host
// a codegen step), but by asserting BOTH shipped citationPatterns, each in
// its own real engine — V8 here, Go's regexp/RE2 there — against ONE
// identical canonical codepoint list.
//
// That list is #0193's phase-3 review, already exhaustive: sweeping all of
// U+0000-U+10FFFF against Go's regexp, V8's own \s, and this pattern found
// exactly 25 codepoints in the accepted set and named 10 specific
// near-misses that a hand-maintained class most plausibly drifts to
// (U+180E in particular — Zs before Unicode 6.3, Cf since). Reusing that
// derived set here, rather than re-deriving a fresh one, is deliberate: the
// two files independently passing against the SAME hardcoded list is what
// makes the coupling structural rather than conventional. If a future edit
// narrows or widens either citationPattern's whitespace class, one of
// WHITESPACE_MUST_MATCH or WHITESPACE_MUST_NOT_MATCH stops holding for that
// pattern, and this test — in whichever file was edited — fails on its own,
// with no dependency on the other file's suite having run in the same
// process or even the same language.
//
// Codepoints are asserted directly against citationPattern (String
// concatenation via String.fromCodePoint at runtime), not by generating and
// parsing a TS source fixture — LF/CR are the only two codepoints in this
// list that cannot appear raw and unescaped inside a single-quoted TS
// string literal, and that restriction belongs to the "catches" block's
// scanTsSource fixtures above, not to a direct regex-membership test like
// this one.
const WHITESPACE_MUST_MATCH: [string, number][] = [
  ['TAB', 0x0009],
  ['LF', 0x000a],
  ['VT', 0x000b],
  ['FF', 0x000c],
  ['CR', 0x000d],
  ['SPACE', 0x0020],
  ['NBSP', 0x00a0],
  ['OGHAM SPACE MARK', 0x1680],
  ['EN QUAD', 0x2000],
  ['EM QUAD', 0x2001],
  ['EN SPACE', 0x2002],
  ['EM SPACE', 0x2003],
  ['THREE-PER-EM SPACE', 0x2004],
  ['FOUR-PER-EM SPACE', 0x2005],
  ['SIX-PER-EM SPACE', 0x2006],
  ['FIGURE SPACE', 0x2007],
  ['PUNCTUATION SPACE', 0x2008],
  ['THIN SPACE', 0x2009],
  ['HAIR SPACE', 0x200a],
  ['LINE SEPARATOR', 0x2028],
  ['PARAGRAPH SEPARATOR', 0x2029],
  ['NARROW NO-BREAK SPACE', 0x202f],
  ['MEDIUM MATHEMATICAL SPACE', 0x205f],
  ['IDEOGRAPHIC SPACE', 0x3000],
  ['ZERO WIDTH NO-BREAK SPACE / BOM', 0xfeff],
];

// WHITESPACE_MUST_NOT_MATCH is #0193's near-miss set: codepoints a naive
// Unicode-whitespace class could plausibly include but JavaScript's \s
// (and, since #0193, the Go pattern) does not. U+180E is the one that
// matters most: it was General Category Zs before Unicode 6.3 and is Cf
// now, so it is the plausible drift if either engine's bundled Unicode
// version moves.
const WHITESPACE_MUST_NOT_MATCH: [string, number][] = [
  ['NEL (NEXT LINE)', 0x0085],
  ['SOFT HYPHEN', 0x00ad],
  ['MONGOLIAN VOWEL SEPARATOR', 0x180e],
  ['ZERO WIDTH SPACE', 0x200b],
  ['ZERO WIDTH NON-JOINER', 0x200c],
  ['ZERO WIDTH JOINER', 0x200d],
  ['WORD JOINER', 0x2060],
  ['INVISIBLE PLUS', 0x2064],
  ['HANGUL FILLER', 0x3164],
  ['OBJECT REPLACEMENT CHARACTER', 0xfffc],
];

// #0205: the two lists above are the ONLY thing enforcing the canonical
// 25-accept/10-reject codepoint set — nothing else notices if an entry
// is quietly deleted from one of them. WHITESPACE_LIST_COUNTS below pins
// those lengths so that deleting a codepoint to silence a coupling failure
// (the lazy repair the coupling test's own failure message invites) fails
// this test by name instead of leaving both suites green. MATRIX_COUNTS
// below (#0187) is the precedent for pinning a list's length as a named
// constant this way rather than an inline magic number.
const WHITESPACE_LIST_COUNTS = {
  mustMatch: 25,
  mustNotMatch: 10,
};

// describe block is #0198's coupling test: it fails if citationPattern's
// whitespace class ever disagrees with the canonical 25-accept/10-reject
// codepoint list above, which is asserted verbatim (same codepoints, same
// expected outcome) against the Go guard's own citationPattern in
// internal/handlers/citation_guard_test.go. Neither file reads the other's
// source or a shared data file — the coupling is that both are pinned to
// the same hardcoded ground truth, so a one-sided edit to either whitespace
// class breaks that file's own test without needing the other suite to run.
describe('citation guard whitespace class stays coupled with the Go guard (#0198)', () => {
  it('has not had either canonical list shortened (#0205)', () => {
    expect(
      WHITESPACE_MUST_MATCH.length,
      `WHITESPACE_MUST_MATCH has ${WHITESPACE_MUST_MATCH.length} entries, want ${WHITESPACE_LIST_COUNTS.mustMatch} — the accept list has been shortened; restore the deleted codepoint instead of deleting the next one the coupling test names`,
    ).toBe(WHITESPACE_LIST_COUNTS.mustMatch);
    expect(
      WHITESPACE_MUST_NOT_MATCH.length,
      `WHITESPACE_MUST_NOT_MATCH has ${WHITESPACE_MUST_NOT_MATCH.length} entries, want ${WHITESPACE_LIST_COUNTS.mustNotMatch} — the reject list has been shortened; restore the deleted codepoint instead of deleting the next one the coupling test names`,
    ).toBe(WHITESPACE_LIST_COUNTS.mustNotMatch);
  });

  it('matches every codepoint in the canonical accepted-whitespace list', () => {
    const section = String.fromCodePoint(0x00a7); // §
    for (const [name, cp] of WHITESPACE_MUST_MATCH) {
      const s = 'PRD' + String.fromCodePoint(cp) + section + '6.6';
      expect(
        citationPattern.test(s),
        `expected a match for PRD + ${name} (U+${cp.toString(16).toUpperCase().padStart(4, '0')}) + section — this codepoint is in the canonical whitespace list shared with citation_guard_test.go's coupling test; the two guards have drifted`,
      ).toBe(true);
    }
  });

  it('rejects every codepoint in the canonical near-miss list', () => {
    const section = String.fromCodePoint(0x00a7); // §
    for (const [name, cp] of WHITESPACE_MUST_NOT_MATCH) {
      const s = 'PRD' + String.fromCodePoint(cp) + section + '6.6';
      expect(
        citationPattern.test(s),
        `expected NO match for PRD + ${name} (U+${cp.toString(16).toUpperCase().padStart(4, '0')}) + section — this near-miss codepoint must stay outside the canonical whitespace list shared with citation_guard_test.go's coupling test; the two guards have drifted`,
      ).toBe(false);
    }
  });
});

// #0194: the shape matrix that three passes each rebuilt from scratch in a
// throwaway probe and then deleted — #0181's pass-2 review (the original
// 20-24 case sweep), #0187's implementation, and #0187's phase-3 review
// (the 34-case version this table reproduces exactly). Committed here so
// the next pass runs it instead of paying an afternoon to reconstruct it,
// against the shipped scanSvelteSource, scanTsSource, and citationPattern
// declared above in this same file — not a re-export, not a replica; this
// literally calls the functions the guard itself runs. This tracker has
// hit the replica trap three times already (#0184, #0189, #0195):
// reproducing scanner logic in a probe instead of calling the shipped code
// lets the probe and the shipped code drift apart silently. Nothing here
// is reproduced — every case below is dispatched through runMatrixCase to
// one of the three shipped exports.
//
// Self-reference hazard (the same one #0181's own doc comment names for
// why the Go guard excludes _test.go): this table's fixtures are
// citation-SHAPED by construction, since proving a catch requires writing
// a string that looks exactly like what an admin must never see. That is
// safe here for two independent reasons. First, this file is
// citationGuard.test.ts — a .test.ts — and the guard's own SOURCE_FILES
// glob skips any path ending .test.ts, the same way the Go guard's walk
// skips *_test.go, so nothing below is ever scanned by the guard it is
// testing. Second, and checked rather than assumed: the guard was re-run
// against the real tree with this file in it (see #0194's
// ## Verification) and stayed green — the fixtures do not leak into the
// thing they are proving leaks get caught.
//
// Counts match #0194's acceptance criteria exactly, and MATRIX_COUNTS
// below asserts it rather than leaving it to be counted by eye: 4 bounce
// forms + 12 further Svelte shapes + 5 structural misses + 7 acceptable
// misses + 6 boundary checks = 34, the same total #0187's phase-3 review
// ran.
type MatrixCategory = 'bounce' | 'further-shape' | 'structural-miss' | 'acceptable-miss' | 'boundary';

interface MatrixCase {
  category: MatrixCategory;
  name: string;
  kind: 'svelte' | 'ts' | 'pattern';
  src: string;
  expectMatch: boolean;
  // Required (and enforced below) for every 'acceptable-miss' case: why
  // this specific miss is deliberate rather than a regression. That is
  // what lets a future pass tell "the guard still doesn't catch this, as
  // designed" from "the guard broke."
  note?: string;
}

function runMatrixCase(c: MatrixCase): boolean {
  switch (c.kind) {
    case 'svelte':
      return scanSvelteSource('fixture.svelte', c.src).length > 0;
    case 'ts':
      return scanTsSource('fixture.ts', c.src).length > 0;
    case 'pattern':
      return citationPattern.test(c.src);
  }
}

const MATRIX: MatrixCase[] = [
  // ---- 4 bounce forms: the shapes #0181's original bounce named explicitly ----
  {
    category: 'bounce',
    name: "string literal expression: {'...'}",
    kind: 'svelte',
    expectMatch: true,
    src: `<p>{'see PRD §6.6'}</p>`,
  },
  {
    category: 'bounce',
    name: 'ternary expression',
    kind: 'svelte',
    expectMatch: true,
    src: `<script lang="ts">let cond = true;</script>\n<p>{cond ? 'see CLAUDE.md §9' : ''}</p>`,
  },
  {
    category: 'bounce',
    name: 'template literal expression',
    kind: 'svelte',
    expectMatch: true,
    src: '<p>{`see #0138`}</p>',
  },
  {
    category: 'bounce',
    name: "{@html '...'}",
    kind: 'svelte',
    expectMatch: true,
    src: `{@html 'see PRD §6.6'}`,
  },

  // ---- 12 further Svelte-expression shapes: every nested ESTree form
  // collectByType walks for free once it walks by node `type` rather than
  // by structural position ----
  {
    category: 'further-shape',
    name: 'object literal in a prop',
    kind: 'svelte',
    expectMatch: true,
    src: `<Foo opts={{ hint: 'see PRD §6.6' }} />`,
  },
  {
    category: 'further-shape',
    name: 'array in a prop',
    kind: 'svelte',
    expectMatch: true,
    src: `<Foo items={['see PRD §6.6']} />`,
  },
  {
    category: 'further-shape',
    name: 'attribute expression',
    kind: 'svelte',
    expectMatch: true,
    src: `<p title={'see PRD §6.6'}>hi</p>`,
  },
  {
    category: 'further-shape',
    name: '{#if} block test expression',
    kind: 'svelte',
    expectMatch: true,
    src: `<script lang="ts">let s = 'see PRD §6.6';</script>\n{#if s === 'see PRD §6.6'}<p>match</p>{/if}`,
  },
  {
    category: 'further-shape',
    name: '{#if}/{:else}',
    kind: 'svelte',
    expectMatch: true,
    src: `<script lang="ts">let cond = false;</script>\n{#if cond}<p>a</p>{:else}<p>{'see PRD §6.6'}</p>{/if}`,
  },
  {
    category: 'further-shape',
    name: '{#each}',
    kind: 'svelte',
    expectMatch: true,
    src: `{#each ['see PRD §6.6'] as i}<p>{i}</p>{/each}`,
  },
  {
    category: 'further-shape',
    name: '{@const} inside {#each}',
    kind: 'svelte',
    expectMatch: true,
    src: `{#each [1] as x}{@const m = 'see PRD §6.6'}<p>{m}</p>{/each}`,
  },
  {
    category: 'further-shape',
    name: 'snippet body text',
    kind: 'svelte',
    expectMatch: true,
    src: `{#snippet hint()}<p>see PRD §6.6</p>{/snippet}{@render hint()}`,
  },
  {
    category: 'further-shape',
    name: 'snippet body expression',
    kind: 'svelte',
    expectMatch: true,
    src: `{#snippet hint()}<p>{'see PRD §6.6'}</p>{/snippet}{@render hint()}`,
  },
  {
    category: 'further-shape',
    name: '{@render} argument',
    kind: 'svelte',
    expectMatch: true,
    src: `{#snippet hint(x)}<p>{x}</p>{/snippet}{@render hint('see PRD §6.6')}`,
  },
  {
    category: 'further-shape',
    name: '{#await}...then',
    kind: 'svelte',
    expectMatch: true,
    src: `{#await Promise.resolve()}<p>pending</p>{:then v}<p>{'see PRD §6.6'}</p>{/await}`,
  },
  {
    category: 'further-shape',
    name: 'call argument',
    kind: 'svelte',
    expectMatch: true,
    src: `<script lang="ts">function f(s: string) { return s; }</script>\n<p>{f('see PRD §6.6')}</p>`,
  },

  // ---- 5 structural misses: excluded by the AST shape itself, not by an
  // allowlist ----
  {
    category: 'structural-miss',
    name: 'JS comment inside an expression tag',
    kind: 'svelte',
    expectMatch: false,
    src: `<p>{/* see PRD §6.6 and #0138 */ 'ok'}</p>`,
  },
  {
    category: 'structural-miss',
    name: '<style> block',
    kind: 'svelte',
    expectMatch: false,
    src: `<style>/* see PRD §6.6 and #0138 */\n.x { color: #04140a; }</style>\n<p>hi</p>`,
  },
  {
    category: 'structural-miss',
    name: 'HTML comment in markup',
    kind: 'svelte',
    expectMatch: false,
    src: `<!-- see PRD §6.6, CLAUDE.md §9, #0138, Issues.md, docs/x.md -->\n<p>hi</p>`,
  },
  {
    category: 'structural-miss',
    name: 'CAN-SPAM §7704 in an expression',
    kind: 'svelte',
    expectMatch: false,
    src: `<p>{'CAN-SPAM §7704 requires a physical mailing address'}</p>`,
  },
  {
    category: 'structural-miss',
    name: 'EMAIL_REPLY_TO / EMAIL_LIST_DOMAIN env var names in an expression',
    kind: 'svelte',
    expectMatch: false,
    src: `<p>{'Set EMAIL_REPLY_TO and EMAIL_LIST_DOMAIN before sending.'}</p>`,
  },

  // ---- 7 acceptable misses: each one deliberate, each one earning its
  // own reason a future pass can check against ----
  {
    category: 'acceptable-miss',
    name: 'split string concatenation (CLAUDE.md)',
    kind: 'ts',
    expectMatch: false,
    src: `export const a = 'see CLAUDE' + '.md §9';`,
    note: 'the scanner reads each literal node\'s own .text; runtime concatenation joins the two halves only after the program executes, so neither half alone matches CLAUDE\\.md — accepted because deliberately splitting a citation across two literals to dodge the guard is not a plausible authoring accident.',
  },
  {
    category: 'acceptable-miss',
    name: 'split string concatenation (issue number)',
    kind: 'ts',
    expectMatch: false,
    src: `export const a = 'see #0' + '138';`,
    note: 'same mechanism as the CLAUDE.md split above: "#0" and "138" are two separate literal nodes, and #0[0-9]{3}\\b never sees them joined.',
  },
  {
    category: 'acceptable-miss',
    name: 'template literal interpolation splitting PRD from §',
    kind: 'ts',
    expectMatch: false,
    src: 'export const a = `see PRD ${1}§6.6`;',
    note: 'a template literal with an interpolation is TemplateHead ("see PRD ") + expression + TemplateTail ("§6.6") as separate nodes; the whitespace-class term needs PRD and § in the same literal, so an interpolated value sitting between them breaks the match — accepted for the same reason as the two splits above.',
  },
  {
    category: 'acceptable-miss',
    name: 'unpadded issue number (#138, not #0138)',
    kind: 'pattern',
    expectMatch: false,
    src: 'see #138 for context',
    note: 'the pattern is #0[0-9]{3}\\b, matching the repo\'s own zero-padded four-digit convention (#0001-#0999); an unpadded reference is not the convention this tracker uses, so there is nothing to catch.',
  },
  {
    category: 'acceptable-miss',
    name: 'claude.md lowercase case variant',
    kind: 'pattern',
    expectMatch: false,
    src: 'see claude.md for details',
    note: 'CLAUDE\\.md is case-sensitive by design; the real file is CLAUDE.md (all caps) and every citation in the tree spells it that way, so a hypothetical lowercase variant is not a shape this project produces.',
  },
  {
    category: 'acceptable-miss',
    name: 'prose mentioning docs without a path',
    kind: 'pattern',
    expectMatch: false,
    src: 'read the docs or any .md file for more',
    note: 'docs/\\S*\\.md requires "docs/" immediately adjacent to the filename with no whitespace between; ordinary prose that merely uses the word "docs" has no slash there, so \\S* never bridges the space.',
  },
  {
    category: 'acceptable-miss',
    name: 'prose mentioning an external docs host',
    kind: 'pattern',
    expectMatch: false,
    src: 'see docs.google.com for the shared doc',
    note: 'docs\\.google\\.com has a dot, not a slash, after "docs" — the pattern needs "docs/", so a domain name that merely starts with "docs." never matches.',
  },

  // ---- 6 boundary checks: exact edges of the regex, not the AST walk ----
  {
    category: 'boundary',
    name: 'PRD followed by two spaces then §',
    kind: 'pattern',
    expectMatch: true,
    src: 'PRD  §6.6',
  },
  {
    category: 'boundary',
    name: 'PRD immediately followed by § (no space)',
    kind: 'pattern',
    expectMatch: true,
    src: 'PRD§6.6',
  },
  {
    category: 'boundary',
    name: 'hex color that contains #041 as a substring',
    kind: 'pattern',
    expectMatch: false,
    src: '#04140a',
  },
  {
    category: 'boundary',
    name: 'a genuine issue reference',
    kind: 'pattern',
    expectMatch: true,
    src: 'see #0181 for context',
  },
  {
    category: 'boundary',
    name: 'an issues/NNNN.md tracker path',
    kind: 'pattern',
    expectMatch: true,
    src: 'see issues/0138.md for the acceptance criteria',
  },
  {
    category: 'boundary',
    name: 'ordinary prose containing the word "issues"',
    kind: 'pattern',
    expectMatch: false,
    src: 'see the issues tracker for details',
  },
];

const MATRIX_COUNTS: Record<MatrixCategory, number> = {
  bounce: 4,
  'further-shape': 12,
  'structural-miss': 5,
  'acceptable-miss': 7,
  boundary: 6,
};

describe('citation guard shape matrix (#0194)', () => {
  it('has exactly the case counts #0187\'s phase-3 review ran (4/12/5/7/6 = 34)', () => {
    for (const category of Object.keys(MATRIX_COUNTS) as MatrixCategory[]) {
      const actual = MATRIX.filter((c) => c.category === category).length;
      expect(actual, `expected ${MATRIX_COUNTS[category]} '${category}' cases, found ${actual}`).toBe(MATRIX_COUNTS[category]);
    }
    expect(MATRIX.length).toBe(34);
  });

  it('every acceptable-miss case carries a one-line note explaining why the miss is deliberate', () => {
    for (const c of MATRIX.filter((c) => c.category === 'acceptable-miss')) {
      expect(c.note, `case "${c.name}" has no note`).toBeTruthy();
      expect((c.note ?? '').length, `case "${c.name}"'s note is suspiciously short`).toBeGreaterThan(20);
    }
  });

  for (const c of MATRIX) {
    it(`[${c.category}] ${c.name} — ${c.expectMatch ? 'should catch' : 'should miss'}`, () => {
      expect(runMatrixCase(c)).toBe(c.expectMatch);
    });
  }
});

describe('toRepoRelativePath (#0194)', () => {
  it('maps a "../" key to a path relative to web/src', () => {
    expect(toRepoRelativePath('../views/admin/WorkshopEditor.svelte')).toBe('web/src/views/admin/WorkshopEditor.svelte');
  });

  it('maps a "./" key to web/src/lib', () => {
    expect(toRepoRelativePath('./branding.ts')).toBe('web/src/lib/branding.ts');
  });

  it('does not mangle a bare (prefix-less) key', () => {
    // #0194: the old else branch did `lib/${globKey.slice(2)}`, which
    // assumed a two-character "./" prefix was always there to strip. Vite
    // emits no bare key today (the 83/83 measurement in #0187's review
    // confirms it), but if it ever did, slicing two characters off a key
    // with no prefix would drop real filename characters rather than
    // report the path as-is.
    expect(toRepoRelativePath('branding.ts')).toBe('web/src/lib/branding.ts');
  });
});
