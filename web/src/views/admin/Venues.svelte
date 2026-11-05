<!--
  The Venues admin section (#0617): add, edit, reorder, enable or disable a venue,
  choose the default, delete an unused one. Modeled on EventTypes.svelte. Decisions
  live in lib/venues.ts. Loads on its own onMount.
-->
<script lang="ts">
  import { onMount, tick } from 'svelte';
  import {
    listVenues,
    createVenue,
    updateVenue,
    deleteVenue,
    ApiError,
  } from '../../lib/api';
  import { reorderSwap } from '../../lib/admin';
  import {
    sortedVenues,
    validateNewVenue,
    nextVenueSortOrder,
    venueDeleteBlock,
  } from '../../lib/venues';
  import type { Venue } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import Panel from '../../lib/Panel.svelte';

  let venues = $state<Venue[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let actionError = $state<string | null>(null);
  let busy = $state(false);
  const sorted = $derived(sortedVenues(venues));

  let newName = $state('');
  let newAddress = $state('');
  let newNote = $state('');
  let addError = $state<string | null>(null);

  let editing = $state<Venue | null>(null);
  let editName = $state('');
  let editAddress = $state('');
  let editNote = $state('');
  let editEnabled = $state(true);
  let editError = $state<string | null>(null);
  let editModalEl = $state<HTMLDivElement | null>(null);
  const editWarning = $derived(
    editing !== null && editing.enabled && !editEnabled
      ? 'Disabling stops new listings here. Existing listings keep this venue and stay visible.'
      : '',
  );

  let deleting = $state<Venue | null>(null);
  let deleteError = $state<string | null>(null);
  let deleteModalEl = $state<HTMLDivElement | null>(null);

  $effect(() => {
    if (editing) void tick().then(() => editModalEl?.focus());
  });
  $effect(() => {
    if (deleting) void tick().then(() => deleteModalEl?.focus());
  });

  function messageFor(err: unknown, fallback: string): string {
    return err instanceof ApiError ? err.message : fallback;
  }

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      venues = (await listVenues()).venues;
    } catch (err) {
      loadError = messageFor(err, 'Could not load venues.');
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load();
  });

  async function submitAdd(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    const valid = validateNewVenue(newName);
    if ('error' in valid) {
      addError = valid.error;
      return;
    }
    busy = true;
    addError = null;
    try {
      const created = await createVenue({
        name: valid.name,
        address: newAddress.trim(),
        note: newNote.trim(),
        sort_order: nextVenueSortOrder(venues),
      });
      venues = [...venues, created];
      newName = '';
      newAddress = '';
      newNote = '';
    } catch (err) {
      addError = messageFor(err, 'Could not add this venue.');
    } finally {
      busy = false;
    }
  }

  async function move(venue: Venue, direction: 'up' | 'down'): Promise<void> {
    const swap = reorderSwap(sorted, venue.id, direction);
    if (!swap) return;
    busy = true;
    actionError = null;
    try {
      const [a, b] = await Promise.all([
        updateVenue(swap.moved.id, { sort_order: swap.moved.sortOrder }),
        updateVenue(swap.other.id, { sort_order: swap.other.sortOrder }),
      ]);
      venues = venues.map((v) => (v.id === a.id ? a : v.id === b.id ? b : v));
    } catch (err) {
      actionError = messageFor(err, 'Could not reorder.');
    } finally {
      busy = false;
    }
  }

  async function makeDefault(venue: Venue): Promise<void> {
    busy = true;
    actionError = null;
    try {
      await updateVenue(venue.id, { is_default: true });
      await load();
    } catch (err) {
      actionError = messageFor(err, 'Could not change the default.');
    } finally {
      busy = false;
    }
  }

  function openEdit(venue: Venue): void {
    editing = venue;
    editName = venue.name;
    editAddress = venue.address ?? '';
    editNote = venue.note ?? '';
    editEnabled = venue.enabled;
    editError = null;
  }

  function closeEdit(): void {
    if (busy) return;
    editing = null;
  }

  async function submitEdit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    if (!editing) return;
    const name = editName.trim();
    if (name === '') {
      editError = 'Name is required.';
      return;
    }
    busy = true;
    editError = null;
    try {
      const updated = await updateVenue(editing.id, {
        name,
        address: editAddress.trim(),
        note: editNote.trim(),
        enabled: editEnabled,
      });
      venues = venues.map((v) => (v.id === updated.id ? updated : v));
      editing = null;
    } catch (err) {
      editError = messageFor(err, 'Could not save this venue.');
    } finally {
      busy = false;
    }
  }

  function openDelete(venue: Venue): void {
    deleting = venue;
    deleteError = null;
  }

  function closeDelete(): void {
    if (busy) return;
    deleting = null;
  }

  async function confirmDelete(): Promise<void> {
    if (!deleting) return;
    const id = deleting.id;
    busy = true;
    deleteError = null;
    try {
      await deleteVenue(id);
      venues = venues.filter((v) => v.id !== id);
      deleting = null;
    } catch (err) {
      deleteError = messageFor(err, 'Could not delete this venue.');
    } finally {
      busy = false;
    }
  }
</script>

