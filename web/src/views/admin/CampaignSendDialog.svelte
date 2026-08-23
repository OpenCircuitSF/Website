<!--
  The send confirmation dialog (#0047 §7): the one place in this screen an
  operator actually triggers the irreversible action. Every fact shown here
  (subject, from, recipient count) comes from the `summary` prop, which the
  caller reads from GET .../preflight's SERVER-COMPUTED read of the stored
  row — never from the editor's own unsaved buffer. Confirming a destructive
  action against an operator's own textarea would be confirming against
  nothing.

  Markup and wiring only — every decision (whether the typed count matches,
  the hint text, whether the Send control may act) is a call into
  lib/sendConfirm.ts, via sendGuardState's `.kind` (#0186). This component
  holds no comparison and no `.trim()` of its own. The one literal it does
  hold is `status: 'draft'` in that sendGuardState call — a documented
  stand-in, not a re-derivation: the parent never mounts this dialog outside
  draft status (see the `guard` derivation's own comment below).
-->

<script lang="ts">
  import { tick } from 'svelte';
  import { confirmHint, sendGuardState } from '../../lib/sendConfirm';
  import type { PreflightResponse, UnmetRequirement } from '../../lib/types';
  import Button from '../../lib/Button.svelte';

  interface Props {
    summary: PreflightResponse['summary'];
    unmet: UnmetRequirement[];
    inFlight: boolean;
    errorMessage: string | null;
    onConfirm: (confirmCount: number) => void;
    onClose: () => void;
  }

  let { summary, unmet, inFlight, errorMessage, onConfirm, onClose }: Props = $props();

  let confirmRaw = $state('');
  let dialogEl = $state<HTMLDivElement | null>(null);

  // #0186: the single source of truth for the Send control's guard state
  // (lib/sendConfirm.ts's own doc comment) — this dialog must branch on
  // `.kind`, never recompute `inFlight`/`unmet` conditions itself. `status`
  // is hardcoded to 'draft' rather than threaded as a prop: the parent only
  // ever mounts this dialog while the campaign is draft (CampaignEditor's
  // onOpenSend gates on sendGateOpen) and closes it before a successful
  // send can move status past draft, so 'draft' is the one value it can
  // ever legitimately hold for as long as this component exists — the same
  // kind of documented stand-in CampaignEditor's own editorGuard uses.
  let guard = $derived(
    sendGuardState({
      status: 'draft',
      unmet,
      audienceCount: summary.recipients,
      confirmRaw,
      inFlight,
    }),
  );
  // `sending` replaces the old raw `inFlight` boolean everywhere below it
  // was used to disable controls — identical truth value (status is always
  // 'draft' here, and sendGuardState checks `inFlight` before `unmet`, so
  // `kind === 'sending'` iff `inFlight`), but now routed through the guard.
  let sending = $derived(guard.kind === 'sending');
  // `blockedAgain` replaces the old `unmet.length > 0` -- also routed
  // through the guard now, so it correctly defers to `sending` in the one
  // state where both were true at once (a retry submitted while the
  // pre-send checks had just failed again): `sendGuardState` checks
  // `inFlight` before `unmet`, so this panel steps aside for "Sending…"
  // instead of showing "no longer ready" copy that invites yet another
  // retry click while one is already outstanding — see this module's
  // header on why that precedence is not cosmetic.
  let blockedAgain = $derived(guard.kind === 'blocked');
  let hint = $derived(confirmHint(confirmRaw, summary.recipients));
  let fromDisplay = $derived(summary.from === '' ? 'Not configured' : summary.from);

  $effect(() => {
    // Focus the DIALOG CONTAINER, not the input, so the subject / from /
    // count / "mail cannot be unsent" are read by a screen reader before
    // the operator can type (#0047 §8, the same tick() + .focus() shape
    // #0036 used in Unsubscribe.svelte).
    void tick().then(() => dialogEl?.focus());
  });

  function handleKeydown(e: KeyboardEvent): void {
    if (e.key === 'Escape' && !sending) {
      onClose();
    }
  }

  function handleSubmit(e: SubmitEvent): void {
    e.preventDefault();
    // #0186: was `if (inFlight) return;` followed by a re-derived
    // count/match check that never looked at `unmet` at all -- a retry
    // typed correctly while `blockedAgain` was showing could still reach
    // onConfirm. Routing through `.kind === 'ready'` closes that gap: ready
    // implies confirmMatches(confirmRaw, summary.recipients), so the
    // confirmed count IS summary.recipients, with no need to re-derive it.
    if (guard.kind !== 'ready') return;
    onConfirm(summary.recipients);
  }
</script>

<div
  class="modal-backdrop"
  role="presentation"
  onclick={() => !sending && onClose()}
  onkeydown={handleKeydown}
>
  <div
    class="modal"
    role="dialog"
    aria-modal="true"
    aria-label="Confirm send"
    tabindex="-1"
    bind:this={dialogEl}
    onclick={(e) => e.stopPropagation()}
    onkeydown={(e) => e.stopPropagation()}
  >
    <h2 class="modal-title">Send this campaign?</h2>

    <dl class="send-summary">
      <dt>Subject</dt>
      <dd>{summary.subject}</dd>
      <dt>From</dt>
      <dd>{fromDisplay}</dd>
      <dt>Recipients</dt>
      <dd class="recipient-count">{summary.recipients}</dd>
    </dl>

    <p class="text-error">Mail cannot be unsent.</p>

    {#if blockedAgain}
      <div role="status" aria-live="polite" class="send-blocked">
        <p class="text-error">This campaign is no longer ready to send:</p>
        <ol>
          {#each unmet as item (item.code)}
            <li data-code={item.code}>
              <span aria-hidden="true">[ !! ]</span>
              {item.message}
            </li>
          {/each}
        </ol>
      </div>
    {/if}

    <form onsubmit={handleSubmit}>
      <div class="field">
        <label for="send-confirm-count">Type {summary.recipients} to confirm</label>
        <input
          id="send-confirm-count"
          type="text"
          inputmode="numeric"
          autocomplete="off"
          bind:value={confirmRaw}
          disabled={sending}
        />
        {#if hint}
          <p class="text-muted confirm-hint">{hint}</p>
        {/if}
      </div>

      {#if errorMessage}
        <p class="text-error" role="alert">{errorMessage}</p>
      {/if}

      <div class="row send-actions">
        <Button type="submit" variant="danger" disabled={sending}>
          {sending ? 'Sending…' : 'Send now'}
        </Button>
        <Button type="button" disabled={sending} onclick={onClose}>Cancel</Button>
      </div>
    </form>
  </div>
</div>

<style>
  .modal-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.4);
    display: flex;
    align-items: center;
    justify-content: center;
    padding: var(--space-4);
    z-index: 10;
  }
  .modal {
    background: var(--bg-panel);
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
    padding: var(--space-5);
    width: 100%;
    max-width: 460px;
  }
  .modal-title {
    font-size: var(--fs-lg);
    font-weight: 600;
    margin: 0 0 var(--space-4);
    overflow-wrap: anywhere;
  }
  .send-summary {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: var(--space-1) var(--space-3);
    margin: 0 0 var(--space-3);
  }
  .send-summary dt {
    color: var(--text-muted);
  }
  .send-summary dd {
    margin: 0;
    overflow-wrap: anywhere;
  }
  .recipient-count {
    font-size: var(--fs-lg);
    font-weight: 700;
  }
  .send-blocked {
    margin: var(--space-3) 0;
    padding: var(--space-2) var(--space-3);
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
  }
  .send-blocked ol {
    margin: var(--space-2) 0 0;
    padding-left: 0;
    list-style: none;
  }
  .send-blocked li {
    margin-bottom: var(--space-1);
  }
  .confirm-hint {
    margin: var(--space-1) 0 0;
  }
  .send-actions {
    margin-top: var(--space-4);
  }
</style>
