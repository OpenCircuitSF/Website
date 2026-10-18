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
  import {
    clickDefinitionNote,
    foldedChannelsNote,
    formatClicks,
    formatReactionTotal,
    mastodonCountsNote,
    postsCellText,
    showFlyerAllColumn,
    reactionsLabel,
    resultsStatusMessage,
    dayLabel,
  } from '../../lib/outreachResults';
  import type { OutreachResults as Results, OutreachResultsSummary } from '../../lib/types';
  import Panel from '../../lib/Panel.svelte';
  import OutreachClicksChart from './OutreachClicksChart.svelte';
  import OutreachMentions from './OutreachMentions.svelte';

  let { workshopId = null }: { workshopId?: number | null } = $props();

  let results = $state<Results | null>(null);
  let summary = $state<OutreachResultsSummary | null>(null);
  let loading = $state(true);
  let loadError = $state<string | null>(null);

  const status = $derived(workshopId === null ? (summary?.status ?? 'ok') : (results?.status ?? 'ok'));
  const statusMessage = $derived(resultsStatusMessage(status));
  const folded = $derived(results ? foldedChannelsNote(results) : null);
  const flyerAllMessage = $derived(
    results && results.flyer_all_status && results.flyer_all_status !== 'ok' ? 'Scans across all workshops are unavailable right now.' : null,
  );
  const channelLabel = $derived(new Map((results?.channels ?? []).map((c) => [c.name, c.label])));
  const clickNote = $derived(results ? clickDefinitionNote(results) : null);
  const showAll = $derived(results ? showFlyerAllColumn(results.flyer_locations) : false);
  // #0588 will let a channel be switched off; until then every channel counts.
  const channelEnabled = (_name: string): boolean => true;
  const mastodonNote = $derived(results ? mastodonCountsNote(results) : null);
  // The summary response carries no channel list; labels are fixed server-side.
  const summaryLabels: Record<string, string> = { bluesky: 'Bluesky', mastodon: 'Mastodon', instagram: 'Instagram', tiktok: 'TikTok', youtube: 'YouTube' };

  // #0552: a response for a workshop no longer selected is discarded.
  let loadSeq = 0;

  async function load(): Promise<void> {
    const seq = ++loadSeq;
    loading = true;
    loadError = null;
    results = null;
    summary = null;
    try {
      if (workshopId === null) {
        const got = await listOutreachResults();
        if (seq !== loadSeq) return;
        summary = got;
      } else {
        const got = await getWorkshopOutreachResults(workshopId);
        if (seq !== loadSeq) return;
        results = got;
      }
    } catch (err) {
      if (seq !== loadSeq) return;
      loadError = err instanceof ApiError ? err.message : 'Could not load results. Please try again.';
    }
    loading = false;
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

<div class="fit">
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
              <th>Reactions</th>
            </tr>
          </thead>
          <tbody>
            {#each summary.rows as row (row.workshop_id)}
              <tr>
                <td><a href={adminOutreachResultsHash(row.workshop_id)}>{row.workshop_title}</a></td>
                <td>{row.starts_at ? formatDateTime(row.starts_at) : 'Date TBA'}</td>
                <td>{postsCellText(row, summaryLabels, channelEnabled)}</td>
                <td>{formatClicks(row.clicks)}</td>
                <td>{formatReactionTotal(row.reactions)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
    {#if summary}
      <OutreachMentions mentions={summary.mentions} channelLabel={(n) => summaryLabels[n] ?? n} showWorkshop />
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
    {#if results.reactions}
      <p>
        <strong>Reactions:</strong>
        {reactionsLabel(results.reactions)}
      </p>
    {/if}
    <OutreachClicksChart {results} windowText={results.window ? windowText(results) : 'chart window'} />
    {#if folded}
      <p class="text-muted">{folded}</p>
    {/if}
    {#if results.totals && results.totals.unmatched_clicks > 0}
      <p class="text-muted">
        {formatClicks(results.totals.unmatched_clicks)} on other links in this campaign.
      </p>
    {/if}
    {#if results.channels.length > 0}
      <div class="table-scroll">
        <table>
          <caption class="sr-only">Totals by channel</caption>
          <thead>
            <tr>
              <th>Channel</th>
              <th>Clicks (all links in the window)</th>
              <th>Reactions</th>
            </tr>
          </thead>
          <tbody>
            {#each results.channels as ch (ch.name)}
              <tr>
                <td>{ch.label}</td>
                <td>{formatClicks(ch.clicks)}</td>
                <td>{ch.reactions ? reactionsLabel(ch.reactions) : '—'}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
    {#if clickNote}
      <p class="text-muted">{clickNote}</p>
    {/if}
    {#if results.posts.length > 0}
      <details class="links">
        <summary>Every link ({results.posts.length})</summary>
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
              <th>Reactions</th>
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
                <td>{post.engagement ? reactionsLabel(post.engagement) : '—'}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      </details>
    {/if}
    {#if mastodonNote}
      <p class="text-muted">{mastodonNote}</p>
    {/if}
    {#if results.flyer_locations.length > 0}
      <h3 class="flyers-title">Flyers by location</h3>
      {#if flyerAllMessage && showAll}
        <p class="text-muted">{flyerAllMessage}</p>
      {/if}
      <div class="table-scroll">
        <table>
          <thead>
            <tr>
              <th>Location</th>
              <th>Put up</th>
              <th>{showAll ? 'Scans this workshop' : 'Scans'}</th>
              {#if showAll}<th>Scans all workshops</th>{/if}
            </tr>
          </thead>
          <tbody>
            {#each results.flyer_locations as row (row.link_id)}
              <tr>
                <td>{row.name} <span class="mono">{row.code}</span></td>
                <td>{row.put_up_at ? formatDateTime(row.put_up_at) : 'Not marked'}</td>
                <td>{formatClicks(row.clicks)}</td>
                {#if showAll}<td>{formatClicks(row.clicks_all_workshops)}</td>{/if}
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
    <OutreachMentions mentions={results.mentions} channelLabel={(n) => channelLabel.get(n) ?? n} />
    <p><a href={adminOutreachResultsHash()}>All workshops</a> · <a href={adminOutreachHash('workshops')}>Back to Outreach</a></p>
  {/if}
</Panel>
</div>

<style>
  .fit {
    max-width: 100%;
    min-width: 0;
    overflow-x: hidden;
  }
  .fit :global(.table-scroll) {
    max-width: 100%;
  }
  .links {
    margin: var(--space-3) 0;
  }
  .links summary {
    cursor: pointer;
    font-weight: 600;
  }
  .mono {
    overflow-wrap: anywhere;
  }
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
  .flyers-title {
    font-size: var(--fs-lg);
    margin: var(--space-3) 0 var(--space-2);
  }
</style>
