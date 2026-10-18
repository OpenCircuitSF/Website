<!--
  The top-level Outreach admin tab (#0544, reshaped by #0586): every published
  upcoming workshop with a status chip and one Promote/Open button, whether or
  not outreach exists yet. Loads when the Workshops sub-view shows; the
  Locations sub-view (#0548) is OutreachLocations.svelte and the Results
  sub-view (#0546) is OutreachResults.svelte.
-->
<script lang="ts">
  import { untrack } from 'svelte';
  import { listOutreach, listAdminWorkshops, ApiError } from '../../lib/api';
  import {
    adminOutreachWorkshopHash,
    adminOutreachHash,
    adminOutreachResultsHash,
    adminOutreachFlyersHash,
    outreachViewFromQuery,
    outreachWorkshopFromQuery,
    outreachFlyerFromQuery,
    outreachLayoutFromQuery,
  } from '../../lib/adminRouter';
  import { formatDateTime } from '../../lib/admin';
  import { upcomingPublished, summariesByWorkshop, outreachChip, outreachRowButtonLabel } from '../../lib/outreachList';
  import type { OutreachSummary, AdminWorkshop } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import Panel from '../../lib/Panel.svelte';
  import OutreachLocations from './OutreachLocations.svelte';
  import OutreachResults from './OutreachResults.svelte';
  import OutreachWorkshop from './OutreachWorkshop.svelte';
  import OutreachFlyers from './OutreachFlyers.svelte';
  import OutreachFlyerEditor from './OutreachFlyerEditor.svelte';
  import FlyerPrint from './FlyerPrint.svelte';

  // #0548: the sub-view lives in the hash query (#/outreach?view=locations).
  let { query = new URLSearchParams() }: { query?: URLSearchParams } = $props();
  const view = $derived(outreachViewFromQuery(query));
  const flyerId = $derived(outreachFlyerFromQuery(query));

  let rows = $state<OutreachSummary[]>([]);
  let workshops = $state<AdminWorkshop[]>([]);
  const upcoming = $derived(upcomingPublished(workshops));
  const byWorkshop = $derived(summariesByWorkshop(rows));
  let loading = $state(true);
  let loadError = $state<string | null>(null);

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      const [o, w] = await Promise.all([listOutreach(), listAdminWorkshops()]);
      rows = o.outreach;
      workshops = w.workshops;
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : 'Could not load outreach. Please try again.';
    } finally {
      loading = false;
    }
  }

  // The workshops list loads whenever it becomes the visible sub-view.
  $effect(() => {
    if (view === 'workshops') untrack(load);
  });
</script>

<!-- The print view is chrome-less: it takes over the page and hides this nav. -->
{#if view === 'print' && flyerId !== null}
  <FlyerPrint {flyerId} layout={outreachLayoutFromQuery(query)} />
{:else}
<nav class="outreach-subnav" aria-label="Outreach sections">
  <a href={adminOutreachHash('workshops')} aria-current={view === 'workshops' || view === 'workshop' ? 'page' : undefined}>Posts</a>
  <a href={adminOutreachHash('locations')} aria-current={view === 'locations' ? 'page' : undefined}>Flyer spots</a>
  <a href={adminOutreachFlyersHash()} aria-current={view === 'flyers' || view === 'print' ? 'page' : undefined}>Flyers</a>
  <a href={adminOutreachResultsHash()} aria-current={view === 'results' ? 'page' : undefined}>Results</a>
</nav>

{#if view === 'flyers' && flyerId !== null}
  <OutreachFlyerEditor {flyerId} />
{:else if view === 'flyers' || view === 'print'}
  <OutreachFlyers />
{:else if view === 'workshop' && outreachWorkshopFromQuery(query) !== null}
  <OutreachWorkshop workshopId={outreachWorkshopFromQuery(query) ?? 0} />
{:else if view === 'locations'}
  <OutreachLocations />
{:else if view === 'results'}
  <OutreachResults workshopId={outreachWorkshopFromQuery(query)} />
{:else}
<Panel title="Outreach">
  {#if loading}
    <p class="text-muted">Loading outreach…</p>
  {:else if loadError}
    <p class="text-error">{loadError}</p>
    <Button variant="primary" onclick={load}>Retry</Button>
  {:else if upcoming.length === 0}
    <p class="text-muted">No upcoming published workshops. Publish a workshop with a future date under Workshops to promote it here.</p>
  {:else}
    <div class="table-scroll">
      <table>
        <thead>
          <tr>
            <th>Workshop</th>
            <th>Date</th>
            <th>Outreach</th>
            <th><span class="sr-only">Action</span></th>
          </tr>
        </thead>
        <tbody>
          {#each upcoming as w (w.id)}
            {@const s = byWorkshop.get(w.id)}
            {@const chip = outreachChip(s)}
            <tr>
              <td>{w.title}</td>
              <td>{w.starts_at ? formatDateTime(w.starts_at) : 'Date TBA'}</td>
              <td><span class="badge badge-{chip.tone}">{chip.label}</span></td>
              <td><a class="row-button" href={adminOutreachWorkshopHash(w.id)}>{outreachRowButtonLabel(s)}</a></td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</Panel>
{/if}
{/if}

<style>
  .outreach-subnav {
    display: flex;
    gap: var(--space-3);
    margin-bottom: var(--space-3);
  }
  .row-button {
    display: inline-block;
    padding: var(--space-1) var(--space-3);
    border: var(--border-w) solid var(--accent);
    border-radius: var(--radius);
    color: var(--accent);
    text-decoration: none;
  }
  .outreach-subnav a[aria-current='page'] {
    font-weight: 700;
    text-decoration: underline;
  }
</style>
