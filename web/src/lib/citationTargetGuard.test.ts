// #0220: five times now (#0190, #0192, #0196, #0199, #0216) a comment has
// shipped a citation nothing checked -- #0196 built a guard for Go comments
// citing a Test* function that no function defines
// (internal/handlers/dangling_test_citation_guard_test.go), but #0216's own
// fix shipped a TypeScript comment citing a repository path that does not
// exist (e.g. "web/HANDOFF", found by #0094's review) and, in the same file's
// modalKeydown.test.ts, a comment naming three test FILES that do not exist
// (e.g. "web/src/views/Admin.escapeWiring.structuralGuard.test.ts" and two
// siblings). Neither shape is Go, so #0196's guard could not have caught
// either. This file is the TypeScript/Svelte half of the same idea, ported
// to two more citation shapes: a repository PATH, and a "CLAUDE.md §N"
// section reference -- resolved against their own ground truths (the files
// actually on disk, and CLAUDE.md's own numbered headings), the same two
// shapes internal/handlers/citation_target_guard_test.go resolves for Go
// comments. Deliberately the SAME design as that file throughout (a regexp
// naming the candidate shape, a set of exclusion rules mutation-tested
// against the real tree, a repo-relative pasteable failure message) --
// ported, not reinvented, per this issue's own framing.
//
// Comment extraction, unlike citationGuard.test.ts (#0181's web guard,
// which scans STRING/template literals and structurally excludes comments):
// this guard scans ONLY comments, the opposite half of the AST.
//   - .ts files: TypeScript has no public "walk every comment" AST node --
//     comments are trivia. ts.createScanner(.... skipTrivia: false ..)
//     tokenizes SingleLineCommentTrivia/MultiLineCommentTrivia directly,
//     which is the standard technique for this (verified against the
//     `typescript` package already a devDependency via svelte-check).
//   - .svelte files: svelte/compiler's parse({ modern: true }) separates
//     comments from code the same way citationGuard.test.ts's own header
//     documents -- JS comments in <script>/<script module> collect on
//     ast.comments (never inside the ESTree tree collectByType walks), and
//     an HTML comment is its own `Comment` node type in the fragment tree.
//
// Consecutive "//" (or svelte 'Line') comments with no gap between their
// line numbers are joined into one BLOCK before matching, mirroring Go's
// *ast.CommentGroup grouping (see internal/handlers
// /citation_target_guard_test.go's own header) -- a real instance needed
// it on the Go side (an "e.g." marker on the line before its citation), and
// nothing here is different enough to assume TypeScript doc comments never
// do the same. A "/* */" block comment or an HTML "<!-- -->" comment is
// always its own block (never merged), and its own internal newlines are
// split the same way Go's #0199 fix splits a multi-line "/* */" comment's
// text, so a reported line is the citation's own physical line, not the
// block's opening line.
//
// Self-scan (criterion 5): this file is itself a .test.ts file the guard
// below WILL scan -- unlike citationGuard.test.ts, which structurally
// cannot see this problem (#0181's web guard scans string/template
// literals, excludes comments; this guard scans comments, so excluding
// .test.ts the way #0181's guard does would hide exactly the shape #0216
// shipped, which landed in a .test.ts file). Every path or "CLAUDE.md §N"
// token in this file's own REAL comments therefore must resolve -- proven
// by TestCitationTargetGuardScansItselfCleanly below, which asserts zero
// hits from THIS file alone, not just inferred from the tree-wide test
// passing. The fictitious examples this file needs to prove the guard
// fires (see the "catches" describe block) are built from string
// concatenation inside `src` fixture variables, never written as a literal
// comment token in this file -- the same technique
// citation_target_guard_test.go states for itself, and the same reason
// citationGuard.test.ts's own MATRIX fixtures give (#0194) for why that is
// safe: fixture DATA is not a comment this guard's own walk visits.
import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import ts from 'typescript';
import { parse as parseSvelte } from 'svelte/compiler';

