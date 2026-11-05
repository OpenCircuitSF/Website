<!--
  A collapsed Reminder or Final row on the workshop outreach page (#0582):
  the pinned strip with its number, label and status (#0622), with the full
  message section (OutreachAnnouncement) inside once it is open. "Write it"
  sits beside the disclosure, not inside its summary, so it works while the row
  is collapsed: it drafts just this wave and then opens the row.
-->
<script lang="ts">
  import { draftAllOutreach, ApiError } from '../../lib/api';
  import { waveStrip } from '../../lib/outreachMessage';
  import type { OutreachPost, OutreachResults, OutreachWave, WorkshopOutreach } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import OutreachAnnouncement from './OutreachAnnouncement.svelte';
  import OutreachWaveStrip from './OutreachWaveStrip.svelte';

  interface Props {
    view: WorkshopOutreach;
    results: OutreachResults | null;
    wave: OutreachWave;
    workshopId: number;
    onChange: (post: OutreachPost) => void;
    reload: () => void;
  }
  let { view, results, wave, workshopId, onChange, reload }: Props = $props();

  let open = $state(false);
  let busy = $state(false);
  let errorText = $state('');

  const strip = $derived(waveStrip(view, wave.name));

  async function write(): Promise<void> {
    busy = true;
    errorText = '';
    try {
      await draftAllOutreach(workshopId, [wave.name]);
      reload();
      open = true;
    } catch (err) {
      errorText = err instanceof ApiError ? err.message : 'Could not write the posts. Please try again.';
    } finally {
      busy = false;
    }
  }
</script>

<div class="wave-row">
  <div class="row-head">
    <details bind:open>
      <summary class="wave-pin"><OutreachWaveStrip {strip} /></summary>
      {#if strip.action === 'open'}
        <OutreachAnnouncement {view} {results} wave={wave.name} {workshopId} {onChange} {reload} />
      {:else}
        <p class="text-muted">Nothing written for this one yet.</p>
      {/if}
    </details>
    {#if strip.action === 'write'}
      <Button onclick={write} disabled={busy || !view.available}>Write it</Button>
    {/if}
  </div>
  <p class="text-error" role="alert">{errorText}</p>
</div>

<style>
  .wave-row {
    border-top: var(--border-w) solid var(--border);
    padding: var(--space-2) 0;
  }
  .row-head {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    justify-content: space-between;
    gap: var(--space-2);
  }
  details {
    flex: 1 1 16rem;
    min-width: 0;
  }
  [role='alert']:empty {
    display: none;
  }
</style>
