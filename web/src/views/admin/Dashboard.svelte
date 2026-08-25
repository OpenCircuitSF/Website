<!--
  The admin overview dashboard (#0061): the /admin landing screen. One data
  call (GET /admin/overview) backs subscriber counts + 30-day growth,
  interest distribution, recent campaigns, any campaign currently sending
  (with live progress over the existing #0048 SSE stream), and the
  "what needs attention" warnings row — the part that earns this screen a
  place at all (issue notes: an operator should learn the physical address
  is missing HERE, not when a send is refused on announcement day).

  Markup and wiring only, per #0047/#0049's convention (see CampaignStats.svelte's
  own doc comment): every decision about the payload — growth sign/formatting,
  which warnings to show, the complaint-rate string — is a call into
  lib/dashboard.ts, never a comparison or ternary written directly here.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { getOverview, ApiError } from '../../lib/api';
  import { subscribeEvent } from '../../lib/events';
  import {
    CAMPAIGN_PROGRESS_EVENT,
    isProgressForCampaign,
    progressPercent,
    formatProgressDetail,
    type CampaignProgress,
  } from '../../lib/campaignProgress';
  import { campaignStatusLabel, campaignStatusBadgeClass } from '../../lib/campaigns';
  import { formatDateTime, subscriberStatusLabel, subscriberStatusBadgeClass, SUBSCRIBER_STATUSES } from '../../lib/admin';
  import { formatNetGrowth, formatGrowthDetail, buildWarnings, hasAlertWarning } from '../../lib/dashboard';
  import type { DashboardOverview, DashboardSubscriberCounts } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import Panel from '../../lib/Panel.svelte';

  interface Props {
    /** #0061 reuses Admin.svelte's existing Campaigns deep-link mechanism (goToAnnouncedCampaign's own pattern) so clicking a campaign here opens it in the Campaigns tab's editor rather than duplicating that UI. */
    onOpenCampaign: (campaignId: number) => void;
  }

  let { onOpenCampaign }: Props = $props();

  let overview = $state<DashboardOverview | null>(null);
  let loading = $state(true);
  let loadError = $state<string | null>(null);

  let warnings = $derived(overview ? buildWarnings(overview.warnings) : []);
  let anyAlert = $derived(hasAlertWarning(warnings));

  // Live progress for the currently-sending campaign, if any. Reset whenever
  // a fresh load() reports a different (or no) sending campaign — see the
  // $effect below — so a stale percentage from a PREVIOUS sending campaign
  // never lingers after that campaign finishes and another starts.
  let progress = $state<CampaignProgress | null>(null);
  let sendingCampaignId = $derived(overview?.sending_campaign?.id ?? null);

  $effect(() => {
    // Re-reading sendingCampaignId makes this effect re-run whenever it
    // changes; its only job is to drop a progress snapshot that belonged to
    // a campaign that is no longer the one this screen is tracking.
    if (progress && !isProgressForCampaign(progress, sendingCampaignId ?? -1)) {
      progress = null;
    }
  });

  function onProgressEvent(p: CampaignProgress): void {
    if (sendingCampaignId !== null && isProgressForCampaign(p, sendingCampaignId)) {
      progress = p;
    }
  }

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      overview = await getOverview();
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : 'Could not load the dashboard.';
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load();
    // The database, not this stream, is the source of truth (CLAUDE.md) —
    // re-fetching the whole overview on every (re)connect, including the
    // first, is what lets this screen notice a send that started, finished,
    // or was canceled entirely during a gap with no frame to report it,
    // mirroring CampaignEditor.svelte's identical reasoning for its own
    // resync-on-open.
    const unsubscribe = subscribeEvent<CampaignProgress>(
      CAMPAIGN_PROGRESS_EVENT,
      onProgressEvent,
      undefined,
      () => void load(),
    );
    return () => {
      unsubscribe();
    };
  });

  const STATUS_ORDER: (keyof DashboardSubscriberCounts)[] = ['active', 'pending', 'unsubscribed', 'bounced', 'complained'];
</script>

