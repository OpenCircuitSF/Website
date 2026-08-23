<!--
  The Workshops admin subtab (#0052, PRD §5.2): a master/detail list, same
  shape as Campaigns.svelte -- list view here, full create/edit form in
  WorkshopEditor.svelte. Loads on its own onMount (Admin.svelte deliberately
  does not eagerly load every subtab), i.e. only once this subtab is first
  shown.

  Every decision below (row shape, sort order, the new-workshop modal's
  validity) is a call into lib/workshopAdmin.ts -- this file is markup and
  wiring only, per CLAUDE.md's "SPA logic goes in plain TypeScript modules
  ... Svelte components stay thin."

  Modal Escape handling: the create modal below closes on Escape from the
  MODAL ITSELF, not by letting the key bubble to the backdrop's handler.
  #0120 (still open) is exactly this bug elsewhere in this codebase: a
  panel's `onkeydown={(e) => e.stopPropagation()}` guard eats Escape before
  it ever reaches the backdrop's own `onkeydown`, so Escape silently does
  nothing. Do not copy that shape here -- see closeCreate's call site below.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { listAdminWorkshops, createWorkshop, ApiError } from '../../lib/api';
  import {
    workshopStatusLabel,
    workshopStatusBadgeClass,
    sortWorkshopsByDate,
    toggleSortDirection,
    type SortDirection,
  } from '../../lib/workshopAdmin';
  import { formatWorkshopDate } from '../../lib/workshops';
  import type { AdminWorkshop } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import Panel from '../../lib/Panel.svelte';
  import WorkshopEditor from './WorkshopEditor.svelte';

  let workshops = $state<AdminWorkshop[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let sortDirection = $state<SortDirection>('asc');

  type ViewMode = 'list' | 'editor';
  let mode = $state<ViewMode>('list');
  let activeWorkshopId = $state<number | null>(null);
  // Non-null by construction whenever mode === 'editor' -- mirrors
  // Campaigns.svelte's activeId fallback so the template can pass a plain
  // number prop without its own null-check.
  let activeId = $derived(activeWorkshopId ?? -1);

  let creating = $state(false);
  let newTitle = $state('');
  let createError = $state<string | null>(null);
  let submittingCreate = $state(false);

  let sortedWorkshops = $derived(sortWorkshopsByDate(workshops, sortDirection));
  let workshopRows = $derived(
    sortedWorkshops.map((w) => ({
      id: w.id,
      title: w.title,
      status: w.status,
      statusLabel: workshopStatusLabel(w.status),
      statusBadgeClass: workshopStatusBadgeClass(w.status),
      dateLabel: formatWorkshopDate(w.starts_at, w.ends_at),
    })),
  );
  let hasWorkshops = $derived(workshopRows.length > 0);
  let noWorkshops = $derived(workshopRows.length === 0);
  let listPanelNoPadding = $derived(hasWorkshops && !loading && loadError === null);
  let sortButtonLabel = $derived(sortDirection === 'asc' ? 'Date ▲' : 'Date ▼');

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      const resp = await listAdminWorkshops();
      workshops = resp.workshops;
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : 'Could not load workshops.';
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load();
  });

  function toggleSort(): void {
    sortDirection = toggleSortDirection(sortDirection);
  }

  function openWorkshop(id: number): void {
    mode = 'editor';
    activeWorkshopId = id;
  }

  function backToList(): void {
    mode = 'list';
    activeWorkshopId = null;
    void load();
  }

  function openCreate(): void {
    creating = true;
    newTitle = '';
    createError = null;
  }

  function closeCreate(): void {
    if (submittingCreate) return;
    creating = false;
  }

  async function submitCreate(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    const title = newTitle.trim();
    if (title === '') {
      createError = 'Title is required.';
      return;
    }
    submittingCreate = true;
    createError = null;
    try {
      const created = await createWorkshop({ title });
      creating = false;
      workshops = [created, ...workshops];
      mode = 'editor';
      activeWorkshopId = created.id;
    } catch (err) {
      createError = err instanceof ApiError ? err.message : 'Could not create this workshop.';
    } finally {
      submittingCreate = false;
    }
  }
</script>

{#if mode === 'editor'}
  {#key activeId}
    <WorkshopEditor workshopId={activeId} onBack={backToList} />
  {/key}
{:else}
  <Panel title="Workshops" noPadding={listPanelNoPadding}>
    {#if loading}
      <p class="text-muted" role="status">Loading workshops…</p>
    {:else if loadError}
      <p class="text-error" role="alert">{loadError}</p>
      <Button variant="primary" onclick={load}>Retry</Button>
    {:else if noWorkshops}
      <p class="text-muted">No workshops yet.</p>
    {:else}
      <div class="table-scroll">
        <table>
          <thead>
            <tr>
              <th>Title</th>
              <th>Status</th>
              <th>
                <button type="button" class="link-button sort-button" onclick={toggleSort}>
                  {sortButtonLabel}
                </button>
              </th>
            </tr>
          </thead>
          <tbody>
            {#each workshopRows as row (row.id)}
              <tr>
                <td>
                  <button type="button" class="link-button" onclick={() => openWorkshop(row.id)}>
                    {row.title}
                  </button>
                </td>
                <td><span class="badge {row.statusBadgeClass}">{row.statusLabel}</span></td>
                <td>{row.dateLabel}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </Panel>

  <Button variant="primary" onclick={openCreate}>New workshop</Button>

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
        aria-label="New workshop"
        tabindex="-1"
        onclick={(e) => e.stopPropagation()}
        onkeydown={(e) => {
          if (e.key === 'Escape') {
            closeCreate();
            return;
          }
          e.stopPropagation();
        }}
      >
        <h2 class="modal-title">New workshop</h2>
        <form onsubmit={submitCreate}>
          <div class="field">
            <label for="new-workshop-title">Title</label>
            <input
              id="new-workshop-title"
              type="text"
              bind:value={newTitle}
              disabled={submittingCreate}
            />
          </div>
          <p class="text-muted create-hint">
            The slug and every other field are set from the full editor after the workshop is created.
          </p>
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
  .sort-button {
    text-decoration: none;
    font-weight: 600;
  }
  .create-hint {
    margin: 0 0 var(--space-2);
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
