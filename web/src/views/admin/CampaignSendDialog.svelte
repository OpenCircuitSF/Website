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
  holds no comparison and no `.trim()` of its own. `status` is threaded in as
  a prop (#0189) — the parent (CampaignEditor's `campaign.status`) is the
  authoritative source; this component never hardcodes or re-derives it, so
  a future reuse of this dialog for a non-draft status (CampaignStore.Send
  also accepts `failed`, for a retry) is handed the real value instead of a
  silently wrong stand-in.
-->

<script lang="ts">
  import { tick } from 'svelte';
  import { confirmHint, sendGuardDescription, sendGuardState } from '../../lib/sendConfirm';
  import type { PreflightResponse, UnmetRequirement } from '../../lib/types';
  import Button from '../../lib/Button.svelte';

  interface Props {
    summary: PreflightResponse['summary'];
    status: string;
    unmet: UnmetRequirement[];
    inFlight: boolean;
    errorMessage: string | null;
    onConfirm: (confirmCount: number) => void;
    onClose: () => void;
  }

  let { summary, status, unmet, inFlight, errorMessage, onConfirm, onClose }: Props = $props();

  let confirmRaw = $state('');
  let dialogEl = $state<HTMLDivElement | null>(null);

  // #0186 / #0189: the single source of truth for the Send control's guard
  // state (lib/sendConfirm.ts's own doc comment) — this dialog must branch
  // on `.kind`, never recompute `inFlight`/`unmet` conditions itself.
  // `status` is the real prop from the parent (CampaignEditor's
  // `campaign.status`), not a hardcoded 'draft' literal — that stand-in was
  // safe only as long as this dialog was draft-only, and CampaignStore.Send
  // already accepts 'failed' for a retry.
  let guard = $derived(
    sendGuardState({
      status,
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
  // #0189, mirroring #0119's fix in CampaignEditor.svelte: in `blocked` and
  // `needs-confirm`, the Send button below is left enabled (not the native
  // `disabled` attribute — that stays reserved for `sending`) because
  // `handleSubmit` already refuses to act unless `guard.kind === 'ready'`.
  // An enabled-but-inert button needs its non-ready state announced, so it
  // carries `aria-disabled` plus an `aria-describedby` naming the reason.
  // Both ids it points at (`send-blocked-hint`, `confirm-hint` below) are
  // rendered UNCONDITIONALLY, not wrapped in `{#if}` — #0119's own defect
  // was exactly a dangling `aria-describedby` from a conditionally-rendered
  // target, so only their CONTENTS are conditional here.
  let guardDescription = $derived(sendGuardDescription(guard));

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

    <!-- #0189: rendered unconditionally (only the inner content is
         conditional) so the Send button's aria-describedby below always
         resolves to a real element, in every guard state -- see #0119. -->
    <div id="send-blocked-hint" role="status" aria-live="polite" class:send-blocked={blockedAgain}>
      {#if blockedAgain}
        <p class="text-error">{guardDescription}</p>
        <ol>
          {#each unmet as item (item.code)}
            <li data-code={item.code}>
              <span aria-hidden="true">[ !! ]</span>
              {item.message}
            </li>
          {/each}
        </ol>
      {/if}
    </div>

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
        <!-- #0189: same unconditional-container reasoning as send-blocked-hint
             above. Falls back to sendGuardDescription's generic needs-confirm
             text when the field is untouched (confirmHint returns null then,
             by design -- no scolding an empty field), so this id never
             resolves to empty content while guard.kind is 'needs-confirm'. -->
        <div id="confirm-hint" role="status" aria-live="polite">
          {#if hint}
            <p class="text-muted confirm-hint">{hint}</p>
          {:else if guard.kind === 'needs-confirm'}
            <p class="text-muted confirm-hint">{guardDescription}</p>
          {/if}
        </div>
      </div>

      {#if errorMessage}
        <p class="text-error" role="alert">{errorMessage}</p>
      {/if}

      <div class="row send-actions">
        <Button
          type="submit"
          variant="danger"
          disabled={sending}
          aria-disabled={guard.kind !== 'ready'}
          aria-describedby="send-blocked-hint confirm-hint"
        >
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
