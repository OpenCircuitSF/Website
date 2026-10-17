<!--
  The top-level Outreach admin tab (#0544): every workshop that has outreach,
  with its post counts per status. Loads when the Workshops sub-view shows; the
  Locations sub-view (#0548) is OutreachLocations.svelte and the Results
  sub-view (#0546) is OutreachResults.svelte.
-->
<script lang="ts">
  import { untrack } from 'svelte';
  import { listOutreach, ApiError } from '../../lib/api';
  import {
    adminWorkshopHash,
    adminOutreachHash,
    adminOutreachResultsHash,
    outreachViewFromQuery,
    outreachWorkshopFromQuery,
  } from '../../lib/adminRouter';
  import { formatDateTime } from '../../lib/admin';
  import type { OutreachSummary } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import Panel from '../../lib/Panel.svelte';
  import OutreachLocations from './OutreachLocations.svelte';
  import OutreachResults from './OutreachResults.svelte';

  // #0548: the sub-view lives in the hash query (#/outreach?view=locations).
  let { query = new URLSearchParams() }: { query?: URLSearchParams } = $props();
  const view = $derived(outreachViewFromQuery(query));

  let rows = $state<OutreachSummary[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      rows = (await listOutreach()).outreach;
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : 'Could not load outreach. Please try again.';
    } finally {
      loading = false;
    }
  }

  function countsLabel(counts: Record<string, number>): string {
    const parts = Object.entries(counts)
      .filter(([, n]) => n > 0)
      .map(([status, n]) => `${n} ${status}`);
    return parts.length > 0 ? parts.join(', ') : 'No posts yet';
  }

  // The workshops list loads whenever it becomes the visible sub-view.
  $effect(() => {
    if (view === 'workshops') untrack(load);
  });
</script>

<nav class="outreach-subnav" aria-label="Outreach sections">
  <a href={adminOutreachHash('workshops')} aria-current={view === 'workshops' ? 'page' : undefined}>Workshops</a>
  <a href={adminOutreachHash('locations')} aria-current={view === 'locations' ? 'page' : undefined}>Locations</a>
  <a href={adminOutreachResultsHash()} aria-current={view === 'results' ? 'page' : undefined}>Results</a>
</nav>

{#if view === 'locations'}
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
  {:else if rows.length === 0}
    <p class="text-muted">No workshop has outreach yet.</p>
  {:else}
    <div class="table-scroll">
      <table>
        <thead>
          <tr>
            <th>Workshop</th>
            <th>Date</th>
            <th>Status</th>
            <th>Posts</th>
          </tr>
        </thead>
        <tbody>
          {#each rows as row (row.outreach_id)}
            <tr>
              <td><a href={adminWorkshopHash(row.workshop_id)}>{row.workshop_title}</a></td>
              <td>{row.starts_at ? formatDateTime(row.starts_at) : 'Date TBA'}</td>
              <td>{row.workshop_status}</td>
              <td>{countsLabel(row.counts)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</Panel>
{/if}

<style>
  .outreach-subnav {
    display: flex;
    gap: var(--space-3);
    margin-bottom: var(--space-3);
  }
  .outreach-subnav a[aria-current='page'] {
    font-weight: 700;
    text-decoration: underline;
  }
</style>