<Panel title="Add a venue">
  <form onsubmit={submitAdd}>
    <div class="field">
      <label for="new-venue-name">Name</label>
      <input id="new-venue-name" type="text" bind:value={newName} disabled={busy} />
    </div>
    <div class="field">
      <label for="new-venue-address">Address</label>
      <input id="new-venue-address" type="text" bind:value={newAddress} disabled={busy} />
    </div>
    <div class="field">
      <label for="new-venue-note">Note</label>
      <input id="new-venue-note" type="text" bind:value={newNote} disabled={busy} />
    </div>
    {#if addError}
      <p class="text-error" role="alert">{addError}</p>
    {/if}
    <Button type="submit" variant="primary" disabled={busy}>Add venue</Button>
  </form>
</Panel>

<Panel title="Venues">
  {#if loading}
    <p class="text-muted" role="status">Loading venues…</p>
  {:else if loadError}
    <p class="text-error" role="alert">{loadError}</p>
    <Button variant="primary" onclick={load}>Retry</Button>
  {:else}
    {#if actionError}
      <p class="text-error" role="alert">{actionError}</p>
    {/if}
    <div class="table-scroll">
      <table>
        <thead>
          <tr>
            <th>Order</th>
            <th>Name</th>
            <th>Address</th>
            <th>Listings</th>
            <th>Status</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {#each sorted as venue, i (venue.id)}
            {@const block = venueDeleteBlock(venue)}
            <tr>
              <td>
                <Button
                  aria-label="Move {venue.name} up"
                  disabled={busy || i === 0}
                  onclick={() => move(venue, 'up')}>↑</Button
                >
                <Button
                  aria-label="Move {venue.name} down"
                  disabled={busy || i === sorted.length - 1}
                  onclick={() => move(venue, 'down')}>↓</Button
                >
              </td>
              <td>{venue.name}</td>
              <td>{venue.address ?? ''}</td>
              <td>{venue.workshop_count}</td>
              <td>
                {#if venue.is_default}
                  <span class="badge badge-success">Default</span>
                {:else if venue.enabled}
                  <span class="badge badge-muted">Enabled</span>
                {:else}
                  <span class="badge badge-danger">Disabled</span>
                {/if}
              </td>
              <td>
                <Button aria-label="Edit {venue.name}" disabled={busy} onclick={() => openEdit(venue)}
                  >Edit</Button
                >
                <Button
                  aria-label="Make {venue.name} the default"
                  disabled={busy || !venue.enabled || venue.is_default}
                  onclick={() => makeDefault(venue)}>Make default</Button
                >
                <Button
                  aria-label="Delete {venue.name}"
                  disabled={busy || block !== null}
                  onclick={() => openDelete(venue)}>Delete</Button
                >
                {#if block}
                  <p class="text-muted block-reason">{block}</p>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</Panel>

{#if editing}
  <div
    class="modal-backdrop"
    role="presentation"
    onclick={closeEdit}
    onkeydown={(e) => {
      if (e.key === 'Escape') closeEdit();
    }}
  >
    <div
      class="modal"
      role="dialog"
      aria-modal="true"
      aria-label="Edit venue"
      tabindex="-1"
      bind:this={editModalEl}
      onclick={(e) => e.stopPropagation()}
      onkeydown={(e) => {
        if (e.key === 'Escape') closeEdit();
      }}
    >
      <h2 class="modal-title">Edit {editing.name}</h2>
      <form onsubmit={submitEdit}>
        <div class="field">
          <label for="edit-venue-name">Name</label>
          <input id="edit-venue-name" type="text" bind:value={editName} disabled={busy} />
        </div>
        <div class="field">
          <label for="edit-venue-address">Address</label>
          <input id="edit-venue-address" type="text" bind:value={editAddress} disabled={busy} />
        </div>
        <div class="field">
          <label for="edit-venue-note">Note</label>
          <input id="edit-venue-note" type="text" bind:value={editNote} disabled={busy} />
        </div>
        <label class="checkbox-row">
          <input type="checkbox" bind:checked={editEnabled} disabled={busy} />
          Enabled
        </label>
        <p class="text-warn" role="status">{editWarning}</p>
        {#if editError}
          <p class="text-error" role="alert">{editError}</p>
        {/if}
        <div class="row">
          <Button type="submit" variant="primary" disabled={busy}>Save</Button>
          <Button disabled={busy} onclick={closeEdit}>Cancel</Button>
        </div>
      </form>
    </div>
  </div>
{/if}

{#if deleting}
  <div
    class="modal-backdrop"
    role="presentation"
    onclick={closeDelete}
    onkeydown={(e) => {
      if (e.key === 'Escape') closeDelete();
    }}
  >
    <div
      class="modal"
      role="dialog"
      aria-modal="true"
      aria-label="Delete venue"
      tabindex="-1"
      bind:this={deleteModalEl}
      onclick={(e) => e.stopPropagation()}
      onkeydown={(e) => {
        if (e.key === 'Escape') closeDelete();
      }}
    >
      <h2 class="modal-title">Delete {deleting.name}?</h2>
      <p>This permanently removes the venue and cannot be undone.</p>
      {#if deleteError}
        <p class="text-error" role="alert">{deleteError}</p>
      {/if}
      <div class="row">
        <Button variant="primary" disabled={busy} onclick={confirmDelete}>Confirm delete</Button>
        <Button disabled={busy} onclick={closeDelete}>Cancel</Button>
      </div>
    </div>
  </div>
{/if}

<style>
  .block-reason {
    margin: var(--space-1) 0 0;
  }
  .checkbox-row {
    display: flex;
    align-items: center;
    gap: var(--space-2);
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
