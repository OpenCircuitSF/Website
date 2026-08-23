// #0120: "Escape does not close any admin modal" traced to one repeated
// shape across web/src/views/Admin.svelte, CampaignEditor.svelte, and
// Campaigns.svelte -- the modal PANEL's own keydown handler was
// `onkeydown={(e) => e.stopPropagation()}`, added to mirror the click guard
// that keeps a click inside the panel from reaching the backdrop's
// click-outside-to-dismiss `onclick`. There is no keyboard analogue of that
// click behaviour -- nothing about pressing a key "inside" vs. "outside" the
// panel -- so the mirrored guard did nothing but eat every keydown
// (including Escape) before it could bubble to the backdrop's own handler.
// Since each of these modals moves focus INTO the panel once it opens (see
// each component's own `$effect` + `tick().then(() => el?.focus())`, the
// same shape #0052 established for WorkshopEditor.svelte/Workshops.svelte),
// focus during normal use is always inside the panel, which made the
// backdrop's Escape handler unreachable dead code in every one of these
// components.
//
// isModalEscape is the one place that answers "does this keydown close the
// modal" -- both the panel's own onkeydown and the backdrop's now call it,
// so there is exactly one Escape rule per modal to read and to test, not one
// copy per element. `dismissible` exists for a modal whose own state must
// not be Escaped away from under itself while some action it started is
// still outstanding (mirrors an existing disabled-Cancel-button guard);
// pass `false` while that state holds. Every modal in this codebase today
// passes the default (always dismissible) -- see CampaignSendDialog.svelte,
// which keeps its own hand-written `e.key === 'Escape' && !sending` shape
// instead of calling this function, because #0197 built a structural test
// (CampaignSendDialog.structuralGuard.test.ts) that reads that exact
// expression shape out of the component's AST; swapping it for a call to
// this function would require deliberately rewriting that guard, which is
// out of proportion to this fix.
export function isModalEscape(e: Pick<KeyboardEvent, 'key'>, dismissible = true): boolean {
  return dismissible && e.key === 'Escape';
}
