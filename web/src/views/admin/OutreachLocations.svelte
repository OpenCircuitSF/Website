<!--
  The Outreach tab's Locations sub-view (#0548): saved flyer locations, so each
  flyer run can make one tracked link per place. Validation, code suggestion
  and patch shaping live in lib/outreachLocations.ts; this file is markup and
  wiring. The add/edit form renders inline above the table (not a modal) and
  the delete confirmation is an inline Yes/No, so nothing here needs a focus
  trap. Server messages (409 duplicate code, locked code) land in the
  always-rendered role="status" element below the form.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import {
    listOutreachLocations,
    createOutreachLocation,
    updateOutreachLocation,
    deleteOutreachLocation,
    ApiError,
  } from '../../lib/api';
  import {
    CODE_MAX,
    canDelete,
    canEditCode,
    formIsValid,
    locationPatch,
    locationStatusLabel,
    normalizeCodeInput,
    suggestCode,
    uniqueCode,
    validateLocationForm,
  } from '../../lib/outreachLocations';
  import type { OutreachLocation } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import Panel from '../../lib/Panel.svelte';

  let rows = $state<OutreachLocation[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let status = $state('');

  // null = form closed; 'new' = creating; a location = editing it.
  let editing = $state<OutreachLocation | 'new' | null>(null);
  let name = $state('');
  let code = $state('');
  let address = $state('');
  let notes = $state('');
  let active = $state(true);
  let codeEdited = $state(false);
  let saving = $state(false);
  let confirmDeleteId = $state<number | null>(null);

  const editingLocation = $derived(editing && editing !== 'new' ? editing : null);
  const takenCodes = $derived(new Set(rows.filter((r) => r.id !== editingLocation?.id).map((r) => r.code)));
  const errors = $derived(validateLocationForm({ name, code, address, notes }));
  const valid = $derived(formIsValid(errors));

  function message(err: unknown, fallback: string): string {
    return err instanceof ApiError ? err.message : fallback;
  }

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      rows = (await listOutreachLocations()).locations;
    } catch (err) {
      loadError = message(err, 'Could not load locations. Please try again.');
    } finally {
      loading = false;
    }
  }

  function openNew(): void {
    editing = 'new';
    name = '';
    code = '';
    address = '';
    notes = '';
    active = true;
    codeEdited = false;
    status = '';
  }

  function openEdit(loc: OutreachLocation): void {
    editing = loc;
    name = loc.name;
    code = loc.code;
    address = loc.address;
    notes = loc.notes;
    active = loc.active;
    codeEdited = true;
    status = '';
  }

  function closeForm(): void {
    editing = null;
  }

  function onNameInput(): void {
    if (editing === 'new' && !codeEdited) code = uniqueCode(suggestCode(name), takenCodes);
  }

  function onCodeInput(): void {
    codeEdited = true;
    code = normalizeCodeInput(code);
  }

  async function save(e: Event): Promise<void> {
    e.preventDefault();
    if (!valid || saving || editing === null) return;
    saving = true;
    status = '';
    try {
      if (editing === 'new') {
        await createOutreachLocation({ name: name.trim(), code, address: address.trim(), notes: notes.trim(), active });
        status = 'Location added.';
      } else {
        const patch = locationPatch(editing, { name, code, address, notes });
        const body: Record<string, unknown> = { ...(patch ?? {}) };
        if (active !== editing.active) body.active = active;
        if (Object.keys(body).length > 0) await updateOutreachLocation(editing.id, body);
        status = 'Location saved.';
      }
      editing = null;
      await load();
    } catch (err) {
      status = message(err, 'Could not save the location. Please try again.');
    } finally {
      saving = false;
    }
  }

  async function toggleActive(loc: OutreachLocation): Promise<void> {
    status = '';
    try {
      await updateOutreachLocation(loc.id, { active: !loc.active });
      status = loc.active ? `${loc.name} deactivated.` : `${loc.name} reactivated.`;
      await load();
    } catch (err) {
      status = message(err, 'Could not update the location. Please try again.');
    }
  }

  async function confirmDelete(loc: OutreachLocation): Promise<void> {
    status = '';
    try {
      await deleteOutreachLocation(loc.id);
      confirmDeleteId = null;
      status = `${loc.name} deleted.`;
      await load();
    } catch (err) {
      confirmDeleteId = null;
      status = message(err, 'Could not delete the location. Please try again.');
    }
  }

  onMount(load);
</script>

