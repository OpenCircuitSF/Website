<!--
  A generic in-app confirmation (#0581), replacing the browser's native dialog on the
  outreach pages: a title, caller-supplied body content, an optional error
  line and Confirm / Cancel. Modelled on OutreachScheduleAllDialog.svelte:
  role="dialog" aria-modal="true", the container takes focus, Escape closes it
  from the same node, and nothing closes it while a request is in flight.
-->
<script lang="ts">
  import { tick, type Snippet } from 'svelte';
  import { isModalEscape } from '../../lib/modalKeydown';
  import Button from '../../lib/Button.svelte';

  interface Props {
    title: string;
    inFlight: boolean;
    errorMessage: string | null;
    confirmLabel: string;
    danger?: boolean;
    /** Extra reason to keep Confirm disabled (e.g. an invalid field). */
    confirmDisabled?: boolean;
    onConfirm: () => void;
    onClose: () => void;
    children?: Snippet;
  }
  let { title, inFlight, errorMessage, confirmLabel, danger = false, confirmDisabled = false, onConfirm, onClose, children }: Props = $props();

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
    aria-label={title}
    tabindex="-1"
    bind:this={dialogEl}
    onclick={(e) => e.stopPropagation()}
    onkeydown={handleKeydown}
  >
    <h2 class="modal-title">{title}</h2>

    {@render children?.()}

    {#if errorMessage}
      <p class="text-error" role="alert">{errorMessage}</p>
    {/if}

    <div class="row">
      <Button variant={danger ? 'danger' : 'primary'} onclick={onConfirm} disabled={inFlight || confirmDisabled}>
        {inFlight ? 'Working…' : confirmLabel}
      </Button>
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
</style>
