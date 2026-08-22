<!--
  The Campaigns admin subtab (#0047): a master/detail list — not a modal,
  per the plan's own decision (a compose screen is not a short form). Loads
  on ITS OWN onMount (Admin.svelte deliberately does not eagerly load it),
  i.e. only once the subtab is first shown.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { listCampaigns, createCampaign, ApiError } from '../../lib/api';
  import {
    campaignStatusLabel,
    campaignStatusBadgeClass,
    validateCampaignDraft,
    wasDemotedAfterScheduling,
  } from '../../lib/campaigns';
  import { canViewCampaignStats } from '../../lib/campaignStats';
  import { formatDateTime } from '../../lib/admin';
  import type { Campaign } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import Panel from '../../lib/Panel.svelte';
  import CampaignEditor from './CampaignEditor.svelte';
  import CampaignStats from './CampaignStats.svelte';

  interface Props {
    onGoToSettings: () => void;
    onGoToAudit: () => void;
  }

  let { onGoToSettings, onGoToAudit }: Props = $props();

  let campaigns = $state<Campaign[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);

  // Third arm on what was a binary list/editor swap (#0049's carried-in
  // review finding): 'list' | 'editor' | 'stats'. activeCampaignId is
  // meaningless in 'list' mode and always set alongside a transition into
  // either of the other two — never read without checking mode first (see
  // hasEditorSelection/hasStatsSelection below).
  type CampaignsViewMode = 'list' | 'editor' | 'stats';
  let mode = $state<CampaignsViewMode>('list');
  let activeCampaignId = $state<number | null>(null);

  let creating = $state(false);
  let newName = $state('');
  let newSubject = $state('');
  let newBody = $state('');
  let createError = $state<string | null>(null);
  let submittingCreate = $state(false);

  let campaignRows = $derived(
    campaigns.map((c) => ({
      id: c.id,
      name: c.name,
      status: c.status,
      statusLabel: campaignStatusLabel(c.status),
      statusBadgeClass: campaignStatusBadgeClass(c.status),
      demoted: wasDemotedAfterScheduling(c),
      updatedAtLabel: formatDateTime(c.updated_at),
      canStats: canViewCampaignStats(c.status),
    })),
  );
  let isEditorView = $derived(mode === 'editor');
  let isStatsView = $derived(mode === 'stats');
  let isListView = $derived(mode === 'list');
  // Non-null by construction whenever isEditorView/isStatsView is true —
  // lets the template pass a plain number prop without a null-check of its
  // own (mirrors the pre-#0049 selectedCampaignId fallback).
  let activeId = $derived(activeCampaignId ?? -1);
  let hasCampaigns = $derived(campaignRows.length > 0);
  let noCampaigns = $derived(campaignRows.length === 0);
  let listPanelNoPadding = $derived(hasCampaigns && !loading && loadError === null);

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      const resp = await listCampaigns();
      campaigns = resp.campaigns;
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : 'Could not load campaigns.';
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load();
  });

  function openCampaign(id: number): void {
    mode = 'editor';
    activeCampaignId = id;
  }

  function openStats(id: number): void {
    mode = 'stats';
    activeCampaignId = id;
  }

  function backToList(): void {
    mode = 'list';
    activeCampaignId = null;
    void load();
  }

  function openCreate(): void {
    creating = true;
    newName = '';
    newSubject = '';
    newBody = '';
    createError = null;
  }

  function closeCreate(): void {
    if (submittingCreate) return;
    creating = false;
  }

  async function submitCreate(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    const validation = validateCampaignDraft({
      name: newName,
      subject: newSubject,
      bodyMd: newBody,
      mode: 'all',
      interestIds: [],
    });
    if (!validation.ok) {
      createError = validation.error;
      return;
    }
    submittingCreate = true;
    createError = null;
    try {
      const created = await createCampaign({
        name: newName,
        subject: newSubject,
        body_md: newBody,
        audience_mode: 'all',
      });
      creating = false;
      campaigns = [created, ...campaigns];
      mode = 'editor';
      activeCampaignId = created.id;
    } catch (err) {
      createError = err instanceof ApiError ? err.message : 'Could not create this campaign.';
    } finally {
      submittingCreate = false;
    }
  }
</script>

