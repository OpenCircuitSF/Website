<!--
  One workshop's outreach page (#0568): header, then the OutreachPanel (start
  button or campaign line, links table and bulk actions, the channel x wave
  grid with the composer, and the results summary). The panel stays
  self-loading; this page injects the links table and bulk actions between
  its campaign line and its grid.
-->
<script lang="ts">
  import { adminOutreachHash, adminWorkshopHash } from '../../lib/adminRouter';
  import { formatDateTime } from '../../lib/admin';
  import type { WorkshopOutreach } from '../../lib/types';
  import Panel from '../../lib/Panel.svelte';
  import OutreachPanel from './OutreachPanel.svelte';
  import OutreachLinks from './OutreachLinks.svelte';
  import OutreachBulkActions from './OutreachBulkActions.svelte';

  let { workshopId }: { workshopId: number } = $props();

  let view = $state<WorkshopOutreach | null>(null);
  let reloadKey = $state(0);

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
    {#snippet beforeGrid()}
      <OutreachLinks {workshopId} available={view?.available ?? false} {reloadKey} />
      {#if view}
        <OutreachBulkActions {workshopId} {view} onDone={() => reloadKey++} />
      {/if}
    {/snippet}
  </OutreachPanel>
</Panel>

<style>
  .heading-row {
    margin-bottom: var(--space-2);
  }
  .heading {
    font-size: var(--fs-lg);
    margin: 0;
    overflow-wrap: anywhere;
  }
  .links-row {
    gap: var(--space-3);
  }
</style>
