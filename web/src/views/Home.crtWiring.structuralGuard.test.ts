// #0398: #0393's first review bounced on an untested seam -- no test
// referenced Home.svelte, so its onMount fetch-and-fallback logic was
// unobserved. The remedy extracted that logic into crtLoadScript()
// (lib/crtScreen.ts) and unit-tested it directly (crtScreen.test.ts), which
// is real coverage of the DECISION logic. But the call site itself shrank
// to two lines and stayed unobserved: nothing failed if Home.svelte stopped
// calling crtLoadScript, or called it but threw the resolved script away.
//
// jsdom cannot substitute for this (issues/0398.md's Notes): Home.svelte's
// CRT onMount bails out at its own `ctx0`/`gctx0` context-null check, since
// this project's jsdom ships no canvas shim (`getContext('2d')` returns
// null) -- confirmed directly against #0393's own verification, which
// mounted nothing else of Home.svelte for exactly that reason. A structural
// scan of the real, un-mounted source is therefore the only oracle
// available here, and it is a WEAKER proof than a mount would be: it shows
// the wiring is textually/structurally present, not that it executes
// correctly at runtime. This file's claim rests entirely on source-text/AST
// structure, never on jsdom.
//
// Modelled on CampaignSendDialog.structuralGuard.test.ts and
// campaignEditorSlugAndDeleteWiring.structuralGuard.test.ts: parse
// Home.svelte with svelte/compiler and check the real AST, never raw text
// (a regex hunting for `crtLoadScript` could as easily match the identifier
// inside a comment -- CLAUDE.md's `#0064` hazard -- so every check below
// walks the parsed tree, not source-text search).
//
// checkCrtWiring below is BOTH the guard's own oracle (called against the
// real, unmodified file) and the thing the self-test drives against
// in-memory mutated copies of that same source (never a file written to
// disk -- CLAUDE.md §8a: do not move the shared tree out from under other
// agents). Its pass/fail decision is built entirely from distinct,
// hand-typed identifier names (`crtLoadScript`, `then`, `crtFallbackScript`,
// `session`) matched against AST node shapes -- never from a copied
// fragment of Home.svelte's own source compared byte-for-byte against
// itself, which is the CLAUDE.md §8 (`#0258`) failure this file is written
// to avoid: an oracle built that way can be edited in lockstep with its
// subject by the very same change and never notice. The two mutation
// helpers below (`withoutCrtLoadScriptCall`, `withDiscardedResult`) use a
// literal anchor from the real file only to locate WHERE to inject a
// mutation for the self-test, and each asserts its own edit actually took
// effect (`mutated !== componentSource`) before trusting the result -- if a
// future edit to Home.svelte moves this call site, the anchor stops
// matching, the mutation silently no-ops, and that assertion is what turns
// that into a loud self-test failure rather than a guard that quietly
// stopped checking anything.
import { describe, it, expect } from 'vitest';
import { parse as parseSvelte } from 'svelte/compiler';

