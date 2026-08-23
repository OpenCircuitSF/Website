// @vitest-environment jsdom
//
// #0094: the first behavioural (DOM-mounted) component test in this repo.
// Every other .svelte-adjacent test here (modalEscapeGuard.test.ts,
// modalKeydown.test.ts, CampaignSendDialog.structuralGuard.test.ts,
// CampaignSendDialog.guard.test.ts) proves its claim either by parsing the
// component's own AST or by re-deriving its formulas side by side — never by
// actually mounting the component, moving focus the way it does, and firing
// a real DOM event. That gap is what #0094 was filed to close, and this file
// is the proof it can be: it mounts CampaignSendDialog.svelte (#0120's own
// example of the exact bug this issue is about — see that component's
// `handleKeydown` doc comment) with @testing-library/svelte under jsdom,
// replicates the fix's tick()-then-focus() pattern by *waiting* for it
// rather than asserting the source shape, and presses a real Escape key.
//
// Deliberately scoped to ONE component, per #0094's acceptance criteria
// ("cover the conditional renders that gate user-visible affordances, with
// #0090's {#if} wrapper as the first case" — the same instruction, applied
// to #0120's Escape wiring instead, since that is the deferred verification
// four other issues this session explicitly left unproven: see this file's
// sibling issue notes). Wiring up every other modal is follow-up work, not
// this issue's deliverable — see issues/0094.md's "Implementation notes"
// (the "Newly-testable surface" paragraph) for the enumerated list of what
// is now testable but not yet tested. (There is no HANDOFF.md under web/ —
// web/ contains no .md file at all; the project-root HANDOFF.md does not
// cover this either.) A sibling file,
// ../../lib/modalFocusWiring.structuralGuard.test.ts (#0216), covers the
// same positive-wiring question structurally for the six sites this file
// does not mount: Admin.svelte's five modals and CampaignEditor.svelte's
// cancel dialog.
//
// What this test establishes, precisely: under jsdom, mounting this
// component moves focus into the dialog panel and a real 'keydown' event
// with key: 'Escape' dispatched at that focused element results in onClose
// being called — and does NOT when `inFlight` is true (sending). It does
// NOT establish that a real browser's focus/tab order, CSS-driven
// visibility, or screen-reader announcement behave the same way; jsdom's
// focus and event model is an approximation of a browser's, not the thing
// itself. CLAUDE.md has no section specifically on jsdom's fidelity to a
// real browser — the nearest relevant text is §5's verification rules, on
// what running a test does and does not prove in general — so the full
// caveat lives here and in this issue's Implementation notes, not there.
import { render, fireEvent, cleanup, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import CampaignSendDialog from './CampaignSendDialog.svelte';
import type { PreflightResponse, UnmetRequirement } from '../../lib/types';

afterEach(() => {
  cleanup();
});

const summary: PreflightResponse['summary'] = {
  subject: 'August workshop announcement',
  from: 'hello@opencircuitsf.com',
  recipients: 482,
};

const noUnmet: UnmetRequirement[] = [];

interface DialogProps {
  summary: PreflightResponse['summary'];
  status: string;
  unmet: UnmetRequirement[];
  inFlight: boolean;
  errorMessage: string | null;
}

function renderDialog(overrides: Partial<DialogProps> = {}) {
  const onConfirm = vi.fn();
  const onClose = vi.fn();
  const result = render(CampaignSendDialog, {
    props: {
      summary,
      status: 'draft',
      unmet: noUnmet,
      inFlight: false,
      errorMessage: null,
      ...overrides,
      onConfirm,
      onClose,
    },
  });
  return { ...result, onConfirm, onClose };
}

describe('CampaignSendDialog (#0120 Escape wiring, proven by mounting under #0094)', () => {
  it('moves focus into the dialog panel on mount (tick() + .focus(), the #0047 §8 / #0120 pattern)', async () => {
    const { container } = renderDialog();
    const dialog = container.querySelector('[role="dialog"]');
    expect(dialog).not.toBeNull();
    await waitFor(() => {
      expect(document.activeElement).toBe(dialog);
    });
  });

  // A genuinely new fact this issue's harness surfaced, not something the
  // AST guards could see: the component's own doc comment says "Binding the
  // identical function reference to both elements" (backdrop AND panel) —
  // and a real keydown DOES bubble from the focused panel up to the
  // backdrop in a real DOM (jsdom included), so ONE Escape keypress invokes
  // `handleKeydown` — and therefore `onClose()` — TWICE: once on the panel,
  // once again when the (unstopped) event reaches the backdrop's own
  // onkeydown. Nothing in this file calls stopPropagation anywhere in the
  // keydown path (deliberately, per that same comment), so this is real
  // production behavior, not a test artifact. It happens to be harmless
  // here because closing an already-closing dialog is idempotent from the
  // caller's side, but it was previously unverified by anything — worth a
  // follow-up issue if a future onClose ever becomes non-idempotent (e.g.
  // an analytics/audit-log side effect keyed on the close action).
  it('closes on Escape pressed while the dialog panel has focus (fires twice: panel handler + bubble to backdrop handler)', async () => {
    const { container, onClose } = renderDialog();
    const dialog = container.querySelector('[role="dialog"]') as HTMLElement;
    await waitFor(() => {
      expect(document.activeElement).toBe(dialog);
    });

    await fireEvent.keyDown(dialog, { key: 'Escape' });

    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it('does NOT close on Escape while a send is in flight (sending state wins — #0195)', async () => {
    const { container, onClose } = renderDialog({ inFlight: true });
    const dialog = container.querySelector('[role="dialog"]') as HTMLElement;
    await waitFor(() => {
      expect(document.activeElement).toBe(dialog);
    });

    await fireEvent.keyDown(dialog, { key: 'Escape' });

    expect(onClose).not.toHaveBeenCalled();
  });

  it('ignores non-Escape keys', async () => {
    const { container, onClose } = renderDialog();
    const dialog = container.querySelector('[role="dialog"]') as HTMLElement;
    await waitFor(() => {
      expect(document.activeElement).toBe(dialog);
    });

    await fireEvent.keyDown(dialog, { key: 'Enter' });

    expect(onClose).not.toHaveBeenCalled();
  });

  // The regression #0120 actually fixed: before that issue, the panel's own
  // onkeydown blindly called e.stopPropagation() on every keydown (mirroring
  // a click guard that had no keyboard analogue), which ate Escape before it
  // could reach the backdrop's handler. This asserts the OUTCOME (Escape
  // closes) rather than the source shape modalEscapeGuard.test.ts checks —
  // the two are complementary: that test would catch the shape regressing
  // back to blind stopPropagation even if this behavioural test were
  // accidentally skipped or its assertion weakened, and this test would
  // catch a fix that has the right shape but the wrong wiring (e.g. calling
  // the wrong handler, or one that no longer calls onClose).
  it('a full mount-focus-Escape cycle: two Escape presses close (2x each, see above), the non-Escape key in between closes nothing', async () => {
    const { container, onClose } = renderDialog();
    const dialog = container.querySelector('[role="dialog"]') as HTMLElement;
    await waitFor(() => expect(document.activeElement).toBe(dialog));

    await fireEvent.keyDown(dialog, { key: 'Escape' });
    await fireEvent.keyDown(dialog, { key: 'a' });
    await fireEvent.keyDown(dialog, { key: 'Escape' });

    expect(onClose).toHaveBeenCalledTimes(4);
  });
});
