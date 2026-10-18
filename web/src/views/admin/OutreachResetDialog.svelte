<!--
  The Reset outreach confirmation (#0592): says exactly what is removed and
  what is kept. Modelled on OutreachScheduleAllDialog.svelte: role="dialog"
  aria-modal="true", the container takes focus, Escape closes it.
-->
<script lang="ts">
  import { tick } from 'svelte';
  import { isModalEscape } from '../../lib/modalKeydown';
  import Button from '../../lib/Button.svelte';

  interface Props {
    inFlight: boolean;
    errorMessage: string | null;
    onConfirm: () => void;
    onClose: () => void;
  }
  let { inFlight, errorMessage, onConfirm, onClose }: Props = $props();

  let dialogEl = $state<HTMLDivElement | null>(null);

  $effect(() => {
    void tick().then(() => dialogEl?.focus());
  });

  function handleKeydown(e: KeyboardEvent): void {
    if (isModalEscape(e) && !inFlight) onClose();
  }
</script>

<div class="modal-backdrop" role="presentation" onclick={() => !inFlight && onClose()} onkeydown={handleKeydown}>
  <div
    class="modal"
    role="dialog"
    aria-modal="true"
    aria-label="Confirm reset outreach"
    tabindex="-1"
    bind:this={dialogEl}
    onclick={(e) => e.stopPropagation()}
    onkeydown={handleKeydown}
  >
    <h2 class="modal-title">Reset this workshop's outreach?</h2>
    <h3>Removed</h3>
    <ul>
      <li>Every drafted, scheduled or failed post, with its text.</li>
      <li>Every flyer and its location choices.</li>
    </ul>
    <h3>Kept</h3>
    <ul>
      <li>The tracked short links and their click counts. Drafting again reuses them.</li>
      <li>Flyer QR codes and the saved locations.</li>
    </ul>
    <p class="text-muted">Nothing has been posted, so nothing public changes. This cannot be undone.</p>

    {#if errorMessage}
      <p class="text-error" role="alert">{errorMessage}</p>
    {/if}

    <div class="row">
      <Button variant="primary" onclick={onConfirm} disabled={inFlight}>{inFlight ? 'Resetting…' : 'Reset outreach'}</Button>
      <Button onclick={onClose} disabled={inFlight}>Cancel</Button>
    </div>
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
    max-width: 520px;
    max-height: 90vh;
    overflow: auto;
  }
  .modal-title {
    font-size: var(--fs-lg);
    font-weight: 600;
    margin: 0 0 var(--space-4);
  }
  h3 {
    font-size: var(--fs-base);
    margin: var(--space-3) 0 var(--space-1);
  }
</style>
