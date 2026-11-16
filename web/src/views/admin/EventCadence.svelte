<!--
  Settings → Event cadence (#0678): what a normal month of events looks like,
  for the Events page's "Next month" panel and v1's event_coverage. Add, edit,
  retire (inactive) or delete a rule. Decisions live in lib/eventCadence.ts.
  Loads on its own onMount.
-->
<script lang="ts">
  import { onMount, tick } from 'svelte';
  import {
    listCadenceRules,
    createCadenceRule,
    updateCadenceRule,
    deleteCadenceRule,
    listEventTypes,
    listVenues,
    ApiError,
  } from '../../lib/api';
  import {
    WEEKDAYS,
    weekdayLabel,
    emptyRuleForm,
    formFromRule,
    describeRule,
    describeMatch,
    ruleFormErrors,
    firstRuleFormError,
    toRulePayload,
    type CadenceRuleForm,
  } from '../../lib/eventCadence';
  import type { CadenceRule, EventType, Venue } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import Panel from '../../lib/Panel.svelte';

  let rules = $state<CadenceRule[]>([]);
  let types = $state<EventType[]>([]);
  let venueList = $state<Venue[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let busy = $state(false);

  let form = $state<CadenceRuleForm>(emptyRuleForm());
  let editingId = $state<number | null>(null);
  let formError = $state<string | null>(null);

  let deleting = $state<CadenceRule | null>(null);
  let deleteError = $state<string | null>(null);
  let deleteModalEl = $state<HTMLDivElement | null>(null);

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
      const [r, t, v] = await Promise.all([listCadenceRules(), listEventTypes(), listVenues()]);
      rules = r.rules;
      types = t.event_types;
      venueList = v.venues;
    } catch (err) {
      loadError = messageFor(err, 'Could not load the cadence rules.');
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load();
  });

  function startEdit(rule: CadenceRule): void {
    editingId = rule.id;
    form = formFromRule(rule);
    formError = null;
  }

  function resetForm(): void {
    editingId = null;
    form = emptyRuleForm();
    formError = null;
  }

  function onTypeChange(e: Event): void {
    const v = (e.currentTarget as HTMLSelectElement).value;
    form.eventTypeId = v === '' ? null : Number(v);
  }

  function onVenueChange(e: Event): void {
    const v = (e.currentTarget as HTMLSelectElement).value;
    form.venueId = v === '' ? null : Number(v);
  }

  async function submit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    const problem = firstRuleFormError(ruleFormErrors(form));
    if (problem) {
      formError = problem;
      return;
    }
    busy = true;
    formError = null;
    try {
      const payload = toRulePayload(form);
      if (editingId === null) {
        const created = await createCadenceRule(payload);
        rules = [...rules, created];
      } else {
        const updated = await updateCadenceRule(editingId, payload);
        rules = rules.map((r) => (r.id === updated.id ? updated : r));
      }
      resetForm();
    } catch (err) {
      formError = messageFor(err, 'Could not save this rule.');
    } finally {
      busy = false;
    }
  }

  function openDelete(rule: CadenceRule): void {
    deleting = rule;
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
      await deleteCadenceRule(id);
      rules = rules.filter((r) => r.id !== id);
      if (editingId === id) resetForm();
      deleting = null;
    } catch (err) {
      deleteError = messageFor(err, 'Could not delete this rule.');
    } finally {
      busy = false;
    }
  }
</script>

