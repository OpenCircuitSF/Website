<!--
  The Events page's "Next month" panel (#0678): next month's events checked
  against Settings → Event cadence, plus the Luma comparison. GET
  /admin/event-coverage is the same report as v1's event_coverage. Its own
  fetch fails into one line and never blocks the Events list below it.
  Decisions live in lib/eventCadence.ts.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { getEventCoverage, ApiError } from '../../lib/api';
  import { adminSettingsHash } from '../../lib/adminRouter';
  import {
    coverageHeadline,
    nextMonthLabel,
    formatCoverageDate,
    formatCoverageTime,
    lumaLine,
    eventHash,
  } from '../../lib/eventCadence';
  import type { Coverage } from '../../lib/types';
  import Panel from '../../lib/Panel.svelte';

  let coverage = $state<Coverage | null>(null);
  let error = $state<string | null>(null);

  onMount(() => {
    void (async () => {
      try {
        coverage = await getEventCoverage();
      } catch (err) {
        error = err instanceof ApiError ? err.message : 'Could not load next month’s coverage.';
      }
    })();
  });

  const STATE_LABELS: Record<string, string> = { ok: 'All there', drafts: 'Drafts only', missing: 'Missing' };
</script>

<Panel title={coverage ? `Next month: ${nextMonthLabel(coverage)}` : 'Next month'}>
  {#if error}
    <p class="text-error">Coverage unavailable: {error}</p>
  {:else if !coverage}
    <p class="text-muted" role="status">Checking next month…</p>
  {:else}
    <p class="headline">{coverageHeadline(coverage)}</p>
    {#if coverage.rules.length === 0}
      <p class="text-muted">
        <a href={adminSettingsHash('cadence')}>Add cadence rules</a> under Settings → Event cadence to check what is missing.
      </p>
    {:else}
      <ul class="rules">
        {#each coverage.rules as rc (rc.rule.id)}
          <li>
            <strong>{rc.rule.name}</strong>
            <span class="text-muted">({rc.rule.description})</span>
            <span
              class="badge"
              class:badge-success={rc.state === 'ok'}
              class:badge-muted={rc.state === 'drafts'}
              class:badge-danger={rc.state === 'missing'}>{STATE_LABELS[rc.state] ?? rc.state}</span
            >
            {#if rc.missing_dates.length > 0}
              <div>Missing: {rc.missing_dates.map(formatCoverageDate).join(', ')}</div>
            {:else if rc.rule.frequency === 'monthly' && rc.missing > 0}
              <div>Missing: {rc.missing} of {rc.expected.count}</div>
            {/if}
            {#if rc.drafts.length > 0}
              <div>
                Drafts:
                {#each rc.drafts as d, i (d.id)}
                  {#if i > 0},
                  {/if}<a href={eventHash(d.id)}>{d.title} ({formatCoverageTime(d.starts_at)})</a>
                {/each}
              </div>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
    {#if coverage.unmatched_events.length > 0}
      <div class="unmatched">
        Not covered by a rule:
        {#each coverage.unmatched_events as ev, i (ev.id)}
          {#if i > 0},
          {/if}<a href={eventHash(ev.id)}>{ev.title}</a>
        {/each}
      </div>
    {/if}
    <p class="text-muted luma">{lumaLine(coverage.luma)}</p>
  {/if}
</Panel>

<style>
  .headline {
    font-weight: 600;
    margin: 0 0 var(--space-2);
  }
  .rules {
    list-style: none;
    padding: 0;
    margin: 0 0 var(--space-2);
  }
  .rules li {
    margin-bottom: var(--space-2);
  }
  .unmatched {
    margin-bottom: var(--space-2);
  }
  .luma {
    margin: 0;
  }
</style>