// citationTargetPathPattern mirrors citation_target_guard_test.go's
// citationTargetPathPattern exactly (same extension set, same
// HANDOFF/README/LICENSE bare-suffix exception for the real historical
// "web" + "HANDOFF" defect) -- see that file's own doc comment for the
// reasoning; not repeated here so the two never drift on which reasoning
// is authoritative, only on which engine reads it (V8 here, RE2 there).
const citationTargetPathPattern =
  /\b[A-Za-z0-9_][A-Za-z0-9_.-]*(?:\/[A-Za-z0-9_.-]+)*\/(?:[A-Za-z0-9_.-]+\.(?:go|ts|tsx|svelte|md|sql|sh|json|ya?ml|css|html|mjs|cjs)|HANDOFF|README|LICENSE)\b/g;

const citationTargetFileLikeSegmentPattern = /^[A-Za-z0-9_.-]+\.(?:go|ts|tsx|svelte|md|sql|sh|json|ya?ml|css|html|mjs|cjs)$/;

const citationTargetSectionPattern = /CLAUDE\.md(?:['’]s)?\s*§\s*(\d+[A-Za-z]?)/g;

const citationTargetHypotheticalMarkers = [
  'e.g.',
  'for example',
  'such as',
  'hypothetical',
  'imagine',
  'not a real path',
  'does not exist',
  'no such file',
  'made up',
  'placeholder',
];

// "never written" is the one marker here without a Go-side counterpart:
// the real instance is web/-only, modalKeydown.test.ts:9's own honest
// history of #0216's dangling citations ("that were never written").
const citationTargetHistoricalMarkers = ['deleted in', 'the deleted', 'was deleted', 'removed in', 'no longer exists', 'used to live at', 'never written'];

const citationTargetExternalDependencyMarkers = ['goldmark'];

// citationTargetWildcardPrecedingChars, checked as the single character
// immediately before a match, discounts a glob-style family reference
// ("*.escapeWiring.structuralGuard.test.ts") from the bare-test-file
// pattern -- the real instance is modalKeydown.test.ts's OWN corrected
// text (post-#0216), which replaced three specific dangling citations
// with an honest "*.escapeWiring.structuralGuard.test.ts" shape describing
// what the old, wrong citations looked like, not naming a specific file.
// Mirrors citation_target_guard_test.go's Go-side wildcard-family
// reasoning (borrowed from #0196's dangling-test-citation guard) --
// there the wildcard character trails the match; here, because a glob
// prefix reads left-to-right in front of the extension it stands for, it
// precedes it instead.
const citationTargetWildcardPrecedingChars = new Set(['*']);

const citationTargetPlaceholderSegments = new Set(['NNNN']);

const citationTargetBareDocNames = new Set(['HANDOFF', 'README', 'LICENSE', 'PRD']);

// citationTargetBareTestFilePattern additionally recognizes a bare
// "Name" + ".something" + ".test.ts"-shaped reference with NO
// leading directory -- the real historical shape #0216 shipped in
// modalKeydown.test.ts (e.g. a fully-qualified "web/src/views/" path for
// the first of three sibling *.structuralGuard.test.ts names, with the
// other two left bare) names one fully-qualified path and two bare
// siblings sharing its implied directory. citationTargetPathPattern alone
// (which requires at least one "/") already catches the first; this
// second, narrower pattern (bare, ".test.ts"/".test.tsx" specifically,
// still requiring at least one internal "." before it so a plain word
// doesn't qualify) closes the bare-sibling shape too, resolved the same
// way as any other citation -- against the real tree, with the SAME
// leading-bare-word retry unavailable (there is no leading segment to
// strip), so a bare one only resolves via an exact basename match
// somewhere in the tree.
const citationTargetBareTestFilePattern = /\b[A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)+\.test\.tsx?\b/g;

function pathCitationIsExcluded(text: string, start: number, end: number): boolean {
  const cited = text.slice(start, end);

  // Checked up to 2 characters back, not just 1: the real shape is
  // "*.escapeWiring..." -- a glob star, then a literal "." before the
  // first letter the bare-test-file pattern can start matching on (the
  // pattern requires a letter, so it never starts matching ON the "*" or
  // "." themselves).
  const wildcardWindow = text.slice(Math.max(0, start - 2), start);
  for (const ch of wildcardWindow) {
    if (citationTargetWildcardPrecedingChars.has(ch)) return true;
  }

  const urlWindowStart = Math.max(0, start - 24);
  if (text.slice(urlWindowStart, start).includes('://')) return true;

  const proseStart = Math.max(0, start - 60);
  const lowerWindow = text.slice(proseStart, start).toLowerCase();
  for (const marker of citationTargetHypotheticalMarkers) {
    if (lowerWindow.includes(marker)) return true;
  }
  for (const marker of citationTargetHistoricalMarkers) {
    if (lowerWindow.includes(marker)) return true;
  }

  const lowerBeforeAll = text.slice(0, start).toLowerCase();
  for (const dep of citationTargetExternalDependencyMarkers) {
    if (lowerBeforeAll.includes(dep)) return true;
  }

  const segments = cited.split('/');

  for (const seg of segments) {
    const dot = seg.lastIndexOf('.');
    const base = dot >= 0 ? seg.slice(0, dot) : seg;
    if (citationTargetPlaceholderSegments.has(base)) return true;
  }

  // ANY intermediate segment (not the last) matching -- not a requirement
  // that every intermediate segment qualify. Mirrors
  // internal/handlers/citation_target_guard_test.go's own
  // pathCitationIsExcluded exactly, including its cost: a citation shaped
  // like a real file segment followed by a later dangling segment (not
  // spelled out here as a literal slash-joined token, since this comment is
  // itself scanned by the guard below -- see that Go file's own synthetic
  // example, "AdminDotSvelte / lib / totallyNonexistentXyzDotTs") is
  // excluded by its first matching segment alone, even if a later segment
  // is a genuinely dangling path. That residual cost is a MISSED
  // detection, not a false alarm -- but THIS file's own tree-wide scan
  // cannot demonstrate it: this guard walks only .ts/.svelte comments
  // (collectTsCommentBlocks/collectSvelteCommentBlocks below), and the one
  // real citation in the whole tree that "any" and "every" disagree on
  // (a legitimate multi-file list citation that "every" wrongly flags as
  // unresolved) lives in a .go file, admin_workshops.go, which this guard
  // never opens. Flipping this loop to "every"-semantics and re-running
  // THIS file's own tree-wide scan therefore stays green -- zero hits,
  // not one -- which argues nothing either way about the citation itself.
  // The actual evidence for keeping "any" is the Go guard's reproduction,
  // cited there, not this file's: see
  // internal/handlers/citation_target_guard_test.go's own doc comment for
  // the full reasoning and the real probe that surfaced "any" vs "every"
  // (#0220's phase-3 review).
  for (const seg of segments.slice(0, -1)) {
    if (citationTargetFileLikeSegmentPattern.test(seg) || citationTargetBareDocNames.has(seg)) return true;
  }

  return false;
}

function pathExistsDirectly(paths: Set<string>, cited: string): boolean {
  if (paths.has(cited) || paths.has(cited + '.md')) return true;
  const suffix = '/' + cited;
  const suffixMD = suffix + '.md';
  for (const p of paths) {
    if (p.endsWith(suffix) || p.endsWith(suffixMD)) return true;
  }
  return false;
}

function pathCitationResolves(paths: Set<string>, cited: string): boolean {
  if (pathExistsDirectly(paths, cited)) return true;
  const segments = cited.split('/');
  if (segments.length >= 3 && !segments[0].includes('.')) {
    const remainder = segments.slice(1).join('/');
    if (pathExistsDirectly(paths, remainder)) return true;
  }
  return false;
}

// bareTestFileResolves checks a bare "Name" + ".thing" + ".test.ts"-shaped citation
// (no directory at all) against the real tree by basename only -- the
// weakest, most permissive check this guard performs, deliberately scoped
// to exactly the shape citationTargetBareTestFilePattern matches. See that
// pattern's own comment for why a real, narrow instance needs it.
function bareTestFileResolves(paths: Set<string>, cited: string): boolean {
  const suffix = '/' + cited;
  for (const p of paths) {
    if (p === cited || p.endsWith(suffix)) return true;
  }
  return false;
}

// citationTargetSkipDirs mirrors citation_target_guard_test.go's
// citationTargetSkipDirs exactly, including the same reasoning for NOT
// pruning "dist": web/dist/index.html is a tracked placeholder several
// real comments cite as "dist/index.html" or "web/dist/index.html".
const citationTargetSkipDirs = new Set(['.git', 'node_modules']);

function collectRepoPaths(repoRoot: string): Set<string> {
  const paths = new Set<string>();
  function walk(dir: string) {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      if (entry.isDirectory()) {
        if (citationTargetSkipDirs.has(entry.name)) continue;
        walk(path.join(dir, entry.name));
        continue;
      }
      const rel = path.relative(repoRoot, path.join(dir, entry.name)).split(path.sep).join('/');
      paths.add(rel);
    }
  }
  walk(repoRoot);
  return paths;
}

const claudeMDHeadingPattern = /^##\s+(\d+[A-Za-z]?)\.\s/gm;

function loadClaudeMDSections(repoRoot: string): Set<string> {
  const data = readFileSync(path.join(repoRoot, 'CLAUDE.md'), 'utf-8');
  const sections = new Set<string>();
  for (const m of data.matchAll(claudeMDHeadingPattern)) {
    sections.add(m[1]);
  }
  return sections;
}

// __filename/repoRoot: web/src/lib/ -> repo root is three levels up.
const __filename = fileURLToPath(import.meta.url);
const REPO_ROOT = path.resolve(path.dirname(__filename), '..', '..', '..');

interface CitationHit {
  file: string;
  line: number;
  kind: 'path' | 'section';
  cited: string;
}

interface CommentBlock {
  lines: string[]; // marker-stripped physical lines
  startLine: number;
}

function stripLineMarker(text: string): string {
  return text.startsWith('//') ? text.slice(2) : text;
}

function stripBlockMarker(text: string): string {
  if (text.startsWith('/*') && text.endsWith('*/')) return text.slice(2, -2);
  return text;
}

// mergeLineComments joins consecutive single-line comments (no gap between
// their line numbers) into one CommentBlock, mirroring how Go's
// *ast.CommentGroup automatically groups adjacent "//" lines with no blank
// line between them (see citation_target_guard_test.go's own header for
// why a real instance needs this).
function mergeLineComments(raw: { text: string; line: number }[]): CommentBlock[] {
  const sorted = [...raw].sort((a, b) => a.line - b.line);
  const blocks: CommentBlock[] = [];
  for (const c of sorted) {
    const last = blocks[blocks.length - 1];
    if (last && last.startLine + last.lines.length === c.line) {
      last.lines.push(c.text);
    } else {
      blocks.push({ lines: [c.text], startLine: c.line });
    }
  }
  return blocks;
}

function collectTsCommentBlocks(fileName: string, source: string): CommentBlock[] {
  const sourceFile = ts.createSourceFile(fileName, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const scanner = ts.createScanner(ts.ScriptTarget.Latest, false, ts.LanguageVariant.Standard, source);
  const lineComments: { text: string; line: number }[] = [];
  const blocks: CommentBlock[] = [];
  let kind = scanner.scan();
  while (kind !== ts.SyntaxKind.EndOfFileToken) {
    if (kind === ts.SyntaxKind.SingleLineCommentTrivia) {
      const pos = scanner.getTokenPos();
      const lc = sourceFile.getLineAndCharacterOfPosition(pos);
      lineComments.push({ text: stripLineMarker(scanner.getTokenText()), line: lc.line + 1 });
    } else if (kind === ts.SyntaxKind.MultiLineCommentTrivia) {
      const pos = scanner.getTokenPos();
      const lc = sourceFile.getLineAndCharacterOfPosition(pos);
      const stripped = stripBlockMarker(scanner.getTokenText());
      blocks.push({ lines: stripped.split('\n'), startLine: lc.line + 1 });
    }
    kind = scanner.scan();
  }
  return [...mergeLineComments(lineComments), ...blocks];
}

function collectSvelteCommentBlocks(fileName: string, source: string): CommentBlock[] {
  const ast = parseSvelte(source, { filename: fileName, modern: true }) as unknown as Record<string, unknown>;
  const lineOf = (offset: number): number => source.slice(0, offset).split('\n').length;

  const lineComments: { text: string; line: number }[] = [];
  const blocks: CommentBlock[] = [];

  const jsComments = (ast.comments as { type: string; value: string; start: number }[] | undefined) ?? [];
  for (const c of jsComments) {
    if (c.type === 'Line') {
      lineComments.push({ text: c.value, line: lineOf(c.start) });
    } else {
      blocks.push({ lines: c.value.split('\n'), startLine: lineOf(c.start) });
    }
  }

  for (const n of collectByType(ast.fragment, new Set(['Comment']))) {
    const data = (n.data as string | undefined) ?? '';
    const start = n.start as number | undefined;
    blocks.push({ lines: data.split('\n'), startLine: start !== undefined ? lineOf(start) : 0 });
  }

  return [...mergeLineComments(lineComments), ...blocks];
}

// collectByType: identical technique to citationGuard.test.ts's own helper
// of the same name -- walks a plain-object AST and returns every node
// whose `type` is in `types`.
function collectByType(node: unknown, types: Set<string>, out: Record<string, unknown>[] = [], seen = new Set<unknown>()): Record<string, unknown>[] {
  if (node === null || typeof node !== 'object') return out;
  if (seen.has(node)) return out;
  seen.add(node);
  if (Array.isArray(node)) {
    for (const item of node) collectByType(item, types, out, seen);
    return out;
  }
  const obj = node as Record<string, unknown>;
  if (typeof obj.type === 'string' && types.has(obj.type)) out.push(obj);
  for (const key of Object.keys(obj)) {
    if (key === 'parent') continue;
    collectByType(obj[key], types, out, seen);
  }
  return out;
}

// scanBlocksForHits mirrors citation_target_guard_test.go's
// collectCitationTargetHits: joins a block's own lines into one text blob
// (so an exclusion marker on one physical line can excuse a citation on
// the next, and the goldmark-style external-dependency check can look back
// unbounded within just that one block), matches both patterns against it,
// and maps each match's offset back to its own physical line.
function scanBlocksForHits(fileName: string, blocks: CommentBlock[], paths: Set<string>, sections: Set<string>): CitationHit[] {
  const hits: CitationHit[] = [];
  for (const block of blocks) {
    const spans: { start: number; line: number }[] = [];
    let text = '';
    for (let i = 0; i < block.lines.length; i++) {
      spans.push({ start: text.length, line: block.startLine + i });
      text += block.lines[i] + '\n';
    }
    const lineAt = (offset: number): number => {
      let cur = spans[0];
      for (const s of spans) {
        if (s.start > offset) break;
        cur = s;
      }
      return cur.line;
    };

    for (const m of text.matchAll(citationTargetPathPattern)) {
      const start = m.index ?? 0;
      const end = start + m[0].length;
      if (pathCitationIsExcluded(text, start, end)) continue;
      const cited = m[0];
      if (!pathCitationResolves(paths, cited)) {
        hits.push({ file: fileName, line: lineAt(start), kind: 'path', cited });
      }
    }

    for (const m of text.matchAll(citationTargetBareTestFilePattern)) {
      const start = m.index ?? 0;
      if (pathCitationIsExcluded(text, start, start + m[0].length)) continue;
      const cited = m[0];
      if (!bareTestFileResolves(paths, cited)) {
        hits.push({ file: fileName, line: lineAt(start), kind: 'path', cited });
      }
    }

    for (const m of text.matchAll(citationTargetSectionPattern)) {
      const start = m.index ?? 0;
      const whole = m[0];
      const section = m[1];
      if (sections.has(section)) continue;
      hits.push({ file: fileName, line: lineAt(start), kind: 'section', cited: whole });
    }
  }
  return hits;
}

const SOURCE_FILES = import.meta.glob('../**/*.{ts,svelte}', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

// toRepoRelativePath matches citationGuard.test.ts's own function of the
// same name exactly (see #0187/#0194's reasoning there) -- not imported
// from that file since it is not exported, and duplicating six lines is
// cheaper than restructuring a file this issue is not otherwise touching.
function toRepoRelativePath(globKey: string): string {
  if (globKey.startsWith('../')) {
    return `web/src/${globKey.slice(3)}`;
  }
  if (globKey.startsWith('./')) {
    return `web/src/lib/${globKey.slice(2)}`;
  }
  return `web/src/lib/${globKey}`;
}

function scanFile(rel: string, source: string, paths: Set<string>, sections: Set<string>): CitationHit[] {
  const blocks = rel.endsWith('.svelte') ? collectSvelteCommentBlocks(rel, source) : collectTsCommentBlocks(rel, source);
  return scanBlocksForHits(rel, blocks, paths, sections);
}

describe('citation TARGET guard (#0220): every comment-cited repository path or CLAUDE.md section resolves', () => {
  it('no .ts/.svelte comment (test files included) cites a path or CLAUDE.md section that does not resolve', () => {
    const paths = collectRepoPaths(REPO_ROOT);
    const sections = loadClaudeMDSections(REPO_ROOT);
    expect(sections.size, 'loadClaudeMDSections found zero numbered headings -- the heading pattern itself is broken, not the tree').toBeGreaterThan(0);

    const hits: CitationHit[] = [];
    for (const [globKey, source] of Object.entries(SOURCE_FILES)) {
      const rel = toRepoRelativePath(globKey);
      hits.push(...scanFile(rel, source, paths, sections));
    }

    if (hits.length > 0) {
      const detail = hits
        .slice()
        .sort((a, b) => (a.file === b.file ? a.line - b.line : a.file < b.file ? -1 : 1))
        .map((h) => `  ${h.file}:${h.line}: [${h.kind}] ${h.cited}`)
        .join('\n');
      throw new Error(
        `comment cites a repository path or CLAUDE.md section that does not resolve -- fix the citation or the target (see #0196, #0216, #0220):\n${detail}`,
      );
    }
    expect(hits).toHaveLength(0);
  });

  // Criterion 5: this file's OWN comments must resolve too, asserted in
  // isolation (not just inferred from the tree-wide pass above) so a
  // reviewer can re-run exactly this one file and see the property hold on
  // its own.
  it('scans its own file (self-reference) and finds zero hits', () => {
    const source = readFileSync(__filename, 'utf-8');
    const paths = collectRepoPaths(REPO_ROOT);
    const sections = loadClaudeMDSections(REPO_ROOT);
    const hits = scanFile('web/src/lib/citationTargetGuard.test.ts', source, paths, sections);
    expect(hits, `unexpected self-citation hits: ${JSON.stringify(hits)}`).toHaveLength(0);
  });
});

describe('citation target guard: catches (synthetic fixtures)', () => {
  it('fires on a .ts comment citing a nonexistent repository path', () => {
    const fictitiousPath = 'internal/handlers/' + 'fictitious_file_for_guard_self_proof.go';
    const src = `// see ${fictitiousPath} for the rationale\nexport function ok(): void {}`;
    const blocks = collectTsCommentBlocks('fixture.ts', src);
    const paths = collectRepoPaths(REPO_ROOT);
    expect(pathCitationResolves(paths, fictitiousPath), 'fixture setup: fictitious path unexpectedly resolves').toBe(false);
    const hits = scanBlocksForHits('fixture.ts', blocks, paths, loadClaudeMDSections(REPO_ROOT));
    expect(hits.some((h) => h.cited === fictitiousPath)).toBe(true);
  });

  it('fires on a .svelte HTML comment citing a nonexistent repository path', () => {
    const fictitiousPath = 'web/src/lib/' + 'fictitious_component_for_guard_self_proof.ts';
    const src = `<!-- see ${fictitiousPath} for the rationale -->\n<p>hi</p>`;
    const blocks = collectSvelteCommentBlocks('fixture.svelte', src);
    const paths = collectRepoPaths(REPO_ROOT);
    const hits = scanBlocksForHits('fixture.svelte', blocks, paths, loadClaudeMDSections(REPO_ROOT));
    expect(hits.some((h) => h.cited === fictitiousPath)).toBe(true);
  });

  it('fires on a .svelte <script> comment citing a nonexistent CLAUDE.md section', () => {
    const src = `<script lang="ts">\n  // see CLAUDE.md §${'9999'} for the rationale\n  let x = 1;\n</script>\n<p>{x}</p>`;
    const blocks = collectSvelteCommentBlocks('fixture.svelte', src);
    const sections = loadClaudeMDSections(REPO_ROOT);
    expect(sections.has('9999'), 'fixture setup: CLAUDE.md unexpectedly has section 9999').toBe(false);
    const hits = scanBlocksForHits('fixture.svelte', blocks, collectRepoPaths(REPO_ROOT), sections);
    expect(hits.some((h) => h.kind === 'section' && h.cited.includes('9999'))).toBe(true);
  });

  it('fires on a bare "Name.thing.test.ts" citation with no directory at all (the #0216 modalKeydown.test.ts shape)', () => {
    const src = `// proven by Fictitious.escapeWiring.structuralGuard.test.ts\nexport function ok(): void {}`;
    const blocks = collectTsCommentBlocks('fixture.ts', src);
    const paths = collectRepoPaths(REPO_ROOT);
    const hits = scanBlocksForHits('fixture.ts', blocks, paths, loadClaudeMDSections(REPO_ROOT));
    expect(hits.some((h) => h.cited === 'Fictitious.escapeWiring.structuralGuard.test.ts')).toBe(true);
  });
});

describe('citation target guard: exclusions (synthetic fixtures)', () => {
  it('does not fire on a URL-embedded path', () => {
    const src = `// see "https://example.com/docs/guide.md" for the rationale\nexport function ok(): void {}`;
    const blocks = collectTsCommentBlocks('fixture.ts', src);
    const hits = scanBlocksForHits('fixture.ts', blocks, collectRepoPaths(REPO_ROOT), loadClaudeMDSections(REPO_ROOT));
    expect(hits).toHaveLength(0);
  });

  it('does not fire on a deliberately hypothetical example introduced by "e.g."', () => {
    const src = `// issues/NNNN.md (a path to one tracker file, e.g. issues/fictitious/example.go)\nexport function ok(): void {}`;
    const blocks = collectTsCommentBlocks('fixture.ts', src);
    const hits = scanBlocksForHits('fixture.ts', blocks, collectRepoPaths(REPO_ROOT), loadClaudeMDSections(REPO_ROOT));
    expect(hits).toHaveLength(0);
  });

  it('does not fire across a line break: the "e.g." marker sits on the PREVIOUS physical line', () => {
    const src = `// hashedAssetPattern matches a Vite-emitted hashed asset reference, e.g.\n// */assets/index-BDtqW4JY.fictitious* or */assets/index-TVYljy6F.fictitious*\nexport function ok(): void {}`;
    // ".fictitious" is not a recognized extension, so build the real
    // cross-line proof against a recognized one instead, matching the real
    // dist_placeholder_guard_test.go:116 shape exactly (an "e.g." on line
    // 1, the cited path on line 2).
    const real = `// hashedAssetPattern matches a Vite-emitted hashed asset reference, e.g.\n// */assets/index-BDtqW4JY.js* or */assets/index-TVYljy6F.css*\nexport function ok(): void {}`;
    void src;
    const blocks = collectTsCommentBlocks('fixture.ts', real);
    const hits = scanBlocksForHits('fixture.ts', blocks, collectRepoPaths(REPO_ROOT), loadClaudeMDSections(REPO_ROOT));
    expect(hits).toHaveLength(0);
  });

  it('does not fire on the NNNN placeholder convention', () => {
    const src = `// #0187, item 2: issues/NNNN.md (a path to one tracker file)\nexport function ok(): void {}`;
    const blocks = collectTsCommentBlocks('fixture.ts', src);
    const hits = scanBlocksForHits('fixture.ts', blocks, collectRepoPaths(REPO_ROOT), loadClaudeMDSections(REPO_ROOT));
    expect(hits).toHaveLength(0);
  });

  it('does not fire on two whole filenames joined by a bare "/" (list shorthand, not a nested path)', () => {
    const src = `// following campaigns_test.go/audience_test.go's own conventions\nexport function ok(): void {}`;
    const blocks = collectTsCommentBlocks('fixture.ts', src);
    const hits = scanBlocksForHits('fixture.ts', blocks, collectRepoPaths(REPO_ROOT), loadClaudeMDSections(REPO_ROOT));
    expect(hits).toHaveLength(0);
  });

  it('does not fire on "PRD/CLAUDE.md §9" (bare document names joined, not a nested path)', () => {
    const src = `// PRD/CLAUDE.md §9 still forbid anything unsafe\nexport function ok(): void {}`;
    const blocks = collectTsCommentBlocks('fixture.ts', src);
    const hits = scanBlocksForHits('fixture.ts', blocks, collectRepoPaths(REPO_ROOT), loadClaudeMDSections(REPO_ROOT));
    expect(hits).toHaveLength(0);
  });

  it('does not fire on a citation of something the comment itself says was deleted', () => {
    const src = `// Ported from the source skeleton (deleted in #0002 along with the rest of internal/handlers/redirect.go)\nexport function ok(): void {}`;
    const blocks = collectTsCommentBlocks('fixture.ts', src);
    const hits = scanBlocksForHits('fixture.ts', blocks, collectRepoPaths(REPO_ROOT), loadClaudeMDSections(REPO_ROOT));
    expect(hits).toHaveLength(0);
  });

  it('does not fire on a named external dependency\'s own internal source layout (goldmark)', () => {
    const src = `// Confirmed against goldmark's own source (renderer/html/html.go: renderRawHTML)\nexport function ok(): void {}`;
    const blocks = collectTsCommentBlocks('fixture.ts', src);
    const hits = scanBlocksForHits('fixture.ts', blocks, collectRepoPaths(REPO_ROOT), loadClaudeMDSections(REPO_ROOT));
    expect(hits).toHaveLength(0);
  });

  it('does not fire on a string/template literal -- only comments are scanned', () => {
    const src = `export const a = 'see internal/handlers/does_not_exist_anywhere.go for the rationale';`;
    const blocks = collectTsCommentBlocks('fixture.ts', src);
    expect(blocks).toHaveLength(0);
  });

  it('resolves a genuine, existing repository path', () => {
    const src = `// see internal/handlers/citation_guard_test.go for the Go half\nexport function ok(): void {}`;
    const blocks = collectTsCommentBlocks('fixture.ts', src);
    const hits = scanBlocksForHits('fixture.ts', blocks, collectRepoPaths(REPO_ROOT), loadClaudeMDSections(REPO_ROOT));
    expect(hits).toHaveLength(0);
  });

  it('resolves a genuine, existing CLAUDE.md section', () => {
    const src = `// see CLAUDE.md §1 for the jsdom caveat\nexport function ok(): void {}`;
    const blocks = collectTsCommentBlocks('fixture.ts', src);
    const hits = scanBlocksForHits('fixture.ts', blocks, collectRepoPaths(REPO_ROOT), loadClaudeMDSections(REPO_ROOT));
    expect(hits).toHaveLength(0);
  });
});
