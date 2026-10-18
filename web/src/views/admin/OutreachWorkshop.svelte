<!--
  One workshop's outreach page (#0568): header, then (#0569) a summary line, the bulk
  actions, the posts list, the collapsed tracked-links table and the results
  link. The OutreachPanel stays self-loading and hands its view and results
  to the body snippet.
-->
<script lang="ts">
  import { adminOutreachHash, adminOutreachResultsHash, adminWorkshopHash } from '../../lib/adminRouter';
  import { formatDateTime } from '../../lib/admin';
  import type { WorkshopOutreach } from '../../lib/types';
  import Panel from '../../lib/Panel.svelte';
  import OutreachPanel from './OutreachPanel.svelte';
  import OutreachLinks from './OutreachLinks.svelte';
  import OutreachBulkActions from './OutreachBulkActions.svelte';
  import OutreachPostCards from './OutreachPostCards.svelte';
  import OutreachResetDialog from './OutreachResetDialog.svelte';
  import Button from '../../lib/Button.svelte';
  import { resetOutreach, ApiError } from '../../lib/api';
  import { summaryLine } from '../../lib/outreachCards';
  import { resultsSummaryLine } from '../../lib/outreachResults';

  let { workshopId }: { workshopId: number } = $props();

  let view = $state<WorkshopOutreach | null>(null);
  let reloadKey = $state(0);
  let linkCount = $state(0);

  let resetOpen = $state(false);
  let resetBusy = $state(false);
  let resetError = $state<string | null>(null);
  let resetNote = $state('');

  async function doReset(): Promise<void> {
    resetBusy = true;
    resetError = null;
    try {
      await resetOutreach(workshopId);
      resetOpen = false;
      resetNote = 'Outreach was reset. Draft all to start again.';
      reloadKey++;
    } catch (err) {
      resetError = err instanceof ApiError ? err.message : 'Could not reset outreach. Please try again.';
    } finally {
      resetBusy = false;
    }
  }

  function onLoaded(v: WorkshopOutreach): void {
    view = v;
  }
</script>

<div class="row spread heading-row">
  {#if view}
    <h2 class="heading">{view.workshop.title}</h2>
  {:else}
    <h2 class="heading">Loading…</h2>
  {/if}
  <span class="row links-row">
    <a href={adminWorkshopHash(workshopId)}>Edit workshop</a>
    <a href={adminOutreachHash('workshops')}>All outreach</a>
  </span>
</div>
{#if view}
  <p class="text-muted">
    {view.workshop.starts_at ? formatDateTime(view.workshop.starts_at) : 'Date TBA'} · {view.workshop.status}
  </p>
{/if}

<Panel title="Outreach">
  <OutreachPanel {workshopId} {reloadKey} {onLoaded}>
    {#snippet body({ view: v, results, onChange })}
      <p class="summary">{summaryLine(v, results)}</p>
      <OutreachBulkActions {workshopId} view={v} onDone={() => reloadKey++} />
      <OutreachPostCards view={v} {results} {onChange} reload={() => reloadKey++} />
      <details class="links-details">
        <summary>Tracked links ({linkCount})</summary>
        <OutreachLinks {workshopId} available={v.available} {reloadKey} onCount={(n) => (linkCount = n)} />
      </details>
      {#if resetNote}<p class="text-muted">{resetNote}</p>{/if}
      {#if v.can_reset}
        <p>
          <Button onclick={() => { resetError = null; resetOpen = true; }}>Reset outreach</Button>
        </p>
      {/if}
      <p>
        {#if resultsSummaryLine(results)}{resultsSummaryLine(results)}{' '}{/if}<a href={adminOutreachResultsHash(workshopId)}>View results</a>
      </p>
    {/snippet}
  </OutreachPanel>
</Panel>

{#if resetOpen}
  <OutreachResetDialog inFlight={resetBusy} errorMessage={resetError} onConfirm={doReset} onClose={() => (resetOpen = false)} />
{/if}

<style>
  .heading-row {
    margin-bottom: var(--space-2);
  }
  .heading {
    font-size: var(--fs-lg);
    margin: 0;
    overflow-wrap: anywhere;
  }
  .summary {
    font-weight: 600;
  }
  .links-details {
    margin: var(--space-4) 0;
  }
  .links-row {
    gap: var(--space-3);
  }
</style>
