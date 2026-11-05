<!--
  The Event types admin section (#0613): add, rename, reorder, enable or disable
  a type, choose the default, delete an unused one. Same behaviours as the
  Interests section; same modal focus and Escape shape as Workshops.svelte.
  Decisions live in lib/eventTypes.ts. Loads on its own onMount.
-->
<script lang="ts">
  import { onMount, tick } from 'svelte';
  import {
    listEventTypes,
    createEventType,
    updateEventType,
    deleteEventType,
    ApiError,
  } from '../../lib/api';
  import { reorderSwap } from '../../lib/admin';
  import {
    sortedEventTypes,
    validateNewEventType,
    nextEventTypeSortOrder,
    eventTypeDeleteBlock,
  } from '../../lib/eventTypes';
  import type { EventType } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import Panel from '../../lib/Panel.svelte';

  let types = $state<EventType[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let actionError = $state<string | null>(null);
  let busy = $state(false);
  const sorted = $derived(sortedEventTypes(types));

  let newSlug = $state('');
  let newName = $state('');
  let addError = $state<string | null>(null);

  let editing = $state<EventType | null>(null);
  let editName = $state('');
  let editEnabled = $state(true);
  let editError = $state<string | null>(null);
  let editModalEl = $state<HTMLDivElement | null>(null);
  const editWarning = $derived(
    editing !== null && editing.enabled && !editEnabled
      ? 'Disabling stops new listings of this type. Existing listings keep it and stay visible.'
      : '',
  );

  let deleting = $state<EventType | null>(null);
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
      types = (await listEventTypes()).event_types;
    } catch (err) {
      loadError = messageFor(err, 'Could not load event types.');
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load();
  });

  async function submitAdd(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    const valid = validateNewEventType(newSlug, newName);
    if ('error' in valid) {
      addError = valid.error;
      return;
    }
    busy = true;
    addError = null;
    try {
      const created = await createEventType(valid.slug, valid.name, nextEventTypeSortOrder(types));
      types = [...types, created];
      newSlug = '';
      newName = '';
    } catch (err) {
      addError = messageFor(err, 'Could not add this event type.');
    } finally {
      busy = false;
    }
  }

  async function move(type: EventType, direction: 'up' | 'down'): Promise<void> {
    const swap = reorderSwap(sorted, type.id, direction);
    if (!swap) return;
    busy = true;
    actionError = null;
    try {
      const [a, b] = await Promise.all([
        updateEventType(swap.moved.id, { sort_order: swap.moved.sortOrder }),
        updateEventType(swap.other.id, { sort_order: swap.other.sortOrder }),
      ]);
      types = types.map((t) => (t.id === a.id ? a : t.id === b.id ? b : t));
    } catch (err) {
      actionError = messageFor(err, 'Could not reorder.');
    } finally {
      busy = false;
    }
  }

  async function makeDefault(type: EventType): Promise<void> {
    busy = true;
    actionError = null;
    try {
      await updateEventType(type.id, { is_default: true });
      await load();
    } catch (err) {
      actionError = messageFor(err, 'Could not change the default.');
    } finally {
      busy = false;
    }
  }

  function openEdit(type: EventType): void {
    editing = type;
    editName = type.name;
    editEnabled = type.enabled;
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
      const updated = await updateEventType(editing.id, { name, enabled: editEnabled });
      types = types.map((t) => (t.id === updated.id ? updated : t));
      editing = null;
    } catch (err) {
      editError = messageFor(err, 'Could not save this event type.');
    } finally {
      busy = false;
    }
  }

  function openDelete(type: EventType): void {
    deleting = type;
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
      await deleteEventType(id);
      types = types.filter((t) => t.id !== id);
      deleting = null;
    } catch (err) {
      deleteError = messageFor(err, 'Could not delete this event type.');
    } finally {
      busy = false;
    }
  }
</script>

<Panel title="Add an event type">
  <form onsubmit={submitAdd}>
    <div class="field">
      <label for="new-event-type-slug">Slug</label>
      <input id="new-event-type-slug" type="text" bind:value={newSlug} disabled={busy} />
    </div>
    <div class="field">
      <label for="new-event-type-name">Name</label>
      <input id="new-event-type-name" type="text" bind:value={newName} disabled={busy} />
    </div>
    <p class="text-muted">The slug cannot be changed later.</p>
    {#if addError}
      <p class="text-error" role="alert">{addError}</p>
    {/if}
    <Button type="submit" variant="primary" disabled={busy}>Add type</Button>
  </form>
</Panel>

<Panel title="Event types">
  {#if loading}
    <p class="text-muted" role="status">Loading event types…</p>
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
            <th>Slug</th>
            <th>Name</th>
            <th>Listings</th>
            <th>Status</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {#each sorted as type, i (type.id)}
            {@const block = eventTypeDeleteBlock(type)}
            <tr>
              <td>
                <Button
                  aria-label="Move {type.name} up"
                  disabled={busy || i === 0}
                  onclick={() => move(type, 'up')}>↑</Button
                >
                <Button
                  aria-label="Move {type.name} down"
                  disabled={busy || i === sorted.length - 1}
                  onclick={() => move(type, 'down')}>↓</Button
                >
              </td>
              <td class="mono">{type.slug}</td>
              <td>{type.name}</td>
              <td>{type.workshop_count}</td>
              <td>
                {#if type.is_default}
                  <span class="badge badge-success">Default</span>
                {:else if type.enabled}
                  <span class="badge badge-muted">Enabled</span>
                {:else}
                  <span class="badge badge-danger">Disabled</span>
                {/if}
              </td>
              <td>
                <Button aria-label="Edit {type.name}" disabled={busy} onclick={() => openEdit(type)}
                  >Edit</Button
                >
                <Button
                  aria-label="Make {type.name} the default"
                  disabled={busy || !type.enabled || type.is_default}
                  onclick={() => makeDefault(type)}>Make default</Button
                >
                <Button
                  aria-label="Delete {type.name}"
                  disabled={busy || block !== null}
                  onclick={() => openDelete(type)}>Delete</Button
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
      aria-label="Edit event type"
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
          <label for="edit-event-type-name">Name</label>
          <input id="edit-event-type-name" type="text" bind:value={editName} disabled={busy} />
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
      aria-label="Delete event type"
      tabindex="-1"
      bind:this={deleteModalEl}
      onclick={(e) => e.stopPropagation()}
      onkeydown={(e) => {
        if (e.key === 'Escape') closeDelete();
      }}
    >
      <h2 class="modal-title">Delete {deleting.name}?</h2>
      <p>This permanently removes the type and cannot be undone.</p>
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
  .mono {
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    overflow-wrap: anywhere;
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