const COMPONENT_GLOB = import.meta.glob('./Home.svelte', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

const COMPONENT_PATH = 'web/src/views/Home.svelte';

function soleValue(glob: Record<string, string>, label: string): string {
  const values = Object.values(glob);
  if (values.length !== 1) {
    throw new Error(`expected exactly one match for ${label}, found ${values.length}`);
  }
  return values[0];
}

const componentSource = soleValue(COMPONENT_GLOB, COMPONENT_PATH);

type SvelteNode = Record<string, unknown>;

// ---- generic, type-agnostic tree walk over svelte/compiler's plain-object
// AST -- same shape as the other *.structuralGuard.test.ts files' own
// findFirst (each guard file keeps its own copy rather than sharing a
// module, matching existing convention in this directory).
function findFirst(node: unknown, pred: (n: SvelteNode) => boolean, seen = new Set<unknown>()): SvelteNode | undefined {
  if (node === null || typeof node !== 'object') return undefined;
  if (seen.has(node)) return undefined;
  seen.add(node);
  if (Array.isArray(node)) {
    for (const item of node) {
      const found = findFirst(item, pred, seen);
      if (found) return found;
    }
    return undefined;
  }
  const obj = node as SvelteNode;
  if (pred(obj)) return obj;
  for (const key of Object.keys(obj)) {
    if (key === 'parent') continue;
    const found = findFirst(obj[key], pred, seen);
    if (found) return found;
  }
  return undefined;
}

function isIdentifierNamed(node: SvelteNode | undefined, name: string): boolean {
  return node?.type === 'Identifier' && node.name === name;
}

function isCallTo(node: SvelteNode | undefined, calleeName: string): boolean {
  if (node?.type !== 'CallExpression') return false;
  return isIdentifierNamed(node.callee as SvelteNode | undefined, calleeName);
}

/**
 * Finds the local variable declared as `let script: CrtScriptStep[] =
 * crtFallbackScript();` -- by that SHAPE (a declarator initialised to a
 * call to crtFallbackScript()), not by assuming its name is "script" --
 * so the rest of this file never has to hard-code that name as a guess.
 * This is "the script the CRT draws from": #393's session() loop reads it,
 * and it starts as the compiled-in fallback specifically so session() has
 * something to run before crtLoadScript() resolves.
 */
function findScriptBufferDeclarator(instanceContent: unknown): SvelteNode {
  const decl = findFirst(instanceContent, (n) => {
    if (n.type !== 'VariableDeclarator') return false;
    if ((n.id as SvelteNode | undefined)?.type !== 'Identifier') return false;
    return isCallTo(n.init as SvelteNode | undefined, 'crtFallbackScript');
  });
  if (!decl) {
    throw new Error(
      `${COMPONENT_PATH}: could not find a local declared as \`let X = crtFallbackScript()\` -- has the CRT script buffer moved? (#0393, #0398)`,
    );
  }
  return decl;
}

/**
 * Finds a `crtLoadScript().then(...)` call -- by AST shape (a CallExpression
 * whose callee is `<expr>.then` and whose object is itself a call to the
 * identifier `crtLoadScript`), never by scanning source text for the
 * substring "crtLoadScript" (CLAUDE.md's `#0064` hazard: a regex finds the
 * occurrence inside a comment as readily as the real call -- and this file's
 * own header comment contains that exact identifier several times over).
 */
function findCrtLoadScriptThenCall(instanceContent: unknown): SvelteNode {
  const call = findFirst(instanceContent, (n) => {
    if (n.type !== 'CallExpression') return false;
    const callee = n.callee as SvelteNode | undefined;
    if (callee?.type !== 'MemberExpression') return false;
    if (!isIdentifierNamed(callee.property as SvelteNode | undefined, 'then')) return false;
    return isCallTo(callee.object as SvelteNode | undefined, 'crtLoadScript');
  });
  if (!call) {
    throw new Error(
      `${COMPONENT_PATH}: could not find a \`crtLoadScript().then(...)\` call -- Home.svelte no longer appears to call crtLoadScript (#0398)`,
    );
  }
  return call;
}

/**
 * Given the `crtLoadScript().then(...)` call and the script-buffer variable
 * name, finds the `.then(...)` callback's parameter-to-buffer assignment
 * (`(s) => { X = s; ... }`). Returns undefined -- never throws -- when the
 * callback exists but does not assign, so callers can distinguish "the call
 * is gone" from "the call exists but its result is discarded", per
 * issues/0398.md's Description: "Home.svelte stops calling crtLoadScript, OR
 * stops using its result" are the two distinct failure shapes this guard
 * must catch.
 */
function findResultAssignment(thenCall: SvelteNode, scriptVarName: string): SvelteNode | undefined {
  const args = thenCall.arguments as SvelteNode[] | undefined;
  const callback = args?.[0];
  if (!callback || (callback.type !== 'ArrowFunctionExpression' && callback.type !== 'FunctionExpression')) {
    throw new Error(`${COMPONENT_PATH}: crtLoadScript().then(...)'s argument is not a single function expression`);
  }
  const params = callback.params as SvelteNode[] | undefined;
  const param = params?.[0];
  if (!param || param.type !== 'Identifier') {
    throw new Error(`${COMPONENT_PATH}: crtLoadScript().then(...)'s callback has no simple identifier parameter`);
  }
  const paramName = param.name as string;
  return findFirst(callback.body, (n) => {
    if (n.type !== 'AssignmentExpression' || n.operator !== '=') return false;
    return isIdentifierNamed(n.left as SvelteNode | undefined, scriptVarName) && isIdentifierNamed(n.right as SvelteNode | undefined, paramName);
  });
}

/**
 * Confirms the script-buffer variable is actually read inside `session()`
 * -- the loop that types the CRT's lines -- rather than merely existing.
 * Without this, a decoy `let script = crtFallbackScript();` that
 * crtLoadScript().then(...) dutifully reassigns but that session() never
 * reads would satisfy every check above while changing nothing the glass
 * shows.
 */
function sessionReadsVar(instanceContent: unknown, varName: string): boolean {
  const sessionFn = findFirst(
    instanceContent,
    (n) => (n.type === 'FunctionDeclaration' || n.type === 'FunctionExpression') && isIdentifierNamed(n.id as SvelteNode | undefined, 'session'),
  );
  if (!sessionFn) {
    throw new Error(`${COMPONENT_PATH}: could not find a function named \`session\` -- has #0393's typing loop moved or been renamed? (#0398)`);
  }
  const read = findFirst(sessionFn.body, (n) => isIdentifierNamed(n, varName) && n !== (sessionFn.id as SvelteNode));
  return !!read;
}

/**
 * The guard's whole oracle, in one function so it can be run unchanged
 * against both the real file (the guard itself) and in-memory mutated
 * copies (the self-test below). Returns a result object rather than
 * throwing so a caller can assert on `.ok` without a try/catch at every
 * call site; `.reason` carries either a specific defect or the underlying
 * parse/lookup error, so a failure is always actionable.
 */
function checkCrtWiring(source: string): { ok: boolean; reason: string } {
  try {
    const ast = parseSvelte(source, { filename: COMPONENT_PATH, modern: true }) as unknown as SvelteNode;
    const instance = (ast.instance as SvelteNode)?.content;
    if (!instance) {
      return { ok: false, reason: `${COMPONENT_PATH}: parsed AST has no <script> instance content` };
    }
    const bufferDecl = findScriptBufferDeclarator(instance);
    const bufferVarName = (bufferDecl.id as SvelteNode).name as string;
    const thenCall = findCrtLoadScriptThenCall(instance);
    const assignment = findResultAssignment(thenCall, bufferVarName);
    if (!assignment) {
      return {
        ok: false,
        reason: `${COMPONENT_PATH}: crtLoadScript().then(...)'s callback does not assign its resolved value to \`${bufferVarName}\` -- the fetched session is being discarded (#0398)`,
      };
    }
    if (!sessionReadsVar(instance, bufferVarName)) {
      return {
        ok: false,
        reason: `${COMPONENT_PATH}: \`${bufferVarName}\` is assigned crtLoadScript()'s result but session() never reads it (#0398)`,
      };
    }
    return { ok: true, reason: 'ok' };
  } catch (err) {
    return { ok: false, reason: err instanceof Error ? err.message : String(err) };
  }
}

// ---- self-test mutation helpers: text-anchored ONLY to locate where to
// inject a mutation into an in-memory copy, never used as the pass/fail
// oracle itself (that is checkCrtWiring, entirely AST/identifier-based
// above). Each helper asserts its own edit took effect before returning, so
// a stale anchor fails loudly rather than silently producing an unmutated
// "copy" that would make the self-test meaningless.

function withoutCrtLoadScriptCall(source: string): string {
  const anchor = 'void crtLoadScript().then((s) => {';
  const replacement = 'void Promise.resolve(crtFallbackScript()).then((s) => {';
  const mutated = source.replace(anchor, replacement);
  if (mutated === source) {
    throw new Error(`self-test anchor not found in ${COMPONENT_PATH} -- has the crtLoadScript() call site's text changed? update this mutation helper`);
  }
  return mutated;
}

function withDiscardedResult(source: string): string {
  const anchor = '      script = s;\n      void session();';
  const replacement = '      void session();';
  const mutated = source.replace(anchor, replacement);
  if (mutated === source) {
    throw new Error(`self-test anchor not found in ${COMPONENT_PATH} -- has the .then(...) callback's body text changed? update this mutation helper`);
  }
  return mutated;
}

describe('Home.svelte CRT-session wiring (#0398)', () => {
  it('calls crtLoadScript() and assigns its resolved script to the buffer session() reads', () => {
    const result = checkCrtWiring(componentSource);
    expect(result.ok, result.reason).toBe(true);
  });

  // #0398 acceptance: "carries a self-test that runs its own predicates
  // over an in-memory mutated copy of the file and asserts they fail on it
  // -- never against the file on disk". Both mutations below are pure
  // string transforms of `componentSource`, held only in memory; nothing is
  // written to disk (CLAUDE.md §8a).
  describe('self-test: checkCrtWiring actually detects both failure shapes', () => {
    it('fails when the crtLoadScript() call itself is removed', () => {
      const mutated = withoutCrtLoadScriptCall(componentSource);
      const result = checkCrtWiring(mutated);
      expect(result.ok).toBe(false);
    });

    it('fails when the resolved result is discarded rather than assigned', () => {
      const mutated = withDiscardedResult(componentSource);
      const result = checkCrtWiring(mutated);
      expect(result.ok).toBe(false);
    });

    it('passes against the real file, unmodified', () => {
      const result = checkCrtWiring(componentSource);
      expect(result.ok, result.reason).toBe(true);
    });
  });
});
