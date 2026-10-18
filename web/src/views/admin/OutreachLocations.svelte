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
  let statusIsError = $state(false);

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
  // #0589: a field's error shows once it has been touched (blurred) or the
  // form has been submitted, never on the untouched empty draft.
  let touched = $state<Record<string, boolean>>({});
  let submitted = $state(false);
  // The location just deactivated, so the status line can offer Undo.
  let undoLoc = $state<OutreachLocation | null>(null);

  const editingLocation = $derived(editing && editing !== 'new' ? editing : null);
  const takenCodes = $derived(new Set(rows.filter((r) => r.id !== editingLocation?.id).map((r) => r.code)));
  const errors = $derived(validateLocationForm({ name, code, address, notes }));
  const valid = $derived(formIsValid(errors));
  const shown = $derived({
    name: submitted || touched.name ? errors.name : undefined,
    code: submitted || touched.code ? errors.code : undefined,
    address: submitted || touched.address ? errors.address : undefined,
    notes: submitted || touched.notes ? errors.notes : undefined,
  });

  function touch(field: string): void {
    touched = { ...touched, [field]: true };
  }

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
    touched = {};
    submitted = false;
    undoLoc = null;
    status = '';
    statusIsError = false;
  }

  function openEdit(loc: OutreachLocation): void {
    editing = loc;
    name = loc.name;
    code = loc.code;
    address = loc.address;
    notes = loc.notes;
    active = loc.active;
    codeEdited = true;
    touched = {};
    submitted = false;
    undoLoc = null;
    status = '';
    statusIsError = false;
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
    submitted = true;
    if (!valid || saving || editing === null) return;
    saving = true;
    status = '';
    statusIsError = false;
    try {
      if (editing === 'new') {
        await createOutreachLocation({ name: name.trim(), code, address: address.trim(), notes: notes.trim(), active });
        status = 'Location added.';
        statusIsError = false;
      } else {
        const patch = locationPatch(editing, { name, code, address, notes });
        const body: Record<string, unknown> = { ...(patch ?? {}) };
        if (active !== editing.active) body.active = active;
        if (Object.keys(body).length > 0) await updateOutreachLocation(editing.id, body);
        status = 'Location saved.';
        statusIsError = false;
      }
      editing = null;
      await load();
    } catch (err) {
      status = message(err, 'Could not save the location. Please try again.');
      statusIsError = true;
    } finally {
      saving = false;
    }
  }

  async function toggleActive(loc: OutreachLocation): Promise<void> {
    status = '';
    statusIsError = false;
    undoLoc = null;
    try {
      await updateOutreachLocation(loc.id, { active: !loc.active });
      status = loc.active ? `${loc.name} deactivated.` : `${loc.name} reactivated.`;
      if (loc.active) undoLoc = loc;
      statusIsError = false;
      await load();
    } catch (err) {
      status = message(err, 'Could not update the location. Please try again.');
      statusIsError = true;
    }
  }

  async function undoDeactivate(): Promise<void> {
    const loc = undoLoc;
    if (!loc) return;
    undoLoc = null;
    status = '';
    try {
      await updateOutreachLocation(loc.id, { active: true });
      status = `${loc.name} reactivated.`;
      statusIsError = false;
      await load();
    } catch (err) {
      status = message(err, 'Could not undo that. Please try again.');
      statusIsError = true;
    }
  }

  async function confirmDelete(loc: OutreachLocation): Promise<void> {
    status = '';
    statusIsError = false;
    try {
      await deleteOutreachLocation(loc.id);
      confirmDeleteId = null;
      status = `${loc.name} deleted.`;
      statusIsError = false;
      await load();
    } catch (err) {
      confirmDeleteId = null;
      status = message(err, 'Could not delete the location. Please try again.');
      statusIsError = true;
    }
  }

  onMount(load);
</script>

