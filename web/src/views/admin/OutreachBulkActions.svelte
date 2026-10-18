<!--
  Schedule all on a workshop's outreach page (#0568). It previews first (a dry
  run), shows the server's own plan in a confirm dialog, and confirms with
  exactly the previewed ids. Drafting moved into the Announcement section and
  the wave rows' Write it (#0582). Wording and decisions are calls into
  lib/outreachLinks.ts.
-->
<script lang="ts">
  import { scheduleAllOutreach, ApiError } from '../../lib/api';
  import { adminSettingsHash, adminWorkshopHash } from '../../lib/adminRouter';
  import { scheduleAllMessage, canScheduleAll } from '../../lib/outreachLinks';
  import type { OutreachScheduleAllResult, WorkshopOutreach } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import OutreachScheduleAllDialog from './OutreachScheduleAllDialog.svelte';

  interface Props {
    workshopId: number;
    view: WorkshopOutreach;
    onDone: () => void;
  }
  let { workshopId, view, onDone }: Props = $props();

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

  async function confirm(postIds: number[], times: Record<number, string>): Promise<void> {
    if (!plan) return;
    confirming = true;
    dialogError = null;
    try {
      const r = await scheduleAllOutreach(workshopId, Object.keys(times).length > 0 ? { dry_run: false, post_ids: postIds, times } : { dry_run: false, post_ids: postIds });
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
    <Button onclick={preview} disabled={busy || !gate.ok}>Schedule all</Button>
  </div>
  <p class="text-muted">Schedule all shows a preview of the automatic posts it would schedule, then asks you to confirm.</p>
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
  [role='status']:empty,
  [role='alert']:empty {
    display: none;
  }
</style>