<Panel title="Event cadence">
  <p class="text-muted">
    What a normal month looks like. The Events page's “Next month” panel checks next month against these rules. Dates
    are Pacific time.
  </p>
  {#if loading}
    <p class="text-muted" role="status">Loading cadence rules…</p>
  {:else if loadError}
    <p class="text-error" role="alert">{loadError}</p>
    <Button variant="primary" onclick={load}>Retry</Button>
  {:else}
    {#if rules.length === 0}
      <p class="text-muted">No cadence rules yet.</p>
    {:else}
      <div class="table-scroll">
        <table>
          <thead>
            <tr>
              <th>Name</th>
              <th>How often</th>
              <th>Matches</th>
              <th>Venue</th>
              <th>Status</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {#each rules as rule (rule.id)}
              <tr>
                <td>{rule.name}</td>
                <td>{describeRule(rule)}</td>
                <td>{describeMatch(rule)}</td>
                <td>{rule.venue ? rule.venue.name : 'Any'}</td>
                <td>
                  {#if rule.active}
                    <span class="badge badge-success">Active</span>
                  {:else}
                    <span class="badge badge-muted">Retired</span>
                  {/if}
                </td>
                <td>
                  <Button aria-label="Edit {rule.name}" disabled={busy} onclick={() => startEdit(rule)}>Edit</Button>
                  <Button aria-label="Delete {rule.name}" disabled={busy} onclick={() => openDelete(rule)}
                    >Delete</Button
                  >
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}

    <form class="rule-form" onsubmit={submit}>
      <h3 class="form-title">{editingId === null ? 'Add a rule' : 'Edit rule'}</h3>
      <div class="field">
        <label for="cadence-name">Name</label>
        <input id="cadence-name" type="text" bind:value={form.name} disabled={busy} />
      </div>
      <div class="field">
        <label for="cadence-type">Event type</label>
        <select id="cadence-type" value={form.eventTypeId ?? ''} onchange={onTypeChange} disabled={busy}>
          <option value="">Any type</option>
          {#each types as t (t.id)}
            <option value={t.id}>{t.name}</option>
          {/each}
        </select>
      </div>
      <div class="field">
        <label for="cadence-prefix">Title starts with</label>
        <input id="cadence-prefix" type="text" bind:value={form.titlePrefix} disabled={busy} />
      </div>
      <p class="text-muted">Set an event type, a title prefix, or both. Both must match when both are set.</p>
      <fieldset class="field">
        <legend>How often</legend>
        <label class="radio-row">
          <input type="radio" name="cadence-frequency" value="weekly" bind:group={form.frequency} disabled={busy} />
          Weekly
        </label>
        <label class="radio-row">
          <input type="radio" name="cadence-frequency" value="monthly" bind:group={form.frequency} disabled={busy} />
          A number of times a month
        </label>
      </fieldset>
      {#if form.frequency === 'weekly'}
        <div class="field">
          <label for="cadence-weekday">Weekday</label>
          <select id="cadence-weekday" bind:value={form.weekday} disabled={busy}>
            <option value="">Choose a day</option>
            {#each WEEKDAYS as d (d)}
              <option value={d}>{weekdayLabel(d)}</option>
            {/each}
          </select>
        </div>
      {:else}
        <div class="field">
          <label for="cadence-per-month">Events a month</label>
          <input id="cadence-per-month" type="text" inputmode="numeric" bind:value={form.perMonth} disabled={busy} />
        </div>
      {/if}
      <div class="field">
        <label for="cadence-venue">Venue</label>
        <select id="cadence-venue" value={form.venueId ?? ''} onchange={onVenueChange} disabled={busy}>
          <option value="">Any venue</option>
          {#each venueList as v (v.id)}
            <option value={v.id}>{v.name}</option>
          {/each}
        </select>
      </div>
      <label class="checkbox-row">
        <input type="checkbox" bind:checked={form.active} disabled={busy} />
        Active
      </label>
      {#if formError}
        <p class="text-error" role="alert">{formError}</p>
      {/if}
      <div class="row">
        <Button type="submit" variant="primary" disabled={busy}>{editingId === null ? 'Add rule' : 'Save rule'}</Button>
        {#if editingId !== null}
          <Button disabled={busy} onclick={resetForm}>Cancel</Button>
        {/if}
      </div>
    </form>
  {/if}
</Panel>

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
      aria-label="Delete cadence rule"
      tabindex="-1"
      bind:this={deleteModalEl}
      onclick={(e) => e.stopPropagation()}
      onkeydown={(e) => {
        if (e.key === 'Escape') closeDelete();
      }}
    >
      <h2 class="modal-title">Delete {deleting.name}?</h2>
      <p>This permanently removes the rule. To stop checking it but keep it, edit it and clear Active instead.</p>
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
  .rule-form {
    margin-top: var(--space-4);
  }
  .form-title {
    font-size: var(--fs-md);
    font-weight: 600;
    margin: 0 0 var(--space-3);
  }
  fieldset.field {
    border: none;
    padding: 0;
    margin: 0 0 var(--space-3);
  }
  .radio-row,
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
