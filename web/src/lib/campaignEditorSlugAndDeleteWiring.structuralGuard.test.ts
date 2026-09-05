// #0444: CampaignEditor.svelte is never mounted by any test (#0094's own
// jsdom harness is too expensive for it -- see
// modalFocusWiring.structuralGuard.test.ts's header, which names this exact
// file as one of the ten sites nothing mounts) and STORAGE=json has no
// mailing subsystem at all, so no end-to-end path exercises it either. Two
// legs of its wiring can silently break with every visible signal still
// reporting success:
//
//   - #0410 wired an editable archive `slug` into this component. Drop
//     `slug: slugToSend` from saveDraft's updateCampaign(...) payload and
//     the field still types, autosave still reports "Saved", and the
//     Archive URL preview still updates -- because #0410 deliberately made
//     that preview read the live edit buffer rather than the persisted
//     `campaign.slug`, which is the correct fix for a DIFFERENT problem
//     (PRD #6.8's "does the effect look obvious before saving") but also
//     happens to remove the last visible cue that a save silently failed.
//   - #0411 then added a **destructive** Delete-campaign action to the same
//     component. Its five web tests all cover the two pure helpers
//     (canDeleteCampaign, deleteCampaignConfirmMessage) -- nothing exercises
//     whether the confirm button is actually wired to deleteCampaign(...),
//     or whether the trigger's offer gate still reads the draft-only rule.
//
// This is the structural guard #0410's review recommended and #0444 (widened
// by #0411's review) files: parse CampaignEditor.svelte with svelte/compiler
// and check the real AST, not raw text -- the same technique
// modalFocusWiring.structuralGuard.test.ts and
// CampaignSendDialog.structuralGuard.test.ts already use for this class of
// silent-wiring defect, modelled closely on the latter's findFirst/
// attrExpression/attrText helpers.
//
// Deliberately five checks, not a general component-shape scanner (#0444
// criterion 3/6): the two LOAD-BEARING ones (updateCampaign's slug property,
// the delete-confirm handler's deleteCampaign(...) call -- #0444 criteria 1
// and 4) whose loss is genuinely silent, plus three cheap-once-parsed
// companions (the archive-URL preview's live-buffer source, the slug
// field's disabled gate, the delete trigger's offer gate -- criteria 2, 3,
// 5). #0411's review separately confirmed the SERVER refuses a non-draft
// delete independently, so the offer-gate check (5) guards a cosmetic UI
// defect, not a data-loss path -- criterion 4's check is the one that does.
//
// Every check below throws with a specific, file-and-expression-quoting
// message the moment its own extraction step finds nothing, rather than
// silently reporting zero violations for a scan that found no subject at
// all (CLAUDE.md's fail-open warning -- #0258, #0428). None of the checks
// compares against a quoted copy of the expected source sitting next to the
// component (CLAUDE.md #8's "oracle must not be the same bytes as its
// subject"): each expected identifier/call name is a distinct string
// (`slug`, `slugEditable`, `deleteCampaign`, `canDeleteCampaign`) compared
// against the AST node it names, never against a re-typed fragment of
// CampaignEditor.svelte's own source.
//
// Mutation-proved against a copy of this exact real file, one leg at a time
// -- see issues/0444.md's `## Verification` for the five failure messages
// and restore hashes.
import { describe, it, expect, beforeAll } from 'vitest';
import { parse as parseSvelte } from 'svelte/compiler';

