<!--
  The top-level Outreach admin tab (#0544): every workshop that has outreach,
  with its post counts per status. Results (clicks per channel and wave) are a
  later issue. Loads on its own onMount like the other subtabs.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { listOutreach, ApiError } from '../../lib/api';
  import { adminWorkshopHash } from '../../lib/adminRouter';
  import { formatDateTime } from '../../lib/admin';
  import type { OutreachSummary } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import Panel from '../../lib/Panel.svelte';

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

  onMount(load);
</script>

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