{#if isEditorView}
  {#key activeId}
    <CampaignEditor campaignId={activeId} onBack={backToList} {onGoToSettings} {onGoToAudit} />
  {/key}
{:else if isStatsView}
  {#key activeId}
    <CampaignStats campaignId={activeId} onBack={backToList} />
  {/key}
{:else if isListView}
  <Panel title="Campaigns" noPadding={listPanelNoPadding}>
    {#if loading}
      <p class="text-muted" role="status">Loading campaigns…</p>
    {:else if loadError}
      <p class="text-error" role="alert">{loadError}</p>
      <Button variant="primary" onclick={load}>Retry</Button>
    {:else if noCampaigns}
      <p class="text-muted">No campaigns yet.</p>
    {:else}
      <div class="table-scroll">
        <table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Status</th>
              <th>Updated</th>
              <th>Stats</th>
            </tr>
          </thead>
          <tbody>
            {#each campaignRows as row (row.id)}
              <tr>
                <td>
                  <button type="button" class="link-button" onclick={() => openCampaign(row.id)}>
                    {row.name}
                  </button>
                  {#if row.demoted}
                    <p class="text-error demoted-hint">Returned to draft — worker refused it</p>
                  {/if}
                </td>
                <td><span class="badge {row.statusBadgeClass}">{row.statusLabel}</span></td>
                <td>{row.updatedAtLabel}</td>
                <td>
                  <button
                    type="button"
                    class="link-button"
                    disabled={!row.canStats}
                    onclick={() => openStats(row.id)}
                  >
                    Stats
                  </button>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </Panel>

  <Button variant="primary" onclick={openCreate}>New campaign</Button>

  {#if creating}
    <div
      class="modal-backdrop"
      role="presentation"
      onclick={closeCreate}
      onkeydown={(e) => {
        if (e.key === 'Escape') closeCreate();
      }}
    >
      <div
        class="modal"
        role="dialog"
        aria-modal="true"
        aria-label="New campaign"
        tabindex="-1"
        onclick={(e) => e.stopPropagation()}
        onkeydown={(e) => e.stopPropagation()}
      >
        <h2 class="modal-title">New campaign</h2>
        <form onsubmit={submitCreate}>
          <div class="field">
            <label for="new-campaign-name">Name (internal)</label>
            <input id="new-campaign-name" type="text" bind:value={newName} disabled={submittingCreate} />
          </div>
          <div class="field">
            <label for="new-campaign-subject">Subject</label>
            <input id="new-campaign-subject" type="text" bind:value={newSubject} disabled={submittingCreate} />
          </div>
          <div class="field">
            <label for="new-campaign-body">Body (Markdown)</label>
            <textarea
              id="new-campaign-body"
              rows="6"
              class="mono"
              bind:value={newBody}
              disabled={submittingCreate}
            ></textarea>
          </div>
          {#if createError}
            <p class="text-error" role="alert">{createError}</p>
          {/if}
          <div class="row" style="margin-top: var(--space-3);">
            <Button type="submit" variant="primary" disabled={submittingCreate}>
              {submittingCreate ? 'Creating…' : 'Create draft'}
            </Button>
            <Button disabled={submittingCreate} onclick={closeCreate}>Cancel</Button>
          </div>
        </form>
      </div>
    </div>
  {/if}
{/if}

<style>
  .mono {
    font-family: var(--font-mono);
  }
  .demoted-hint {
    margin: var(--space-1) 0 0;
    font-size: var(--fs-sm);
  }
  .link-button {
    background: none;
    border: none;
    color: var(--accent);
    cursor: pointer;
    text-decoration: underline;
    font-family: var(--font);
    font-size: var(--fs-base);
    padding: 0;
  }
  .link-button:disabled {
    color: var(--text-muted);
    cursor: default;
    text-decoration: none;
  }
  .modal-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.4);
    display: flex;
    align-items: center;
    justify-content: center;
    padding: var(--space-4);
    z-index: 10;
  }
  .modal {
    background: var(--bg-panel);
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
    padding: var(--space-5);
    width: 100%;
    max-width: 480px;
  }
  .modal-title {
    font-size: var(--fs-lg);
    font-weight: 600;
    margin: 0 0 var(--space-4);
  }
</style>
