<!--
  The Schedule all confirmation (#0568): lists what will be scheduled and
  what is skipped, from the server's dry run. Modelled on
  CampaignSendDialog.svelte: role="dialog" aria-modal="true", the dialog
  container takes focus, and Escape closes it from the same node.
-->
<script lang="ts">
  import { tick } from 'svelte';
  import { adminSettingsHash } from '../../lib/adminRouter';
  import { formatPacificDateTime, formatPacificWeekdayTime, fromPacificInput, toPacificInput } from '../../lib/pacificTime';
  import { isModalEscape } from '../../lib/modalKeydown';
  import { scheduleSkipReasonLabel } from '../../lib/outreachLinks';
  import type { OutreachScheduleAllResult } from '../../lib/types';
  import Button from '../../lib/Button.svelte';

  interface Props {
    plan: OutreachScheduleAllResult;
    inFlight: boolean;
    errorMessage: string | null;
    onConfirm: (postIds: number[], times: Record<number, string>) => void;
    onClose: () => void;
  }
  let { plan, inFlight, errorMessage, onConfirm, onClose }: Props = $props();

  let dialogEl = $state<HTMLDivElement | null>(null);

  /** Reminder and Last call are opt-in (#0574); a drafted one is already opted in, so it starts ticked (#0587). */
  const optional = $derived(
    [...new Map(plan.to_schedule.filter((i) => i.wave !== 'announce').map((i) => [i.wave, i.wave_label])).entries()],
  );
  // svelte-ignore state_referenced_locally
  let included = $state<Record<string, boolean>>(
    Object.fromEntries(plan.to_schedule.filter((i) => i.wave !== 'announce').map((i) => [i.wave, true])),
  );
  const chosen = $derived(plan.to_schedule.filter((i) => i.wave === 'announce' || included[i.wave] === true));

  // Per-row one-off time edits (#0587): Pacific datetime-local text per post id.
  let editing = $state<Record<number, boolean>>({});
  let edits = $state<Record<number, string>>({});
  const overrides = $derived.by(() => {
    const out: Record<number, string> = {};
    for (const i of chosen) {
      const v = edits[i.post_id];
      if (v === undefined) continue;
      const iso = fromPacificInput(v);
      // An opened-but-unchanged row is not an override.
      if (iso && iso !== i.scheduled_at) out[i.post_id] = iso;
    }
    return out;
  });
  function timeOf(i: { post_id: number; scheduled_at: string }): string {
    return overrides[i.post_id] ?? i.scheduled_at;
  }
  function rowError(i: { post_id: number }): string {
    const v = edits[i.post_id];
    if (v === undefined) return '';
    const iso = fromPacificInput(v);
    if (!iso) return 'Pick a date and time.';
    return Date.parse(iso) <= Date.now() + 60000 ? 'That time has passed. Pick a later one.' : '';
  }
  const anyRowError = $derived(chosen.some((i) => rowError(i) !== ''));
  function startEdit(i: { post_id: number; scheduled_at: string }): void {
    edits[i.post_id] = edits[i.post_id] ?? toPacificInput(i.scheduled_at);
    editing[i.post_id] = true;
  }

  function joinNames(names: string[]): string {
    return names.length <= 1 ? (names[0] ?? '') : `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`;
  }
  const waveNames = $derived([...new Set(chosen.map((i) => i.wave_label))]);
  const heading = $derived(waveNames.length === 0 ? 'Schedule posts?' : `Schedule the ${joinNames(waveNames)}?`);

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
    <h2 class="modal-title">{heading}</h2>

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
      {#if chosen.length === 0}
        <p class="text-muted">Tick a wave above to schedule its posts.</p>
      {/if}
      <ul>
        {#each chosen as item (item.post_id)}
          <li>
            {item.channel_label} · {item.wave_label} — {formatPacificWeekdayTime(timeOf(item))}
            {#if !editing[item.post_id]}
              <button type="button" class="link" onclick={() => startEdit(item)} disabled={inFlight} aria-label={`Change time for ${item.channel_label} ${item.wave_label}`}>Change time</button>
            {:else}
              <label class="field">
                <span class="field-label">{item.channel_label} · {item.wave_label} time (Pacific)</span>
                <input type="datetime-local" bind:value={edits[item.post_id]} disabled={inFlight} />
              </label>
              {#if rowError(item)}<span class="text-error">{rowError(item)}</span>{/if}
            {/if}
          </li>
        {/each}
      </ul>
      <p class="text-muted">
        Times are Pacific. The posting hour is a setting:
        <a href={adminSettingsHash('outreach')}>change it in Settings</a>.
      </p>
    {/if}
    <p class="text-muted">Posts you make by hand (Instagram, TikTok, YouTube) are not scheduled.</p>

    {#if plan.skipped.length > 0}
      <h3>Skipped</h3>
      <ul>
        {#each plan.skipped as s (s.channel + ':' + s.wave)}
          <li>
            {s.channel_label} · {s.wave_label} — {scheduleSkipReasonLabel(s.reason)}{#if s.suggested_at}{' '}({formatPacificDateTime(s.suggested_at)}){/if}
          </li>
        {/each}
      </ul>
    {/if}

    {#if errorMessage}
      <p class="text-error" role="alert">{errorMessage}</p>
    {/if}

    <div class="row">
      <Button variant="primary" onclick={() => onConfirm(chosen.map((i) => i.post_id), overrides)} disabled={inFlight || chosen.length === 0 || anyRowError}>
        {inFlight ? 'Scheduling…' : chosen.length === 0 ? 'Nothing chosen' : `Schedule ${chosen.length} ${chosen.length === 1 ? 'post' : 'posts'}`}
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
  .link {
    background: none;
    border: 0;
    padding: 0;
    color: var(--accent);
    text-decoration: underline;
    cursor: pointer;
    font: inherit;
  }
  h3 {
    font-size: var(--fs-base);
    margin: var(--space-3) 0 var(--space-1);
  }
</style>