<Panel title="Overview">
  {#if loading}
    <p class="text-muted" role="status">Loading overview…</p>
  {:else if loadError}
    <p class="text-error" role="alert">{loadError}</p>
    <Button variant="primary" onclick={load}>Retry</Button>
  {:else if overview}
    {#if warnings.length > 0}
      <div class="warnings" class:has-alert={anyAlert}>
        <h3 class="warnings-title">Needs attention</h3>
        <ul class="warnings-list">
          {#each warnings as w (w.key)}
            <li class:alert={w.alert} role={w.alert ? 'alert' : 'status'}>{w.message}</li>
          {/each}
        </ul>
      </div>
    {:else}
      <p class="text-notice" role="status">Nothing needs attention right now.</p>
    {/if}

    <div class="overview-grid">
      <div class="stat-block">
        <div class="stat-block-title">Subscribers</div>
        <div class="status-counts">
          {#each STATUS_ORDER as st (st)}
            <div class="status-count">
              <span class="badge {subscriberStatusBadgeClass(st)}">{subscriberStatusLabel(st)}</span>
              <span class="status-count-num">{overview.subscribers.counts[st].toLocaleString()}</span>
            </div>
          {/each}
        </div>
        <p class="growth-line">
          <span class="growth-net">{formatNetGrowth(overview.subscribers.growth_30d)}</span>
          <span class="text-muted">over the last 30 days ({formatGrowthDetail(overview.subscribers.growth_30d)})</span>
        </p>
      </div>

      <div class="stat-block">
        <div class="stat-block-title">Interests</div>
        {#if overview.interests.length === 0}
          <p class="text-muted">No interests defined yet.</p>
        {:else}
          <ul class="interest-list">
            {#each overview.interests as it (it.id)}
              <li>
                <span>{it.name}</span>
                <span class="text-muted">{it.subscriber_count.toLocaleString()}</span>
              </li>
            {/each}
          </ul>
        {/if}
      </div>
    </div>

    {#if overview.sending_campaign}
      {@const sc = overview.sending_campaign}
      <div class="sending-block">
        <div class="stat-block-title">Currently sending</div>
        <button type="button" class="campaign-link" onclick={() => onOpenCampaign(sc.id)}>
          {sc.name}
        </button>
        {#if progress}
          <div class="progress-bar" role="progressbar" aria-valuenow={progressPercent(progress)} aria-valuemin={0} aria-valuemax={100}>
            <div class="progress-fill" style={`width: ${progressPercent(progress)}%`}></div>
          </div>
          <p class="text-muted">{formatProgressDetail(progress)}</p>
        {:else}
          <p class="text-muted">Waiting for the first progress update…</p>
        {/if}
      </div>
    {/if}

    {#if overview.outbound_queue}
      {@const oq = overview.outbound_queue}
      <div class="stat-block-title">Outbound queue</div>
      <ul class="warnings-list">
        <li>{oq.queued.toLocaleString()} queued</li>
        <li>{oq.sending.toLocaleString()} sending</li>
        <li>{oq.sent.toLocaleString()} sent</li>
        <li>{oq.abandoned.toLocaleString()} abandoned</li>
        <li>{oq.abandoned_confirmations.toLocaleString()} abandoned confirmations</li>
      </ul>
    {/if}

    <div class="stat-block-title">Recent campaigns</div>
    {#if overview.recent_campaigns.length === 0}
      <p class="text-muted">No campaigns yet.</p>
    {:else}
      <div class="table-scroll">
        <table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Status</th>
              <th>Last updated</th>
            </tr>
          </thead>
          <tbody>
            {#each overview.recent_campaigns as c (c.id)}
              <tr>
                <td>
                  <button type="button" class="campaign-link" onclick={() => onOpenCampaign(c.id)}>{c.name}</button>
                </td>
                <td><span class="badge {campaignStatusBadgeClass(c.status)}">{campaignStatusLabel(c.status)}</span></td>
                <td>
                  {#if c.completed_at}
                    {formatDateTime(c.completed_at)}
                  {:else if c.started_at}
                    {formatDateTime(c.started_at)}
                  {:else if c.scheduled_at}
                    {formatDateTime(c.scheduled_at)}
                  {:else}
                    <span class="text-muted">—</span>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {/if}
</Panel>

<style>
  .warnings {
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
    padding: var(--space-3);
    margin-bottom: var(--space-4);
  }
  .warnings.has-alert {
    border-color: var(--danger);
    background: var(--danger-subtle);
  }
  .warnings-title {
    margin: 0 0 var(--space-2);
    font-size: var(--fs-base);
  }
  .warnings-list {
    margin: 0;
    padding-left: 1.25em;
  }
  .warnings-list li {
    margin-bottom: var(--space-1);
  }
  .warnings-list li.alert {
    color: var(--danger);
    font-weight: 600;
  }

  .overview-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
    gap: var(--space-4);
    margin-bottom: var(--space-4);
  }
  .stat-block-title {
    font-weight: 600;
    margin-bottom: var(--space-2);
  }
  .status-counts {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
    margin-bottom: var(--space-2);
  }
  .status-count {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 2px;
  }
  .status-count-num {
    font-size: var(--fs-lg);
    font-weight: 700;
  }
  .growth-line {
    margin: 0;
  }
  .growth-net {
    font-weight: 700;
  }
  .interest-list {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .interest-list li {
    display: flex;
    justify-content: space-between;
    padding: 2px 0;
  }

  .sending-block {
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
    padding: var(--space-3);
    margin-bottom: var(--space-4);
  }
  .progress-bar {
    height: 8px;
    border-radius: 4px;
    background: var(--bg-subtle);
    overflow: hidden;
    margin: var(--space-2) 0;
  }
  .progress-fill {
    height: 100%;
    background: var(--accent);
  }

  .campaign-link {
    background: none;
    border: none;
    padding: 0;
    color: var(--accent);
    text-decoration: underline;
    cursor: pointer;
    font: inherit;
  }
</style>
