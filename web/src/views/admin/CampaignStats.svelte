<!--
  The campaign stats screen (#0049): per-campaign send outcome — counts by
  status, bounce/complaint counts reconciled from email_events, the 0.3%
  complaint-rate warning, and the failed-sends list with error messages.

  Markup and wiring only, per #0047's convention this issue's own carried-in
  review restates: every decision (bucket ordering, the reconciliation
  substitution, the threshold verdict, percentage formatting, the
  no-error-message fallback) is a call into lib/campaignStats.ts, or a value
  taken straight off the server response — never a comparison, ternary, or
  status literal written directly here.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { getCampaignStats, ApiError } from '../../lib/api';
  import { campaignStatusLabel, campaignStatusBadgeClass } from '../../lib/campaigns';
  import {
    buildStatBuckets,
    totalSendRows,
    campaignComplaintRateVerdict,
    failedSendErrorLabel,
  } from '../../lib/campaignStats';
  import type { CampaignStatsResponse } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import Panel from '../../lib/Panel.svelte';

  interface Props {
    campaignId: number;
    onBack: () => void;
  }

  let { campaignId, onBack }: Props = $props();

  let stats = $state<CampaignStatsResponse | null>(null);
  let loading = $state(true);
  let loadError = $state<string | null>(null);

  let buckets = $derived(stats ? buildStatBuckets(stats) : []);
  let total = $derived(stats ? totalSendRows(stats) : 0);
  let complaintRate = $derived(stats ? campaignComplaintRateVerdict(stats) : null);
  let statusLabel = $derived(stats ? campaignStatusLabel(stats.status) : '');
  let statusBadgeClass = $derived(stats ? campaignStatusBadgeClass(stats.status) : '');
  let failedSends = $derived(stats?.failed_sends ?? []);
  let hasFailedSends = $derived(failedSends.length > 0);

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      stats = await getCampaignStats(campaignId);
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : 'Could not load campaign stats.';
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load();
  });
</script>

<div class="row spread" style="margin-bottom: var(--space-3);">
  <Button onclick={onBack}>&larr; Back to campaigns</Button>
</div>

<Panel title="Campaign stats">
  {#if loading}
    <p class="text-muted" role="status">Loading stats…</p>
  {:else if loadError}
    <p class="text-error" role="alert">{loadError}</p>
    <Button variant="primary" onclick={load}>Retry</Button>
  {:else if stats}
    <div class="row" style="margin-bottom: var(--space-4);">
      <span class="badge {statusBadgeClass}">{statusLabel}</span>
      <span class="text-muted">{total.toLocaleString()} total recipients</span>
    </div>

    {#if complaintRate}
      <div class="complaint-rate" class:over-threshold={complaintRate.overThreshold}>
        <p class="complaint-rate-line">
          Complaint rate: <strong>{complaintRate.formatted}</strong>
        </p>
        {#if complaintRate.overThreshold}
          <p class="text-error" role="alert">
            Above Gmail's published 0.3% limit — this sending domain risks throttling or blocking.
          </p>
        {:else}
          <p class="text-muted">Within Gmail's published 0.3% limit.</p>
        {/if}
      </div>
    {/if}

    <div class="bucket-grid">
      {#each buckets as bucket (bucket.key)}
        <div class="bucket bucket-{bucket.tone}">
          <div class="bucket-count">{bucket.count.toLocaleString()}</div>
          <div class="bucket-label">{bucket.label}</div>
        </div>
      {/each}
    </div>
  {/if}
</Panel>

{#if stats && hasFailedSends}
  <Panel title="Failed sends" noPadding>
    <div class="table-scroll">
      <table>
        <thead>
          <tr>
            <th>Email</th>
            <th>Attempts</th>
            <th>Error</th>
          </tr>
        </thead>
        <tbody>
          {#each failedSends as f (f.id)}
            <tr>
              <td>{f.email}</td>
              <td>{f.attempts}</td>
              <td>{failedSendErrorLabel(f)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  </Panel>
{/if}

<style>
  .bucket-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(110px, 1fr));
    gap: var(--space-3);
  }
  .bucket {
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
    padding: var(--space-3);
    text-align: center;
  }
  .bucket-count {
    font-size: var(--fs-xl);
    font-weight: 700;
  }
  .bucket-label {
    color: var(--text-muted);
    font-size: var(--fs-sm);
    margin-top: var(--space-1);
  }
  .bucket-success .bucket-count {
    color: var(--success);
  }
  .bucket-danger .bucket-count {
    color: var(--danger);
  }
  .complaint-rate {
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
    padding: var(--space-3);
    margin-bottom: var(--space-4);
  }
  .complaint-rate.over-threshold {
    border-color: var(--danger);
    background: var(--danger-subtle);
  }
  .complaint-rate-line {
    margin: 0 0 var(--space-1);
  }
</style>
