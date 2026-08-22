<!--
  The send confirmation dialog (#0047 §7): the one place in this screen an
  operator actually triggers the irreversible action. Every fact shown here
  (subject, from, recipient count) comes from the `summary` prop, which the
  caller reads from GET .../preflight's SERVER-COMPUTED read of the stored
  row — never from the editor's own unsaved buffer. Confirming a destructive
  action against an operator's own textarea would be confirming against
  nothing.

  Markup and wiring only — every decision (whether the typed count matches,
  the hint text) is a call into lib/sendConfirm.ts. This component holds no
  comparison, no `.trim()`, and no status literal of its own.
-->
<script lang="ts">
  import { tick } from 'svelte';
  import { normalizeCountInput, confirmMatches, confirmHint } from '../../lib/sendConfirm';
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

  let hint = $derived(confirmHint(confirmRaw, summary.recipients));
  let blockedAgain = $derived(unmet.length > 0);
  let fromDisplay = $derived(summary.from === '' ? 'Not configured' : summary.from);

  $effect(() => {
    // Focus the DIALOG CONTAINER, not the input, so the subject / from /
    // count / "mail cannot be unsent" are read by a screen reader before
    // the operator can type (#0047 §8, the same tick() + .focus() shape
    // #0036 used in Unsubscribe.svelte).
    void tick().then(() => dialogEl?.focus());
  });

  function handleKeydown(e: KeyboardEvent): void {
    if (e.key === 'Escape' && !inFlight) {
      onClose();
    }
  }

  function handleSubmit(e: SubmitEvent): void {
    e.preventDefault();
    if (inFlight) return;
    const n = normalizeCountInput(confirmRaw);
    if (n === null || !confirmMatches(confirmRaw, summary.recipients)) {
      return;
    }
    onConfirm(n);
  }
</script>

<div
  class="modal-backdrop"
  role="presentation"
  onclick={() => !inFlight && onClose()}
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
          disabled={inFlight}
        />
        {#if hint}
          <p class="text-muted confirm-hint">{hint}</p>
        {/if}
      </div>

      {#if errorMessage}
        <p class="text-error" role="alert">{errorMessage}</p>
      {/if}

      <div class="row send-actions">
        <Button type="submit" variant="danger" disabled={inFlight}>
          {inFlight ? 'Sending…' : 'Send now'}
        </Button>
        <Button type="button" disabled={inFlight} onclick={onClose}>Cancel</Button>
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
