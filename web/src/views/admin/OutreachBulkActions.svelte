<!--
  Draft all and Schedule all on a workshop's outreach page (#0568). Draft all
  fills every empty cell and never touches an existing post. Schedule all
  previews first (a dry run), shows the server's own plan in a confirm
  dialog, and confirms with exactly the previewed ids. Wording and decisions
  are calls into lib/outreachLinks.ts.
-->
<script lang="ts">
  import { draftAllOutreach, scheduleAllOutreach, ApiError } from '../../lib/api';
  import { adminSettingsHash, adminWorkshopHash } from '../../lib/adminRouter';
  import { draftAllMessage, scheduleAllMessage, canScheduleAll } from '../../lib/outreachLinks';
  import type { OutreachScheduleAllResult, WorkshopOutreach } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import OutreachScheduleAllDialog from './OutreachScheduleAllDialog.svelte';

  interface Props {
    workshopId: number;
    view: WorkshopOutreach;
    onDone: () => void;
  }
  let { workshopId, view, onDone }: Props = $props();

  /** Reminder and Last call are opt-in (#0574). */
  let extraWaves = $state<Record<string, boolean>>({ reminder: false, 'last-call': false });
  let busy = $state(false);
  let status = $state('');
  let errorText = $state('');
  let plan = $state<OutreachScheduleAllResult | null>(null);
  let confirming = $state(false);
  let dialogError = $state<string | null>(null);

  const gate = $derived(canScheduleAll(view));

  function message(err: unknown, fallback: string): string {
    return err instanceof ApiError ? err.message : fallback;
  }

  async function draftAll(): Promise<void> {
    busy = true;
    status = '';
    errorText = '';
    try {
      const picked = Object.keys(extraWaves).filter((w) => extraWaves[w]);
      const r = await draftAllOutreach(workshopId, picked);
      status = draftAllMessage(r);
      onDone();
    } catch (err) {
      errorText = message(err, 'Could not draft the posts. Please try again.');
    } finally {
      busy = false;
    }
  }

  async function preview(): Promise<void> {
    busy = true;
    status = '';
    errorText = '';
    dialogError = null;
    try {
      plan = await scheduleAllOutreach(workshopId, { dry_run: true });
    } catch (err) {
      errorText = message(err, 'Could not preview the schedule. Please try again.');
    } finally {
      busy = false;
    }
  }

  function close(): void {
    plan = null;
    dialogError = null;
  }

  async function confirm(postIds: number[]): Promise<void> {
    if (!plan) return;
    confirming = true;
    dialogError = null;
    try {
      const r = await scheduleAllOutreach(workshopId, { dry_run: false, post_ids: postIds });
      plan = null;
      status = scheduleAllMessage(r);
      onDone();
    } catch (err) {
      dialogError = message(err, 'Could not schedule the posts. Please try again.');
    } finally {
      confirming = false;
    }
  }
</script>

<div class="bulk">
  <div class="row">
    <Button onclick={draftAll} disabled={busy}>Draft all</Button>
    <Button onclick={preview} disabled={busy || !gate.ok}>Schedule all</Button>
  </div>
  <fieldset class="waves">
    <legend class="text-muted">Also draft</legend>
    <label><input type="checkbox" bind:checked={extraWaves.reminder} disabled={busy} /> Reminder</label>
    <label><input type="checkbox" bind:checked={extraWaves['last-call']} disabled={busy} /> Last call</label>
  </fieldset>
  <p class="text-muted">Draft all writes the Announce post for every channel, and the Reminder and Last call posts you tick, and never changes an existing post. Schedule all shows a preview of the automatic posts it would schedule, then asks you to confirm.</p>
  {#if !gate.ok && gate.reason}
    <p class="text-muted">
      {gate.reason}
      {#if view.workshop.status !== 'published'}
        <a href={adminWorkshopHash(workshopId)}>Open the workshop editor</a>
      {:else}
        <a href={adminSettingsHash('outreach')}>Open Settings</a>
      {/if}
    </p>
  {/if}
  <p class="text-muted" role="status">{status}</p>
  <p class="text-error" role="alert">{errorText}</p>
</div>

{#if plan}
  <OutreachScheduleAllDialog {plan} inFlight={confirming} errorMessage={dialogError} onConfirm={confirm} onClose={close} />
{/if}

<style>
  .waves {
    border: 0;
    padding: 0;
    margin: var(--space-2) 0;
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
  }
  [role='status']:empty,
  [role='alert']:empty {
    display: none;
  }
</style>
