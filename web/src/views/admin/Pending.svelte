<!--
  The pending-subscriber admin screen (#0128, PRD §5.2/§6.3): who signed up
  but never confirmed, how long they have been waiting, whether their token
  has expired, the outbound-queue delivery state of their confirmation mail
  (#0126's durable queue turning "why didn't they confirm?" from a mystery
  into a fact), and a per-subscriber resend. No bulk resend, deliberately
  (issues/0128.md Notes: pending addresses are the ones least likely to want
  mail, and a button that re-mails every pending address at once is a spam
  complaint generator).

  Markup and wiring only, per Dashboard.svelte's own convention: every
  formatting/classification decision is a call into lib/pending.ts.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { listPendingSubscribers, resendConfirmation, ApiError } from '../../lib/api';
  import {
    formatPendingAge,
    pendingSignupSource,
    queueStateLabel,
    queueStateBadgeClass,
    pendingExpiredBadgeClass,
  } from '../../lib/pending';
  import { formatDateTime } from '../../lib/admin';
  import type { PendingSubscriber } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import Panel from '../../lib/Panel.svelte';

  let rows = $state<PendingSubscriber[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let oldestFirst = $state(true);

  // Per-row resend state, keyed by subscriber id — an admin resending to
  // one address must not disable or spinner every other row.
  let resendingID = $state<number | null>(null);
  let resendErrorByID = $state<Record<number, string>>({});
  let resendNoticeByID = $state<Record<number, string>>({});

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      const res = await listPendingSubscribers(oldestFirst);
      rows = res.pending;
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : 'Could not load pending signups.';
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load();
  });

  function toggleSort(): void {
    oldestFirst = !oldestFirst;
    void load();
  }

  async function resend(id: number): Promise<void> {
    resendingID = id;
    resendErrorByID = { ...resendErrorByID, [id]: '' };
    resendNoticeByID = { ...resendNoticeByID, [id]: '' };
    try {
      await resendConfirmation(id);
      resendNoticeByID = { ...resendNoticeByID, [id]: 'Confirmation resent.' };
      // Refresh so the age/expiry/queue-state columns reflect the fresh
      // send rather than showing stale data until the next full reload.
      await load();
    } catch (err) {
      const message = err instanceof ApiError ? err.message : 'Could not resend the confirmation.';
      resendErrorByID = { ...resendErrorByID, [id]: message };
    } finally {
      resendingID = null;
    }
  }
</script>

<Panel title="Pending confirmations" noPadding={rows.length > 0 && !loading && !loadError}>
  {#if loading}
    <p class="text-muted" role="status">Loading pending signups…</p>
  {:else if loadError}
    <p class="text-error" role="alert">{loadError}</p>
    <Button variant="primary" onclick={load}>Retry</Button>
  {:else if rows.length === 0}
    <p class="text-muted">No pending signups — everyone who signed up has confirmed.</p>
  {:else}
    <div class="table-scroll">
      <table>
        <thead>
          <tr>
            <th>Email</th>
            <th>
              <button type="button" class="sort-toggle" onclick={toggleSort}>
                Age {oldestFirst ? '(oldest first)' : '(newest first)'}
              </button>
            </th>
            <th>Expires</th>
            <th>Source</th>
            <th>Confirmation mail</th>
            <th class="actions-col">Action</th>
          </tr>
        </thead>
        <tbody>
          {#each rows as row (row.id)}
            <tr>
              <td>{row.email}</td>
              <td>
                {formatPendingAge(row.age_seconds)}
                {#if row.expired}
                  <span class="badge {pendingExpiredBadgeClass(row.expired)}">Expired</span>
                {/if}
              </td>
              <td>{row.confirm_expires_at ? formatDateTime(row.confirm_expires_at) : '—'}</td>
              <td>{pendingSignupSource(row)}</td>
              <td>
                <span class="badge {queueStateBadgeClass(row.queue_state)}">
                  {queueStateLabel(row.queue_state)}
                </span>
              </td>
              <td class="actions-col">
                <Button
                  disabled={resendingID === row.id}
                  onclick={() => resend(row.id)}
                >
                  {resendingID === row.id ? 'Resending…' : 'Resend'}
                </Button>
                {#if resendErrorByID[row.id]}
                  <p class="text-error" role="alert">{resendErrorByID[row.id]}</p>
                {/if}
                {#if resendNoticeByID[row.id]}
                  <p class="text-muted" role="status">{resendNoticeByID[row.id]}</p>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</Panel>

<style>
  .sort-toggle {
    background: none;
    border: none;
    padding: 0;
    font: inherit;
    color: inherit;
    cursor: pointer;
    text-decoration: underline dotted;
  }
</style>
