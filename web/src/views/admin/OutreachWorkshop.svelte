<!--
  One workshop's outreach page (#0568, #0582): header, then a summary line, the
  Announcement (one message, how it will look, one post button), Reminder and
  Last call as collapsed rows, Schedule all, then "Post by hand", Flyers and
  "Links and clicks" as collapsed rows with a status and one action each. The
  OutreachPanel stays self-loading (and starts outreach implicitly) and hands
  its view and results to the body snippet. The tracked-links table lives on
  Results, not here.
-->
<script lang="ts">
  import { adminOutreachFlyersHash, adminOutreachHash, adminOutreachResultsHash, adminWorkshopHash } from '../../lib/adminRouter';
  import { formatDateTime } from '../../lib/admin';
  import type { FlyerRow, WorkshopOutreach } from '../../lib/types';
  import Panel from '../../lib/Panel.svelte';
  import OutreachPanel from './OutreachPanel.svelte';
  import OutreachAnnouncement from './OutreachAnnouncement.svelte';
  import OutreachWaveRow from './OutreachWaveRow.svelte';
  import OutreachBulkActions from './OutreachBulkActions.svelte';
  import OutreachPostCards from './OutreachPostCards.svelte';
  import OutreachResetDialog from './OutreachResetDialog.svelte';
  import Button from '../../lib/Button.svelte';
  import { onMount } from 'svelte';
  import { resetOutreach, listOutreachFlyers, ApiError } from '../../lib/api';
  import { flyerLinkFor, sendsItself, summaryLine } from '../../lib/outreachCards';
  import { manualRowStatus } from '../../lib/outreachMessage';
  import { linkReportMessage } from '../../lib/outreachLinks';
  import { resultsSummaryLine } from '../../lib/outreachResults';

  let { workshopId }: { workshopId: number } = $props();

  let view = $state<WorkshopOutreach | null>(null);
  let reloadKey = $state(0);
  // Set from Start's response, the only one that carries a link report.
  let linkWarning = $state<string | null>(null);

  // This workshop's flyers, so the Instagram card can open the right one (#0588).
  let flyers = $state<FlyerRow[]>([]);
  const flyer = $derived(flyerLinkFor(flyers, workshopId));
  onMount(async () => {
    try {
      flyers = (await listOutreachFlyers()).flyers;
    } catch {
      // The link then falls back to "Make a flyer" for this workshop.
    }
  });

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
      resetNote = 'Outreach was reset. Write the announcement to start again.';
      reloadKey++;
    } catch (err) {
      resetError = err instanceof ApiError ? err.message : 'Could not reset outreach. Please try again.';
    } finally {
      resetBusy = false;
    }
  }

  function onLoaded(v: WorkshopOutreach): void {
    view = v;
    if (v.link_report) linkWarning = linkReportMessage(v.link_report);
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
  <OutreachPanel {workshopId} {reloadKey} {onLoaded} autoStart>
    {#snippet body({ view: v, results, onChange })}
      <p class="summary">{summaryLine(v, results)}</p>
      <OutreachAnnouncement view={v} {results} wave="announce" {workshopId} {onChange} reload={() => reloadKey++} />
      {#each v.waves.filter((w) => w.name !== 'announce') as w (w.name)}
        <OutreachWaveRow view={v} {results} wave={w} {workshopId} {onChange} reload={() => reloadKey++} />
      {/each}
      <OutreachBulkActions {workshopId} view={v} onDone={() => reloadKey++} />
      <details class="row-details">
        <summary><strong>Post by hand</strong> <span class="text-muted">· {manualRowStatus(v)}</span></summary>
        <OutreachPostCards view={v} {results} {onChange} reload={() => reloadKey++} channelFilter={(c) => !sendsItself(c)} {flyer} />
      </details>
      <div class="row-details row spread">
        <span><strong>Flyers</strong> <span class="text-muted">· print and post at saved locations</span></span>
        <a href={adminOutreachFlyersHash()}>Open flyers</a>
      </div>
      <div class="row-details">
        <p class="row spread">
          <span>
            <strong>Links and clicks</strong>
            <span class="text-muted">· {resultsSummaryLine(results) || 'No clicks yet'}</span>
          </span>
          <a href={adminOutreachResultsHash(workshopId)}>View results</a>
        </p>
        {#if linkWarning}<p class="text-warn">{linkWarning}</p>{/if}
      </div>
      {#if resetNote}<p class="text-muted">{resetNote}</p>{/if}
      {#if v.can_reset}
        <p>
          <Button onclick={() => { resetError = null; resetOpen = true; }}>Reset outreach</Button>
        </p>
      {/if}
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
  .row-details {
    border-top: var(--border-w) solid var(--border);
    padding: var(--space-2) 0;
  }
  .links-row {
    gap: var(--space-3);
  }
</style>
