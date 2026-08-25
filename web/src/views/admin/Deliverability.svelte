<!--
  The admin deliverability screen (#0124, PRD §6.9): addresses with bounce
  activity sorted by streak then recency, each one's full email_events
  history on demand, and an explicit streak reset. No suppress/unsuppress
  action of its own — this issue's acceptance criteria say the screen
  "links to the existing #0100 suppression add/remove endpoints rather than
  duplicating them", so a suppressed row's action jumps to the Suppressions
  tab (onGoToSuppressions) instead of reimplementing Remove here.

  Markup and wiring only, per Pending.svelte/Dashboard.svelte's own
  convention: every formatting/classification decision not already covered
  by lib/admin.ts (subscriberStatusLabel/BadgeClass, suppressionReasonLabel,
  formatDateTime) is a call into lib/deliverability.ts.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import {
    listDeliverability,
    getDeliverabilityDetail,
    resetSoftBounceStreak,
    ApiError,
  } from '../../lib/api';
  import {
    subscriberStatusLabel,
    subscriberStatusBadgeClass,
    suppressionReasonLabel,
    formatDateTime,
  } from '../../lib/admin';
  import { deliverabilityEventLabel, deliverabilityEventBadgeClass, streakSummary } from '../../lib/deliverability';
  import type { DeliverabilityListItem, DeliverabilityDetail } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import Panel from '../../lib/Panel.svelte';

  interface Props {
    onGoToSuppressions: () => void;
  }

  let { onGoToSuppressions }: Props = $props();

  let items = $state<DeliverabilityListItem[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);

  // The currently expanded row's email, or null — only one address's
  // history is fetched/shown at a time.
  let expandedEmail = $state<string | null>(null);
  let detail = $state<DeliverabilityDetail | null>(null);
  let detailLoading = $state(false);
  let detailError = $state<string | null>(null);

  // Per-address reset-streak state, keyed by email.
  let resettingEmail = $state<string | null>(null);
  let resetErrorByEmail = $state<Record<string, string>>({});

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      const res = await listDeliverability();
      items = res.items;
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : 'Could not load deliverability data.';
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load();
  });

  async function toggleExpand(email: string): Promise<void> {
    if (expandedEmail === email) {
      expandedEmail = null;
      detail = null;
      return;
    }
    expandedEmail = email;
    detail = null;
    detailError = null;
    detailLoading = true;
    try {
      detail = await getDeliverabilityDetail(email);
    } catch (err) {
      detailError = err instanceof ApiError ? err.message : 'Could not load this address’s history.';
    } finally {
      detailLoading = false;
    }
  }

  async function resetStreak(email: string): Promise<void> {
    resettingEmail = email;
    resetErrorByEmail = { ...resetErrorByEmail, [email]: '' };
    try {
      await resetSoftBounceStreak(email);
      await load();
      if (expandedEmail === email) {
        detail = await getDeliverabilityDetail(email);
      }
    } catch (err) {
      const message = err instanceof ApiError ? err.message : 'Could not reset the streak.';
      resetErrorByEmail = { ...resetErrorByEmail, [email]: message };
    } finally {
      resettingEmail = null;
    }
  }
</script>

<Panel title="Deliverability" noPadding={items.length > 0 && !loading && !loadError}>
  {#if loading}
    <p class="text-muted" role="status">Loading deliverability data…</p>
  {:else if loadError}
    <p class="text-error" role="alert">{loadError}</p>
    <Button variant="primary" onclick={load}>Retry</Button>
  {:else if items.length === 0}
    <p class="text-muted">No addresses currently have bounce activity.</p>
  {:else}
    <div class="table-scroll">
      <table>
        <thead>
          <tr>
            <th>Email</th>
            <th>Status</th>
            <th>Streak</th>
            <th>Last bounce</th>
            <th>Last delivery</th>
            <th>Suppressed</th>
            <th class="actions-col">Actions</th>
          </tr>
        </thead>
        <tbody>
          {#each items as item (item.subscriber_id)}
            <tr>
              <td class="mono">{item.email}</td>
              <td>
                <span class="badge {subscriberStatusBadgeClass(item.status)}">
                  {subscriberStatusLabel(item.status)}
                </span>
              </td>
              <td>{streakSummary(item.soft_bounce_streak)}</td>
              <td>{item.last_bounce_at ? formatDateTime(item.last_bounce_at) : '—'}</td>
              <td>{item.last_delivery_at ? formatDateTime(item.last_delivery_at) : '—'}</td>
              <td>
                {#if item.suppressed}
                  {#each item.suppression_reasons as reason (reason)}
                    <span class="badge badge-danger">{suppressionReasonLabel(reason)}</span>
                  {/each}
                {:else}
                  <span class="text-muted">No</span>
                {/if}
              </td>
              <td class="actions-col">
                <Button onclick={() => toggleExpand(item.email)}>
                  {expandedEmail === item.email ? 'Hide history' : 'View history'}
                </Button>
                <Button
                  disabled={resettingEmail === item.email || item.soft_bounce_streak === 0}
                  onclick={() => resetStreak(item.email)}
                >
                  {resettingEmail === item.email ? 'Resetting…' : 'Reset streak'}
                </Button>
                {#if item.suppressed}
                  <Button onclick={onGoToSuppressions}>Manage suppression</Button>
                {/if}
                {#if resetErrorByEmail[item.email]}
                  <p class="text-error" role="alert">{resetErrorByEmail[item.email]}</p>
                {/if}
              </td>
            </tr>
            {#if expandedEmail === item.email}
              <tr>
                <td colspan="7">
                  {#if detailLoading}
                    <p class="text-muted" role="status">Loading history…</p>
                  {:else if detailError}
                    <p class="text-error" role="alert">{detailError}</p>
                  {:else if detail && detail.events.length === 0}
                    <p class="text-muted">No email_events history recorded for this address.</p>
                  {:else if detail}
                    <table class="history-table">
                      <thead>
                        <tr>
                          <th>Event</th>
                          <th>Diagnostic code</th>
                          <th>Campaign</th>
                          <th>Timestamp</th>
                        </tr>
                      </thead>
                      <tbody>
                        {#each detail.events as ev, i (i)}
                          <tr>
                            <td>
                              <span class="badge {deliverabilityEventBadgeClass(ev)}">
                                {deliverabilityEventLabel(ev)}
                              </span>
                            </td>
                            <td class="mono">{ev.diagnostic_code ?? '—'}</td>
                            <td>{ev.campaign_id ?? '—'}</td>
                            <td>{formatDateTime(ev.timestamp)}</td>
                          </tr>
                        {/each}
                      </tbody>
                    </table>
                  {/if}
                </td>
              </tr>
            {/if}
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</Panel>

<style>
  .history-table {
    width: 100%;
    margin-top: var(--space-2);
  }
</style>
