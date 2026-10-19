<!--
  The top-level Outreach admin tab (#0544, reshaped by #0586): every published
  upcoming workshop with a status chip and one Promote/Open button, whether or
  not outreach exists yet. Loads when the Workshops sub-view shows; the
  Locations sub-view (#0548) is OutreachLocations.svelte and the Results
  sub-view (#0546) is OutreachResults.svelte.
-->
<script lang="ts">
  import SubNav from '../../lib/SubNav.svelte';
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
  import { upcomingPublished, summariesByWorkshop, outreachChip, outreachRowButtonLabel, outreachRowAriaLabel, pastWithOutreach } from '../../lib/outreachList';
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
  const past = $derived(pastWithOutreach(workshops, rows));
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
<SubNav label="Outreach sections" items={[
  { href: adminOutreachHash('workshops'), label: 'Posts', current: view === 'workshops' || view === 'workshop' },
  { href: adminOutreachHash('locations'), label: 'Flyer spots', current: view === 'locations' },
  { href: adminOutreachFlyersHash(), label: 'Flyers', current: view === 'flyers' || view === 'print' },
  { href: adminOutreachResultsHash(), label: 'Results', current: view === 'results' }
]} />

{#if view === 'flyers' && flyerId !== null}
  <OutreachFlyerEditor {flyerId} />
{:else if view === 'flyers' || view === 'print'}
  <OutreachFlyers workshopId={outreachWorkshopFromQuery(query)} />
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
              <td><a class="row-button" href={adminOutreachWorkshopHash(w.id)} aria-label={outreachRowAriaLabel(s, w.title)}>{outreachRowButtonLabel(s)}</a></td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</Panel>
{#if !loading && !loadError && past.length > 0}
  <Panel title="Past">
    <p class="text-muted">Workshops that have happened and had outreach. Open one for its posts, or see how it did under Results.</p>
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
          {#each past as w (w.id)}
            {@const s = byWorkshop.get(w.id)}
            {@const chip = outreachChip(s)}
            <tr>
              <td>{w.title}</td>
              <td>{w.starts_at ? formatDateTime(w.starts_at) : 'Date TBA'}</td>
              <td><span class="badge badge-{chip.tone}">{chip.label}</span></td>
              <td>
                <a class="row-button" href={adminOutreachWorkshopHash(w.id)} aria-label={outreachRowAriaLabel(s, w.title)}>{outreachRowButtonLabel(s)}</a>
                <a href={adminOutreachResultsHash(w.id)} aria-label="Results for {w.title}">Results</a>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  </Panel>
{/if}
{/if}
{/if}

<style>
  .row-button {
    display: inline-block;
    padding: var(--space-1) var(--space-3);
    border: var(--border-w) solid var(--accent);
    border-radius: var(--radius);
    color: var(--accent);
    text-decoration: none;
  }
</style>