const COMPONENT_GLOB = import.meta.glob('../views/admin/CampaignEditor.svelte', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

const COMPONENT_PATH = 'web/src/views/admin/CampaignEditor.svelte';

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
// AST -- same shape as CampaignSendDialog.structuralGuard.test.ts's findFirst
// and modalFocusWiring.structuralGuard.test.ts's collect* walkers.
function findFirst(
  node: unknown,
  pred: (n: SvelteNode) => boolean,
  seen = new Set<unknown>(),
): SvelteNode | undefined {
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

// An Attribute's `.value` is either `true` (bare boolean attribute), an
// array of Text/ExpressionTag fragment nodes, or a bare ExpressionTag object
// when the attribute is a single `{expr}` with nothing else (confirmed by
// probing this exact file's parsed AST -- disabled={!slugEditable} takes the
// bare-object form). Handle both shapes rather than assuming one.
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

function srcOf(node: SvelteNode | undefined): string {
  if (!node) return '<none>';
  const start = node.start as number | undefined;
  const end = node.end as number | undefined;
  if (start === undefined || end === undefined) return '<no span>';
  return componentSource.slice(start, end);
}

function isIdentifierNamed(node: SvelteNode | undefined, name: string): boolean {
  return node?.type === 'Identifier' && node.name === name;
}

function isCallTo(node: SvelteNode | undefined, calleeName: string): boolean {
  if (node?.type !== 'CallExpression') return false;
  return isIdentifierNamed(node.callee as SvelteNode | undefined, calleeName);
}

function findObjectProperty(objExpr: SvelteNode, keyName: string): SvelteNode | undefined {
  const props = (objExpr.properties as SvelteNode[] | undefined) ?? [];
  return props.find(
    (p) => p.type === 'Property' && isIdentifierNamed(p.key as SvelteNode | undefined, keyName),
  );
}

function findAll(node: unknown, pred: (n: SvelteNode) => boolean, out: SvelteNode[] = [], seen = new Set<unknown>()): SvelteNode[] {
  if (node === null || typeof node !== 'object') return out;
  if (seen.has(node)) return out;
  seen.add(node);
  if (Array.isArray(node)) {
    for (const item of node) findAll(item, pred, out, seen);
    return out;
  }
  const obj = node as SvelteNode;
  if (pred(obj)) out.push(obj);
  for (const key of Object.keys(obj)) {
    if (key === 'parent') continue;
    findAll(obj[key], pred, out, seen);
  }
  return out;
}

function spanLength(node: SvelteNode): number {
  const start = (node.start as number | undefined) ?? 0;
  const end = (node.end as number | undefined) ?? 0;
  return end - start;
}

// #0451: scoped to <script>'s TOP-LEVEL statement list only -- deliberately
// not a recursive findFirst walk. A recursive walk would also match a nested
// function or nested variable declarator sharing the target name (e.g. one
// declared inside another handler's body), which is exactly the decoy
// #0444's review constructed against assertion 4 (a nested `onConfirmDelete`
// calling deleteCampaign(...), declared earlier, satisfying the check even
// with the real top-level handler's call deleted). `saveDraft` and
// `onConfirmDelete` are both top-level FunctionDeclarations in
// CampaignEditor.svelte today (confirmed against the real parsed AST), so
// this scoping changes nothing for either of this guard's two callers.
function findFunctionOrArrowByName(instanceContent: unknown, name: string): SvelteNode {
  const body = (instanceContent as SvelteNode).body as unknown[] | undefined;
  if (!Array.isArray(body)) {
    throw new Error(`${COMPONENT_PATH}: <script> content has no top-level statement list`);
  }
  for (const stmt of body) {
    const s = stmt as SvelteNode;
    if (s.type === 'FunctionDeclaration' && isIdentifierNamed(s.id as SvelteNode | undefined, name)) {
      return s;
    }
    if (s.type === 'VariableDeclaration') {
      const decls = (s.declarations as SvelteNode[] | undefined) ?? [];
      for (const d of decls) {
        if (d.type === 'VariableDeclarator' && isIdentifierNamed(d.id as SvelteNode | undefined, name)) {
          const init = d.init as SvelteNode | undefined;
          if (init && (init.type === 'ArrowFunctionExpression' || init.type === 'FunctionExpression')) {
            return init;
          }
        }
      }
    }
  }
  throw new Error(
    `${COMPONENT_PATH}: could not find a top-level function or arrow-function-assigned variable named \`${name}\` in <script>`,
  );
}

function findDerivedDeclarator(instanceContent: unknown, name: string): SvelteNode {
  const decl = findFirst(
    instanceContent,
    (n) => n.type === 'VariableDeclarator' && isIdentifierNamed(n.id as SvelteNode | undefined, name),
  );
  if (!decl) {
    throw new Error(`${COMPONENT_PATH}: could not find the declaration of \`${name}\``);
  }
  return decl;
}

describe('CampaignEditor slug and delete wiring (#0444)', () => {
  let componentAst: SvelteNode;
  let instanceContent: unknown;

  beforeAll(() => {
    componentAst = parseSvelte(componentSource, { filename: COMPONENT_PATH, modern: true }) as unknown as SvelteNode;
    instanceContent = (componentAst.instance as SvelteNode).content;
  });

  // #0444 criterion 1 -- LOAD-BEARING. Dropping this property is the exact
  // silent regression this issue was filed for: every other visible signal
  // (typing, "Saved", the archive URL preview) keeps reporting success.
  it("saveDraft's updateCampaign(...) call still sends a `slug` property", () => {
    // #0451: scoped to saveDraft's own body -- a whole-<script> search would
    // also match an earlier, unrelated updateCampaign(...) call carrying its
    // own `slug` property, which is exactly the decoy #0444's review
    // constructed against this assertion.
    const saveDraftFn = findFunctionOrArrowByName(instanceContent, 'saveDraft');
    const call = findFirst(saveDraftFn.body, (n) => isCallTo(n, 'updateCampaign'));
    if (!call) {
      throw new Error(
        `${COMPONENT_PATH}: could not find a call to updateCampaign(...) inside saveDraft's body -- has saveDraft's PATCH call moved or been renamed? (#0444)`,
      );
    }
    const args = call.arguments as SvelteNode[] | undefined;
    const payload = args?.[1];
    if (!payload || payload.type !== 'ObjectExpression') {
      throw new Error(`${COMPONENT_PATH}: updateCampaign(...)'s second argument is not a single {..} object literal`);
    }
    const slugProp = findObjectProperty(payload, 'slug');
    if (!slugProp) {
      throw new Error(
        `${COMPONENT_PATH}: updateCampaign(...)'s payload (\`${srcOf(payload)}\`) has no \`slug\` property -- ` +
          'dropping it would silently stop persisting the archive-slug edit while typing, autosave\'s "Saved" ' +
          'message, and the Archive URL preview all keep reporting success (#0444\'s Description)',
      );
    }
    expect(slugProp).toBeDefined();
  });

  // #0444 criterion 2. #0410 deliberately built this preview off the live
  // edit buffer, not the persisted campaign.slug, so it updates as the
  // operator types; a regression to `campaign.slug` would silently start
  // previewing the SAVED value again.
  it("archiveURLValue's archiveURL(...) call passes the live `slug` buffer, not `campaign.slug`", () => {
    const decl = findDerivedDeclarator(instanceContent, 'archiveURLValue');
    const call = findFirst(decl.init, (n) => isCallTo(n, 'archiveURL'));
    if (!call) {
      throw new Error(
        `${COMPONENT_PATH}: archiveURLValue's initializer (\`${srcOf(decl.init as SvelteNode)}\`) has no archiveURL(...) call -- has #0410's wiring moved?`,
      );
    }
    const args = call.arguments as SvelteNode[] | undefined;
    const campaignArg = args?.[1];
    if (!campaignArg || campaignArg.type !== 'ObjectExpression') {
      throw new Error(`${COMPONENT_PATH}: archiveURL(...)'s second argument is not a single {..} object literal`);
    }
    const slugProp = findObjectProperty(campaignArg, 'slug');
    if (!slugProp) {
      throw new Error(`${COMPONENT_PATH}: archiveURL(...)'s payload (\`${srcOf(campaignArg)}\`) has no \`slug\` property`);
    }
    const value = slugProp.value as SvelteNode;
    const isLiveBuffer = isIdentifierNamed(value, 'slug');
    if (!isLiveBuffer) {
      throw new Error(
        `${COMPONENT_PATH}: archiveURL(...)'s \`slug\` is \`${srcOf(value)}\`, not the live \`slug\` edit buffer -- ` +
          '#0410 built the preview off the unsaved buffer so it updates as the operator types; reading ' +
          '`campaign.slug` instead would silently revert to previewing the SAVED value (#0444 criterion 2)',
      );
    }
    expect(isLiveBuffer).toBe(true);
  });

  // #0444 criterion 3.
  it('<input id="campaign-slug">\'s `disabled` expression reads `slugEditable`', () => {
    const input = findFirst(
      componentAst.fragment,
      (n) => n.type === 'RegularElement' && n.name === 'input' && attrText(findAttr(n, 'id')) === 'campaign-slug',
    );
    if (!input) {
      throw new Error(`${COMPONENT_PATH}: could not find <input id="campaign-slug">`);
    }
    const disabledAttr = findAttr(input, 'disabled');
    const expr = attrExpression(disabledAttr);
    if (!expr) {
      throw new Error(`${COMPONENT_PATH}: <input id="campaign-slug"> has no disabled={...} expression attribute`);
    }
    const readsSlugEditable = !!findFirst(expr, (n) => isIdentifierNamed(n, 'slugEditable'));
    if (!readsSlugEditable) {
      throw new Error(
        `${COMPONENT_PATH}: <input id="campaign-slug">'s disabled expression is \`${srcOf(expr)}\`, which does ` +
          'not reference `slugEditable` -- #0410\'s draft-only edit gate (canEditCampaignSlug) would no longer govern this field',
      );
    }
    expect(readsSlugEditable).toBe(true);
  });

  // #0444 criterion 5 (from the "Added 2026-09-05" section) -- LOAD-BEARING.
  // #0411's five web tests all cover the two pure helpers; nothing exercises
  // whether the confirm button is actually wired to deleteCampaign(...).
  it("the delete-confirm modal's confirm handler still calls deleteCampaign(...)", () => {
    const deleteDialogIfBlock = findFirst(
      componentAst.fragment,
      (n) => n.type === 'IfBlock' && srcOf(n.test as SvelteNode) === 'deleteDialogOpen',
    );
    if (!deleteDialogIfBlock) {
      throw new Error(
        `${COMPONENT_PATH}: could not find {#if deleteDialogOpen} -- has #0411's delete-confirm dialog been renamed or removed? (#0444)`,
      );
    }
    const confirmButton = findFirst(
      deleteDialogIfBlock.consequent,
      (n) => n.type === 'Component' && n.name === 'Button' && attrText(findAttr(n, 'variant')) === 'danger',
    );
    if (!confirmButton) {
      throw new Error(
        `${COMPONENT_PATH}: {#if deleteDialogOpen}'s modal has no <Button variant="danger"> confirm control`,
      );
    }
    const onclickExpr = attrExpression(findAttr(confirmButton, 'onclick'));
    if (!onclickExpr || onclickExpr.type !== 'Identifier') {
      throw new Error(
        `${COMPONENT_PATH}: the delete-confirm <Button variant="danger">'s onclick is not a single identifier handler reference (\`${srcOf(onclickExpr)}\`)`,
      );
    }
    const handlerName = onclickExpr.name as string;
    const fn = findFunctionOrArrowByName(instanceContent, handlerName);
    const call = findFirst(fn.body, (n) => isCallTo(n, 'deleteCampaign'));
    if (!call) {
      throw new Error(
        `${COMPONENT_PATH}: \`${handlerName}\` (wired to the delete-confirm button) does not call deleteCampaign(...) -- ` +
          "#0411's own web tests all cover its two pure helpers, not this wiring, so a dropped call here would be " +
          'silent (#0444\'s "Added 2026-09-05" section)',
      );
    }
    expect(call).toBeDefined();
  });

  // #0444 criterion 6 (from the "Added 2026-09-05" section). #0411's review
  // confirmed the SERVER refuses a non-draft delete independently, so this
  // check guards a cosmetic UI defect, not a data-loss path -- unlike the
  // load-bearing check just above.
  it('the "Delete campaign" trigger\'s offer gate reads the draft-only canDeleteCampaign(...) gate', () => {
    // Every {#if} whose consequent CONTAINS the trigger button qualifies
    // (an outer, unrelated {:else if campaign} branch also "contains" it,
    // transitively) -- the NEAREST one, i.e. the one with the smallest
    // source span, is the actual offer gate.
    const candidateIfBlocks = findAll(componentAst.fragment, (n) => {
      if (n.type !== 'IfBlock') return false;
      return !!findFirst(n.consequent, (m) => {
        if (m.type !== 'Component' || m.name !== 'Button') return false;
        const onclickExpr = attrExpression(findAttr(m, 'onclick'));
        return isIdentifierNamed(onclickExpr, 'openDelete');
      });
    });
    const offerIfBlock = candidateIfBlocks.sort((a, b) => spanLength(a) - spanLength(b))[0];
    if (!offerIfBlock) {
      throw new Error(
        `${COMPONENT_PATH}: could not find the {#if ...} wrapping the "Delete campaign" trigger ` +
          '(<Button onclick={openDelete}>) -- has #0411\'s offer gate moved? (#0444)',
      );
    }
    // #0451 (the "Also worth fixing" note, revised on this issue's second
    // pass per its reviewer's bounce): a genuine offer gate has the trigger
    // as a DIRECT CHILD of its consequent -- not "exactly one non-whitespace
    // node", which false-failed on an innocuous markup edit (an HTML comment
    // or a sibling hint element added inside `{#if deleteOffered}`, both
    // verified to pass before this commit and to false-fail under the
    // sibling-count check). If `{#if deleteOffered}` is deleted outright, the
    // nearest-by-span candidate that remains is a much larger containing
    // block (the outer `{:else if campaign}` branch) whose trigger is nested
    // deeper than a direct child, which was never a dedicated gate for this
    // button. Naming that mismatch directly, before resolving its unrelated
    // `test` identifier, turns the previously "confusing but fail-closed"
    // message (naming `campaign` as though it were the gate) into one that
    // names the real defect: the gate is gone, not mispointed.
    const consequentNodes = ((offerIfBlock.consequent as SvelteNode).nodes as SvelteNode[] | undefined) ?? [];
    const directlyWrapsTheTrigger = consequentNodes.some(
      (n) =>
        n.type === 'Component' &&
        n.name === 'Button' &&
        isIdentifierNamed(attrExpression(findAttr(n, 'onclick')), 'openDelete'),
    );
    if (!directlyWrapsTheTrigger) {
      throw new Error(
        `${COMPONENT_PATH}: the {#if ...} nearest the "Delete campaign" trigger (\`${srcOf(offerIfBlock.test as SvelteNode)}\`) ` +
          'has that trigger nested somewhere deeper rather than as a direct child -- the dedicated offer gate ' +
          "{#if} appears to be missing entirely, not merely re-pointed to a different gate variable (#0451, from #0444's review)",
      );
    }
    const test = offerIfBlock.test as SvelteNode;
    if (test.type !== 'Identifier') {
      throw new Error(
        `${COMPONENT_PATH}: the Delete-campaign offer gate's {#if} test is \`${srcOf(test)}\`, not a single ` +
          'identifier naming a $derived gate variable',
      );
    }
    const gateVarName = test.name as string;
    const decl = findDerivedDeclarator(instanceContent, gateVarName);
    const call = findFirst(decl.init, (n) => isCallTo(n, 'canDeleteCampaign'));
    if (!call) {
      throw new Error(
        `${COMPONENT_PATH}: \`${gateVarName}\` is \`${srcOf(decl.init as SvelteNode)}\`, which does not call ` +
          'canDeleteCampaign(...) -- the Delete control\'s offer gate would no longer trace to the draft-only rule ' +
          "#0411 established (this leg is cosmetic per #0411's review: the server refuses independently -- but a " +
          'missing gate is still the UI defect this check pins)',
      );
    }
    expect(call).toBeDefined();
  });
});
