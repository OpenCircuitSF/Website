// #0197: this tracker has hit the replicated-logic trap three times —
// #0184 and #0189 (a hand-rolled `normalizeCountInput` copy, caught only
// because a reviewer hex-dumped a line on suspicion) and #0195
// (`countInputDisabled` / `escapeCloses` / `backdropCloses` in
// CampaignSendDialog.guard.test.ts, one-token mirrors of the component's
// own `sending` / `!sending` / `!sending` gates). Hex-dumping on suspicion
// is not a repeatable defence.
//
// This file is the structural answer. It reads CampaignSendDialog.svelte's
// REAL count-input `disabled`, Escape, and backdrop gate expressions from
// its `svelte/compiler` AST (the technique citationGuard.test.ts already
// uses for a different purpose — no DOM, no #0094), and separately reads
// CampaignSendDialog.guard.test.ts's `countInputDisabled` / `escapeCloses`
// / `backdropCloses` helper functions from THEIR OWN AST (TypeScript's
// compiler API, the same technique citationGuard.test.ts uses for .ts
// files). Neither side is retyped as a string literal here — every
// expected value is itself read out of a real file, and the two readings
// are then compared to each other. That is deliberate: a test that spells
// "sending" or "!sending" as a hand-typed string would just be a fourth
// replica of the thing three prior issues already got burned by. Instead:
//
//   - the component side is anchored to the ACTUAL local variable bound to
//     `$derived(guard.kind === 'sending')` (found structurally, by that
//     shape — never by assuming its name is "sending"), and each gate
//     expression is classified as "identity of that variable" or
//     "negation of that variable" purely from its own AST shape;
//   - the test-helper side is anchored to each helper's OWN parameter name
//     and classified the same way from ITS AST;
//   - the two classifications are then asserted equal.
//
// A mutation that changes `disabled={sending}` to
// `disabled={sending || blockedAgain}` turns the component side's
// classification from "identity" to "other", which no longer matches the
// test-helper side's "identity" claim — the assertion fails, and the
// failure message names both the component's real expression and the
// test's claimed one. See `## Implementation notes` in issues/0197.md for
// the executed proof.
//
// #0204 item 3 — WHAT THIS GUARD DOES NOT COVER, so a future reader does
// not over-trust the mechanism. The replicated-logic trap above has shown
// up in two distinct shapes:
//
//   - the MIRROR form (#0195): a test restates a one-token expression —
//     `sending`, `!sending` — that can only go STALE, never disagree in
//     substance. classifySvelteGate/classifyTsGate's whole vocabulary is
//     `identity` / `negation` / `other` over a single identifier, and
//     that vocabulary is exactly matched to this form. This file closes
//     it, for these three gates.
//   - the RE-IMPLEMENTATION form (#0184/#0189): a test rewrites a
//     function's LOGIC — the hand-rolled `normalizeCountInput` copy that
//     used an ASCII space where the real separator is U+2009 — and can
//     therefore DISAGREE with the original, not just go stale. This
//     guard's identity/negation/other vocabulary cannot express a regex
//     character class, a `Number.isSafeInteger` call, or any other
//     function body, so it cannot compare a test helper's body to a real
//     function's body. A fourth hand-rolled `normalizeCountInput` copy
//     landing in some other test file tomorrow would be exactly as
//     invisible to this file as it was in #0189.
//
// The working answer to the re-implementation form is not a guard at all
// — it is #0189's own fix: DELETE the replica and IMPORT AND CALL the
// real function, the way CampaignSendDialog.guard.test.ts now calls
// `normalizeCountInput` directly instead of re-deriving it. A structural
// guard like this one is the right tool only where the real thing
// genuinely cannot be called from a test — the `.svelte`-file case #0094
// creates, which is why this file exists at all for the count-input /
// Escape / backdrop gates. Generalising this file's mechanism to compare
// function bodies should be considered only if a future case truly cannot
// call the real thing; do not treat this file as having closed the
// re-implementation form, because it has not. See issues/0204.md.
import { describe, it, expect, beforeAll } from 'vitest';
import ts from 'typescript';
import { parse as parseSvelte } from 'svelte/compiler';

