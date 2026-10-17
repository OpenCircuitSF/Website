<!--
  The Outreach tab's Results sub-view (#0546). With a workshop id: that
  workshop's click results (chart, posts table). Without one: every workshop
  with outreach, one row each. Our own database always answers, so posts and
  rows render even when ShortLinks cannot be reached; the status line says
  so. All wording and shaping is lib/outreachResults.ts.
-->
<script lang="ts">
  import { untrack } from 'svelte';
  import { getWorkshopOutreachResults, listOutreachResults, ApiError } from '../../lib/api';
  import { adminOutreachResultsHash, adminOutreachHash } from '../../lib/adminRouter';
  import { formatDateTime } from '../../lib/admin';
  import { foldedChannelsNote, formatClicks, resultsStatusMessage, dayLabel } from '../../lib/outreachResults';
  import type { OutreachResults as Results, OutreachResultsSummary } from '../../lib/types';
  import Panel from '../../lib/Panel.svelte';
  import OutreachClicksChart from './OutreachClicksChart.svelte';

  let { workshopId = null }: { workshopId?: number | null } = $props();

  let results = $state<Results | null>(null);
  let summary = $state<OutreachResultsSummary | null>(null);
  let loading = $state(true);
  let loadError = $state<string | null>(null);

  const status = $derived(workshopId === null ? (summary?.status ?? 'ok') : (results?.status ?? 'ok'));
  const statusMessage = $derived(resultsStatusMessage(status));
  const folded = $derived(results ? foldedChannelsNote(results) : null);
  const channelLabel = $derived(new Map((results?.channels ?? []).map((c) => [c.name, c.label])));

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    results = null;
    summary = null;
    try {
      if (workshopId === null) summary = await listOutreachResults();
      else results = await getWorkshopOutreachResults(workshopId);
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : 'Could not load results. Please try again.';
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    void workshopId;
    untrack(() => void load());
  });

  function windowText(r: Results): string {
    if (!r.window) return '';
    const to = new Date(Date.parse(`${r.window.to}T00:00:00Z`) - 86_400_000).toISOString().slice(0, 10);
    return `${dayLabel(r.window.from)} to ${dayLabel(to)} (UTC)`;
  }
</script>

<Panel title={workshopId === null ? 'Results' : results ? `Results: ${results.workshop.title}` : 'Results'}>
  <!-- Always rendered, so a late status message is announced. -->
  <p class="status" role="status">{loading ? 'Loading results…' : (statusMessage ?? '')}</p>
  {#if loadError}
    <p class="text-error" role="alert">{loadError}</p>
  {:else if workshopId === null}
    {#if summary && summary.rows.length === 0}
      <p class="text-muted">No workshop has outreach yet.</p>
    {:else if summary}
      <div class="table-scroll">
        <table>
          <thead>
            <tr>
              <th>Workshop</th>
              <th>Date</th>
              <th>Posts</th>
              <th>Clicks</th>
            </tr>
          </thead>
          <tbody>
            {#each summary.rows as row (row.workshop_id)}
              <tr>
                <td><a href={adminOutreachResultsHash(row.workshop_id)}>{row.workshop_title}</a></td>
                <td>{row.starts_at ? formatDateTime(row.starts_at) : 'Date TBA'}</td>
                <td>{row.posts_posted} of {row.posts_total} posted</td>
                <td>{formatClicks(row.clicks)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {:else if results}
    {#if results.totals}
      <p>
        <strong>{formatClicks(results.totals.clicks)}</strong>
        {#if results.window}in {windowText(results)}{/if}
        <span class="text-muted">
          ({results.totals.excluded_bot_clicks} bot clicks excluded{results.fetched_at ? `, as of ${formatDateTime(results.fetched_at)}` : ''})
        </span>
      </p>
    {/if}
    <OutreachClicksChart {results} />
    {#if folded}
      <p class="text-muted">{folded}</p>
    {/if}
    {#if results.totals && results.totals.unmatched_clicks > 0}
      <p class="text-muted">
        {results.totals.unmatched_clicks} clicks on other links in this campaign, e.g. flyers.
      </p>
    {/if}
    {#if results.posts.length > 0}
      <div class="table-scroll">
        <table>
          <thead>
            <tr>
              <th>Channel</th>
              <th>Wave</th>
              <th>Status</th>
              <th>Posted</th>
              <th>Short link</th>
              <th>Clicks</th>
            </tr>
          </thead>
          <tbody>
            {#each results.posts as post (post.id)}
              <tr>
                <td>{channelLabel.get(post.channel) ?? post.channel}</td>
                <td>{post.wave}</td>
                <td>{post.status}</td>
                <td>{post.posted_at ? formatDateTime(post.posted_at) : '—'}</td>
                <td>{#if post.short_url}<span class="mono">{post.short_url}</span>{:else}—{/if}</td>
                <td>{formatClicks(post.clicks)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
    <p><a href={adminOutreachResultsHash()}>All workshops</a> · <a href={adminOutreachHash('workshops')}>Back to Outreach</a></p>
  {/if}
</Panel>

<style>
  .status {
    margin: 0 0 var(--space-2);
    color: var(--text-muted);
  }
  .status:empty {
    display: none;
  }
  .mono {
    font-family: var(--font-mono);
  }
</style>