<div class="fit">
<Panel title="Flyer locations">
  <p class="text-muted">Places flyers go. Each flyer run makes one short link per active location.</p>
  <p class="loc-status" class:text-muted={status !== '' && !statusIsError} class:text-error={statusIsError} role="status">{status}</p>
  {#if undoLoc}
    <p><Button onclick={undoDeactivate}>Undo: reactivate {undoLoc.name}</Button></p>
  {/if}

  {#if editing === null}
    <div class="loc-add">
      <Button variant="primary" onclick={openNew}>Add location</Button>
    </div>
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
          aria-invalid={shown.name ? 'true' : undefined}
          onblur={() => touch('name')}
          aria-describedby="loc-name-err"
        />
        <p class="text-error" id="loc-name-err">{shown.name ?? ''}</p>
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
          aria-invalid={shown.code ? 'true' : undefined}
          onblur={() => touch('code')}
          aria-describedby="loc-code-help loc-code-err"
        />
        <p class="text-muted" id="loc-code-help">
          {canEditCode(editingLocation)
            ? 'Letters, digits and hyphens. Suggested from the name; edit it until a flyer link uses it, then it is permanent.'
            : 'Flyer links use this code, so it can no longer change.'}
        </p>
        <p class="text-error" id="loc-code-err">{shown.code ?? ''}</p>
      </div>
      <div class="field">
        <label for="loc-address">Address</label>
        <input id="loc-address" type="text" bind:value={address} onblur={() => touch('address')} aria-describedby="loc-address-err" />
        <p class="text-error" id="loc-address-err">{shown.address ?? ''}</p>
      </div>
      <div class="field">
        <label for="loc-notes">Notes</label>
        <textarea id="loc-notes" rows="3" bind:value={notes} onblur={() => touch('notes')} aria-describedby="loc-notes-err"></textarea>
        <p class="text-error" id="loc-notes-err">{shown.notes ?? ''}</p>
      </div>
      <div class="field">
        <label><input type="checkbox" bind:checked={active} /> Active</label>
      </div>
      <div class="loc-actions">
        <Button type="submit" variant="primary" disabled={saving}>{saving ? 'Saving…' : 'Save'}</Button>
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
      <table class="loc-table">
        <thead class="loc-head">
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
              <td class="loc-name" data-label="Name">{loc.name}</td>
              <td data-label="Address">{loc.address}</td>
              <td class="mono" data-label="Code">{loc.code}</td>
              <td class="loc-notes" data-label="Notes" title={loc.notes}>{loc.notes}</td>
              <td data-label="Status">{locationStatusLabel(loc)}</td>
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
</div>

<style>
  .fit {
    max-width: 100%;
    min-width: 0;
    overflow-x: hidden;
  }
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
  /*
   * #0590 (J1): under 480px the table becomes one card per location (name,
   * address, code, notes, status, then the actions stacked full width), so
   * nothing is off-screen and the page does not scroll sideways.
   */
  @media (max-width: 480px) {
    .loc-table,
    .loc-table tbody,
    .loc-table tr,
    .loc-table td {
      display: block;
      width: 100%;
      box-sizing: border-box;
    }
    .loc-head {
      position: absolute;
      width: 1px;
      height: 1px;
      overflow: hidden;
      clip: rect(0, 0, 0, 0);
    }
    .loc-table tr {
      border: var(--border-w) solid var(--border);
      border-radius: var(--radius);
      padding: var(--space-2) var(--space-3);
      margin-bottom: var(--space-3);
    }
    .loc-table td {
      border: 0;
      padding: var(--space-1) 0;
      overflow-wrap: anywhere;
    }
    .loc-table td[data-label]::before {
      content: attr(data-label) ': ';
      color: var(--text-muted);
      font-size: var(--fs-sm);
    }
    .loc-table td.loc-name {
      font-weight: 600;
    }
    .loc-notes {
      max-width: none;
      white-space: normal;
    }
    /* td.loc-row-actions, not .loc-row-actions: `.loc-table td` above sets
       display: block and outranks a lone class, which left this flex dead. */
    .loc-table td.loc-row-actions {
      display: flex;
      flex-direction: column;
      align-items: stretch;
      gap: var(--space-2);
      white-space: normal;
    }
    .loc-table td.loc-row-actions > :global(*) {
      width: 100%;
    }
  }
  .loc-status:empty {
    margin: 0;
  }
  .loc-add {
    margin: var(--space-3) 0;
  }
</style>
