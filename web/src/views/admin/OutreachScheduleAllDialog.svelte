<!--
  The Schedule all confirmation (#0568): lists what will be scheduled and
  what is skipped, from the server's dry run. Modelled on
  CampaignSendDialog.svelte: role="dialog" aria-modal="true", the dialog
  container takes focus, and Escape closes it from the same node.
-->
<script lang="ts">
  import { tick } from 'svelte';
  import { formatDateTime } from '../../lib/admin';
  import { isModalEscape } from '../../lib/modalKeydown';
  import { scheduleSkipReasonLabel } from '../../lib/outreachLinks';
  import type { OutreachScheduleAllResult } from '../../lib/types';
  import Button from '../../lib/Button.svelte';

  interface Props {
    plan: OutreachScheduleAllResult;
    inFlight: boolean;
    errorMessage: string | null;
    onConfirm: (postIds: number[]) => void;
    onClose: () => void;
  }
  let { plan, inFlight, errorMessage, onConfirm, onClose }: Props = $props();

  let dialogEl = $state<HTMLDivElement | null>(null);

  /** Reminder and Last call are opt-in (#0574): unchecked until the admin picks them. */
  const optional = $derived(
    [...new Map(plan.to_schedule.filter((i) => i.wave !== 'announce').map((i) => [i.wave, i.wave_label])).entries()],
  );
  let included = $state<Record<string, boolean>>({});
  const chosen = $derived(plan.to_schedule.filter((i) => i.wave === 'announce' || included[i.wave] === true));

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
    aria-label="Confirm schedule all"
    tabindex="-1"
    bind:this={dialogEl}
    onclick={(e) => e.stopPropagation()}
    onkeydown={handleKeydown}
  >
    <h2 class="modal-title">Schedule all drafted posts?</h2>

    <h3>Will be scheduled</h3>
    {#if plan.to_schedule.length === 0}
      <p class="text-muted">Nothing can be scheduled right now.</p>
    {:else}
      {#if optional.length > 0}
        <fieldset class="waves">
          <legend class="text-muted">Include</legend>
          {#each optional as [wave, label] (wave)}
            <label><input type="checkbox" bind:checked={included[wave]} disabled={inFlight} /> {label}</label>
          {/each}
        </fieldset>
      {/if}
      <ul>
        {#each plan.to_schedule as item (item.post_id)}
          <li class:muted={item.wave !== 'announce' && !included[item.wave]}>{item.channel_label} · {item.wave_label} — {formatDateTime(item.scheduled_at)}</li>
        {/each}
      </ul>
    {/if}

    {#if plan.skipped.length > 0}
      <h3>Skipped</h3>
      <ul>
        {#each plan.skipped as s (s.channel + ':' + s.wave)}
          <li>
            {s.channel_label} · {s.wave_label} — {scheduleSkipReasonLabel(s.reason)}{#if s.suggested_at}{' '}({formatDateTime(s.suggested_at)}){/if}
          </li>
        {/each}
      </ul>
    {/if}

    {#if errorMessage}
      <p class="text-error" role="alert">{errorMessage}</p>
    {/if}

    <div class="row">
      <Button variant="primary" onclick={() => onConfirm(chosen.map((i) => i.post_id))} disabled={inFlight || chosen.length === 0}>
        {inFlight ? 'Scheduling…' : `Schedule ${chosen.length} ${chosen.length === 1 ? 'post' : 'posts'}`}
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
  .waves {
    border: 0;
    padding: 0;
    margin: 0 0 var(--space-2);
    display: flex;
    gap: var(--space-3);
  }
  li.muted {
    opacity: 0.6;
  }
  h3 {
    font-size: var(--fs-base);
    margin: var(--space-3) 0 var(--space-1);
  }
</style>