<Panel title="Flyer locations">
  <p class="text-muted">Places flyers go. Each flyer run makes one tracked link per active location.</p>
  <p class="loc-status" class:text-muted={status !== ''} role="status">{status}</p>

  {#if editing === null}
    <Button variant="primary" onclick={openNew}>Add location</Button>
  {:else}
    <form class="loc-form" onsubmit={save}>
      <h3 class="loc-form-title">{editing === 'new' ? 'Add location' : `Edit ${editing.name}`}</h3>
      <div class="field">
        <label for="loc-name">Name</label>
        <input
          id="loc-name"
          type="text"
          bind:value={name}
          oninput={onNameInput}
          aria-invalid={errors.name ? 'true' : undefined}
          aria-describedby="loc-name-err"
        />
        <p class="text-error" id="loc-name-err">{errors.name ?? ''}</p>
      </div>
      <div class="field">
        <label for="loc-code">Code</label>
        <input
          id="loc-code"
          class="mono"
          type="text"
          bind:value={code}
          oninput={onCodeInput}
          disabled={!canEditCode(editingLocation)}
          maxlength={CODE_MAX + 8}
          aria-invalid={errors.code ? 'true' : undefined}
          aria-describedby="loc-code-help loc-code-err"
        />
        <p class="text-muted" id="loc-code-help">
          {canEditCode(editingLocation)
            ? 'Short and permanent once a flyer link uses it.'
            : 'Flyer links use this code, so it can no longer change.'}
        </p>
        <p class="text-error" id="loc-code-err">{errors.code ?? ''}</p>
      </div>
      <div class="field">
        <label for="loc-address">Address</label>
        <input id="loc-address" type="text" bind:value={address} aria-describedby="loc-address-err" />
        <p class="text-error" id="loc-address-err">{errors.address ?? ''}</p>
      </div>
      <div class="field">
        <label for="loc-notes">Notes</label>
        <textarea id="loc-notes" rows="3" bind:value={notes} aria-describedby="loc-notes-err"></textarea>
        <p class="text-error" id="loc-notes-err">{errors.notes ?? ''}</p>
      </div>
      <div class="field">
        <label><input type="checkbox" bind:checked={active} /> Active</label>
      </div>
      <div class="loc-actions">
        <Button type="submit" variant="primary" disabled={!valid || saving}>{saving ? 'Saving…' : 'Save'}</Button>
        <Button onclick={closeForm} disabled={saving}>Cancel</Button>
      </div>
    </form>
  {/if}

  {#if loading}
    <p class="text-muted">Loading locations…</p>
  {:else if loadError}
    <p class="text-error" role="alert">{loadError}</p>
    <Button variant="primary" onclick={load}>Retry</Button>
  {:else if rows.length === 0}
    <p class="text-muted">No locations yet.</p>
  {:else}
    <div class="table-scroll">
      <table>
        <thead>
          <tr>
            <th>Name</th>
            <th>Address</th>
            <th>Code</th>
            <th>Notes</th>
            <th>Status</th>
            <th><span class="sr-only">Actions</span></th>
          </tr>
        </thead>
        <tbody>
          {#each rows as loc (loc.id)}
            <tr>
              <td>{loc.name}</td>
              <td>{loc.address}</td>
              <td class="mono">{loc.code}</td>
              <td class="loc-notes" title={loc.notes}>{loc.notes}</td>
              <td>{locationStatusLabel(loc)}</td>
              <td class="loc-row-actions">
                <Button variant="subtle" onclick={() => openEdit(loc)}>Edit</Button>
                <Button variant="subtle" onclick={() => toggleActive(loc)}>{loc.active ? 'Deactivate' : 'Reactivate'}</Button>
                {#if canDelete(loc)}
                  {#if confirmDeleteId === loc.id}
                    <span>Delete?</span>
                    <Button variant="danger" onclick={() => confirmDelete(loc)}>Yes</Button>
                    <Button onclick={() => (confirmDeleteId = null)}>No</Button>
                  {:else}
                    <Button variant="subtle" onclick={() => (confirmDeleteId = loc.id)}>Delete</Button>
                  {/if}
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
  .loc-form {
    margin: var(--space-3) 0;
    padding: var(--space-3) 0;
    border-top: var(--border-w) solid var(--border);
    border-bottom: var(--border-w) solid var(--border);
  }
  .loc-form-title {
    font-size: var(--fs-lg);
    margin: 0 0 var(--space-3);
  }
  .field {
    margin-bottom: var(--space-2);
  }
  .field label {
    display: block;
    font-size: var(--fs-sm);
    color: var(--text-muted);
    margin-bottom: var(--space-1);
  }
  textarea {
    width: 100%;
    font-family: var(--font);
    font-size: var(--fs-base);
  }
  .mono {
    font-family: var(--font-mono, monospace);
  }
  .loc-actions {
    display: flex;
    gap: var(--space-2);
  }
  .loc-notes {
    max-width: 18rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .loc-row-actions {
    white-space: nowrap;
  }
  .loc-status {
    min-height: 1.5em;
  }
</style>