const COMPONENT_GLOB = import.meta.glob('./CampaignSendDialog.svelte', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;
const TEST_GLOB = import.meta.glob('./CampaignSendDialog.guard.test.ts', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

const COMPONENT_PATH = 'web/src/views/admin/CampaignSendDialog.svelte';
const TEST_PATH = 'web/src/views/admin/CampaignSendDialog.guard.test.ts';

function soleValue(glob: Record<string, string>, label: string): string {
  const values = Object.values(glob);
  if (values.length !== 1) {
    throw new Error(`expected exactly one match for ${label}, found ${values.length}`);
  }
  return values[0];
}

const componentSource = soleValue(COMPONENT_GLOB, COMPONENT_PATH);
const testSource = soleValue(TEST_GLOB, TEST_PATH);

// ---- generic, type-agnostic tree walk over svelte/compiler's plain-object AST ----

function findFirst(node: unknown, pred: (n: Record<string, unknown>) => boolean, seen = new Set<unknown>()): Record<string, unknown> | undefined {
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
  const obj = node as Record<string, unknown>;
  if (pred(obj)) return obj;
  for (const key of Object.keys(obj)) {
    if (key === 'parent') continue;
    const found = findFirst(obj[key], pred, seen);
    if (found) return found;
  }
  return undefined;
}

type SvelteNode = Record<string, unknown>;

// An Attribute's `.value` is either `true` (bare boolean attribute), an
// array of Text/ExpressionTag fragment nodes, or — observed directly by
// probing this exact file's parsed AST — a bare ExpressionTag object when
// the attribute is a single `{expr}` with nothing else. Handle both shapes
// rather than assuming one.
function attrExpression(attr: SvelteNode | undefined): SvelteNode | undefined {
  const v = attr?.value;
  if (v && typeof v === 'object' && !Array.isArray(v) && (v as SvelteNode).type === 'ExpressionTag') {
    return (v as SvelteNode).expression as SvelteNode;
  }
  if (Array.isArray(v) && v.length === 1 && (v[0] as SvelteNode)?.type === 'ExpressionTag') {
    return (v[0] as SvelteNode).expression as SvelteNode;
  }
  return undefined;
}

function attrText(attr: SvelteNode | undefined): string | undefined {
  const v = attr?.value;
  if (Array.isArray(v) && v.length === 1 && (v[0] as SvelteNode)?.type === 'Text') {
    return (v[0] as SvelteNode).data as string;
  }
  return undefined;
}

function findAttr(el: SvelteNode, name: string): SvelteNode | undefined {
  const attrs = el.attributes as SvelteNode[] | undefined;
  return attrs?.find((a) => a.type === 'Attribute' && a.name === name);
}

// ---- shape classification, shared between the component's real AST and
// the test helpers' real AST — this is the thing being compared, never a
// typed-out expected string ----

interface GateShape {
  kind: 'identity' | 'negation' | 'other';
  varName?: string;
  source: string;
}

function classifySvelteGate(expr: SvelteNode, source: string): GateShape {
  const start = expr.start as number;
  const end = expr.end as number;
  const text = source.slice(start, end);
  if (expr.type === 'Identifier') {
    return { kind: 'identity', varName: expr.name as string, source: text };
  }
  if (
    expr.type === 'UnaryExpression' &&
    expr.operator === '!' &&
    (expr.argument as SvelteNode | undefined)?.type === 'Identifier'
  ) {
    return { kind: 'negation', varName: (expr.argument as SvelteNode).name as string, source: text };
  }
  return { kind: 'other', source: text };
}

function classifyTsGate(expr: ts.Expression, paramName: string, sourceFile: ts.SourceFile): GateShape {
  if (ts.isIdentifier(expr) && expr.text === paramName) {
    return { kind: 'identity', varName: expr.text, source: expr.getText(sourceFile) };
  }
  if (
    ts.isPrefixUnaryExpression(expr) &&
    expr.operator === ts.SyntaxKind.ExclamationToken &&
    ts.isIdentifier(expr.operand) &&
    expr.operand.text === paramName
  ) {
    return { kind: 'negation', varName: expr.operand.text, source: expr.getText(sourceFile) };
  }
  return { kind: 'other', source: expr.getText(sourceFile) };
}

// ---- reading the component's real gates ----

/**
 * Finds the local variable bound to `$derived(guard.kind === 'sending')` —
 * by that SHAPE, not by an assumed name — so the rest of this file never
 * has to type the string "sending" to mean "the dialog's own submitting
 * state variable". If this wiring moves (renamed, restructured), this
 * throws with a message naming what changed, rather than silently
 * comparing against nothing.
 */
function findSendingVarName(instanceContent: unknown): string {
  const decl = findFirst(instanceContent, (n) => {
    if (n.type !== 'VariableDeclarator') return false;
    const id = n.id as SvelteNode | undefined;
    const init = n.init as SvelteNode | undefined;
    if (id?.type !== 'Identifier') return false;
    if (init?.type !== 'CallExpression') return false;
    const callee = init.callee as SvelteNode | undefined;
    if (callee?.type !== 'Identifier' || callee.name !== '$derived') return false;
    const arg = (init.arguments as SvelteNode[] | undefined)?.[0];
    if (arg?.type !== 'BinaryExpression' || arg.operator !== '===') return false;
    const left = arg.left as SvelteNode | undefined;
    const right = arg.right as SvelteNode | undefined;
    if (left?.type !== 'MemberExpression') return false;
    if ((left.property as SvelteNode | undefined)?.type !== 'Identifier') return false;
    if ((left.property as SvelteNode).name !== 'kind') return false;
    return right?.type === 'Literal' && right.value === 'sending';
  });
  if (!decl) {
    throw new Error(
      `${COMPONENT_PATH}: could not find a local variable declared as ` +
        "\`$derived(guard.kind === 'sending')\` — has the guard wiring moved? " +
        '(see #0186, #0189, #0195)',
    );
  }
  return (decl.id as SvelteNode).name as string;
}

function findInputDisabledExpr(fragment: unknown): SvelteNode {
  const input = findFirst(
    fragment,
    (n) =>
      n.type === 'RegularElement' &&
      n.name === 'input' &&
      attrText(findAttr(n, 'id')) === 'send-confirm-count',
  );
  if (!input) {
    throw new Error(`${COMPONENT_PATH}: could not find <input id="send-confirm-count">`);
  }
  const disabledAttr = findAttr(input, 'disabled');
  const expr = attrExpression(disabledAttr);
  if (!expr) {
    throw new Error(
      `${COMPONENT_PATH}: <input id="send-confirm-count">'s \`disabled\` attribute is missing or is not a single {expression}`,
    );
  }
  return expr;
}

function findBackdropDiv(fragment: unknown): SvelteNode {
  const div = findFirst(
    fragment,
    (n) => n.type === 'RegularElement' && n.name === 'div' && attrText(findAttr(n, 'role')) === 'presentation',
  );
  if (!div) {
    throw new Error(`${COMPONENT_PATH}: could not find the backdrop <div role="presentation">`);
  }
  return div;
}

function findBackdropClickGate(backdropDiv: SvelteNode, source: string): SvelteNode {
  const onclickAttr = findAttr(backdropDiv, 'onclick');
  const expr = attrExpression(onclickAttr);
  if (!expr || expr.type !== 'ArrowFunctionExpression') {
    throw new Error(`${COMPONENT_PATH}: the backdrop's \`onclick\` is not a single arrow-function expression`);
  }
  const body = expr.body as SvelteNode;
  if (body.type !== 'LogicalExpression' || body.operator !== '&&') {
    const start = body.start as number;
    const end = body.end as number;
    throw new Error(`${COMPONENT_PATH}: the backdrop's onclick body is not a \`&&\` guard: \`${source.slice(start, end)}\``);
  }
  const right = body.right as SvelteNode;
  const rightCallee = right.callee as SvelteNode | undefined;
  if (right.type !== 'CallExpression' || rightCallee?.type !== 'Identifier' || rightCallee.name !== 'onClose') {
    const start = right.start as number;
    const end = right.end as number;
    throw new Error(
      `${COMPONENT_PATH}: the backdrop onclick's right-hand side is not a call to onClose(), so this may not be the close gate: \`${source.slice(start, end)}\``,
    );
  }
  return body.left as SvelteNode;
}

function findKeydownHandlerName(backdropDiv: SvelteNode): string {
  const attr = findAttr(backdropDiv, 'onkeydown');
  const expr = attrExpression(attr);
  if (!expr || expr.type !== 'Identifier') {
    throw new Error(`${COMPONENT_PATH}: the backdrop's \`onkeydown\` is not a bare identifier reference to a handler function`);
  }
  return expr.name as string;
}

function findEscapeGate(instanceContent: unknown, handlerName: string, source: string): SvelteNode {
  const fn = findFirst(instanceContent, (n) => n.type === 'FunctionDeclaration' && (n.id as SvelteNode | undefined)?.name === handlerName);
  if (!fn) {
    throw new Error(`${COMPONENT_PATH}: could not find function \`${handlerName}\` in the <script> block`);
  }
  const ifStmt = findFirst(fn.body, (n) => {
    if (n.type !== 'IfStatement') return false;
    const test = n.test as SvelteNode | undefined;
    if (test?.type !== 'LogicalExpression' || test.operator !== '&&') return false;
    const left = test.left as SvelteNode | undefined;
    if (left?.type !== 'BinaryExpression') return false;
    const right = left.right as SvelteNode | undefined;
    return right?.type === 'Literal' && right.value === 'Escape';
  });
  if (!ifStmt) {
    throw new Error(`${COMPONENT_PATH}: could not find the \`'Escape'\` branch inside \`${handlerName}\``);
  }
  const test = ifStmt.test as SvelteNode;
  const src = source.slice(test.start as number, test.end as number);
  const right = test.right as SvelteNode | undefined;
  if (!right) {
    throw new Error(`${COMPONENT_PATH}: \`${handlerName}\`'s Escape check is not a two-part \`&&\` condition: \`${src}\``);
  }
  return right;
}

// ---- reading the guard test's claimed gates ----

function classifyTestHelper(sourceFile: ts.SourceFile, fnName: string): GateShape {
  let result: GateShape | undefined;
  const visit = (node: ts.Node): void => {
    if (ts.isFunctionDeclaration(node) && node.name?.text === fnName && node.body) {
      const param = node.parameters[0];
      const paramName = param && ts.isIdentifier(param.name) ? param.name.text : undefined;
      const returnStmt = node.body.statements.find(ts.isReturnStatement);
      if (paramName && returnStmt?.expression) {
        result = classifyTsGate(returnStmt.expression, paramName, sourceFile);
      }
    }
    if (!result) ts.forEachChild(node, visit);
  };
  visit(sourceFile);
  if (!result) {
    throw new Error(`${TEST_PATH}: could not find a single-return function \`${fnName}(x) { return ...x...; }\``);
  }
  return result;
}

// ---- #0204 item 2: pinning the premise the `oldSending`/`oldBlockedAgain`
// before/after block in CampaignSendDialog.guard.test.ts rests on ----

/**
 * True when `expr` is, at its top level (optionally negated by a single
 * `!`), exactly `inFlight` or exactly `unmet.length > 0` — the two formulas
 * `#0186` deleted from the component (`oldSending`/`oldBlockedAgain` in
 * CampaignSendDialog.guard.test.ts). Deliberately narrow: this is not
 * "mentions inFlight anywhere" (the real `$derived(sendGuardState({ ...,
 * inFlight }))` legitimately passes `inFlight` through, and must not trip
 * this), it is "a local's value comes directly from one of these two raw
 * expressions again, bypassing sendGuardState".
 */
function isDirectInFlightOrUnmetExpr(expr: SvelteNode): boolean {
  const inner =
    expr.type === 'UnaryExpression' && expr.operator === '!' ? (expr.argument as SvelteNode | undefined) : expr;
  if (!inner) return false;
  if (inner.type === 'Identifier' && inner.name === 'inFlight') return true;
  if (inner.type === 'BinaryExpression' && inner.operator === '>') {
    const left = inner.left as SvelteNode | undefined;
    const right = inner.right as SvelteNode | undefined;
    const isUnmetLength =
      left?.type === 'MemberExpression' &&
      (left.object as SvelteNode | undefined)?.type === 'Identifier' &&
      ((left.object as SvelteNode).name as string) === 'unmet' &&
      (left.property as SvelteNode | undefined)?.type === 'Identifier' &&
      ((left.property as SvelteNode).name as string) === 'length';
    const isZero = right?.type === 'Literal' && right.value === 0;
    if (isUnmetLength && isZero) return true;
  }
  return false;
}

/**
 * Searches every `$derived(...)`-bound local in the component's instance
 * script for one whose top-level expression is `isDirectInFlightOrUnmetExpr`
 * — i.e. a local derived directly from `inFlight` or `unmet.length > 0`
 * rather than routed through `sendGuardState`. Returns the offending
 * declarator, or undefined if none exists.
 */
function findDirectInFlightOrUnmetLocal(instanceContent: unknown): SvelteNode | undefined {
  return findFirst(instanceContent, (n) => {
    if (n.type !== 'VariableDeclarator') return false;
    const id = n.id as SvelteNode | undefined;
    const init = n.init as SvelteNode | undefined;
    if (id?.type !== 'Identifier') return false;
    if (init?.type !== 'CallExpression') return false;
    const callee = init.callee as SvelteNode | undefined;
    if (callee?.type !== 'Identifier' || callee.name !== '$derived') return false;
    const arg = (init.arguments as SvelteNode[] | undefined)?.[0] as SvelteNode | undefined;
    return !!arg && isDirectInFlightOrUnmetExpr(arg);
  });
}

describe('CampaignSendDialog structural guard (#0197)', () => {
  let sendingVar: string;
  let componentAst: SvelteNode;
  let testSourceFile: ts.SourceFile;

  beforeAll(() => {
    componentAst = parseSvelte(componentSource, { filename: COMPONENT_PATH, modern: true }) as unknown as SvelteNode;
    const instance = componentAst.instance as SvelteNode;
    sendingVar = findSendingVarName(instance.content);
    testSourceFile = ts.createSourceFile(TEST_PATH, testSource, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  });

  function assertGateMatchesHelper(label: string, actualExpr: SvelteNode, helperName: string): void {
    const actual = classifySvelteGate(actualExpr, componentSource);
    const expected = classifyTestHelper(testSourceFile, helperName);
    const detail =
      `${label}: ${COMPONENT_PATH} has \`${actual.source}\` (classified '${actual.kind}'` +
      `${actual.varName ? `, of \`${actual.varName}\`` : ''}), but ` +
      `${TEST_PATH}'s \`${helperName}\` claims '${expected.kind}' (\`${expected.source}\`) — ` +
      'the real gate no longer matches what the guard test asserts it is (see #0195, #0197)';
    expect(actual.kind, detail).toBe(expected.kind);
    if (actual.kind !== 'other') {
      expect(actual.varName, detail).toBe(sendingVar);
    }
  }

  it('the count input\'s `disabled` matches `countInputDisabled`\'s claim', () => {
    const expr = findInputDisabledExpr(componentAst.fragment);
    assertGateMatchesHelper('count-input disabled', expr, 'countInputDisabled');
  });

  it('Escape\'s guard matches `escapeCloses`\'s claim', () => {
    const backdropDiv = findBackdropDiv(componentAst.fragment);
    const handlerName = findKeydownHandlerName(backdropDiv);
    const instance = componentAst.instance as SvelteNode;
    const expr = findEscapeGate(instance.content, handlerName, componentSource);
    assertGateMatchesHelper('Escape', expr, 'escapeCloses');
  });

  it('the backdrop click guard matches `backdropCloses`\'s claim', () => {
    const backdropDiv = findBackdropDiv(componentAst.fragment);
    const expr = findBackdropClickGate(backdropDiv, componentSource);
    assertGateMatchesHelper('backdrop onclick', expr, 'backdropCloses');
  });

  it('all three gates are anchored to the same `$derived(guard.kind === \'sending\')` variable', () => {
    // Belt-and-braces: assertGateMatchesHelper already checks each gate's
    // varName against sendingVar individually. This just makes the shared
    // anchor explicit and would fail loudly if, say, the Escape branch
    // were checking a differently-named but identically-shaped local.
    const backdropDiv = findBackdropDiv(componentAst.fragment);
    const handlerName = findKeydownHandlerName(backdropDiv);
    const instance = componentAst.instance as SvelteNode;

    const disabledExpr = findInputDisabledExpr(componentAst.fragment);
    const escapeExpr = findEscapeGate(instance.content, handlerName, componentSource);
    const backdropExpr = findBackdropClickGate(backdropDiv, componentSource);

    // #0204: these three carried bare `expect(…).toBe(sendingVar)` calls
    // with no message, so a failure read only `expected undefined to be
    // 'sending'` — no file, no expression, nothing pasteable, unlike every
    // other assertion in this file. Give each the same `detail` treatment.
    function anchorDetail(label: string, expr: SvelteNode): string {
      const actual = classifySvelteGate(expr, componentSource);
      return (
        `${label} (shared-anchor check): ${COMPONENT_PATH} has \`${actual.source}\` ` +
        `(classified '${actual.kind}'${actual.varName ? `, of \`${actual.varName}\`` : ''}), but the ` +
        `shared anchor found by findSendingVarName is \`${sendingVar}\` — this gate is not bound to ` +
        'the same $derived variable as the others (see #0195, #0197, #0204)'
      );
    }

    expect(classifySvelteGate(disabledExpr, componentSource).varName, anchorDetail('count-input disabled', disabledExpr)).toBe(
      sendingVar,
    );
    expect(classifySvelteGate(escapeExpr, componentSource).varName, anchorDetail('Escape', escapeExpr)).toBe(sendingVar);
    expect(classifySvelteGate(backdropExpr, componentSource).varName, anchorDetail('backdrop onclick', backdropExpr)).toBe(
      sendingVar,
    );
  });

  // #0204 item 2: CampaignSendDialog.guard.test.ts's `oldSending`/
  // `oldBlockedAgain` before/after block only means anything if the
  // component genuinely no longer derives a local directly from `inFlight`
  // or `unmet.length > 0` — #0197's review checked that premise by hand
  // (grep) and declined to guard it, reasoning there was nothing live left
  // to structurally compare `oldSending`/`oldBlockedAgain` against. This
  // pins that premise instead of leaving it to a future grep: if a raw
  // derivation like this is ever reintroduced, the before/after block
  // silently stops describing anything, and this test is what notices.
  it('derives no local directly from `inFlight` or `unmet.length > 0` (#0186 premise)', () => {
    const instance = componentAst.instance as SvelteNode;
    const offender = findDirectInFlightOrUnmetLocal(instance.content);
    if (offender) {
      const id = offender.id as SvelteNode;
      const init = offender.init as SvelteNode;
      const arg = (init.arguments as SvelteNode[])[0] as SvelteNode;
      const start = arg.start as number;
      const end = arg.end as number;
      const exprText = componentSource.slice(start, end);
      throw new Error(
        `${COMPONENT_PATH}: \`${id.name as string}\` is \`$derived(${exprText})\` — a local derived directly ` +
          "from `inFlight` or `unmet.length > 0` again, bypassing sendGuardState. " +
          "CampaignSendDialog.guard.test.ts's oldSending/oldBlockedAgain before/after block compares against " +
          'these two raw formulas as dead code that nothing in the component matches any more; if a live local ' +
          'like this exists, that comparison needs to target it instead (see #0186, #0197, #0204)',
      );
    }
  });
});
