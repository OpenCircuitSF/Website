<!--
  The admin CRT-session screen (#0393, PRD is silent -- this predates a PRD
  section): the home hero's live CRT screen (#0270, #0274), moved from a
  hard-coded array into rows an admin can toggle, reorder, and edit without a
  deploy. Loads on its own onMount, matching Deliverability.svelte/
  Workshops.svelte's own "Admin.svelte deliberately does not eagerly load
  every subtab" convention.

  Every decision not already covered by lib/admin.ts's shared helpers
  (formatDateTime) is a call into lib/crtCommands.ts -- this file is markup
  and wiring only, per CLAUDE.md's "SPA logic goes in plain TypeScript
  modules ... Svelte components stay thin."

  Rows are edited inline (a "Save" button per row commits its own PATCH)
  rather than through a modal, since every field -- command text, a
  multi-line output textarea, the source select, the active checkbox -- fits
  comfortably in a table row and #0393's Design §4 describes no reason to
  prefer a separate editor surface the way WorkshopEditor.svelte's much
  larger form needs one.

  #0393's Open question 3 ("rate of change"): the public GET /api/crt-session
  endpoint caches for 60s, so a save here can take up to a minute to reach
  the live home page. Said explicitly in the panel copy below rather than
  left for an admin to wonder whether the save took.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { listCrtCommands, createCrtCommand, updateCrtCommand, deleteCrtCommand, ApiError } from '../../lib/api';
  import {
    sortedCrtCommands,
    crtReorderSwap,
    validateNewCrtCommand,
    isValidCrtSlug,
    isValidCrtSource,
    crtOverBudgetWarning,
    CRT_SOURCES,
    CRT_LINE_CHARS,
  } from '../../lib/crtCommands';
  import { formatDateTime } from '../../lib/admin';
  import type { CrtCommand } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import Panel from '../../lib/Panel.svelte';

  let commands = $state<CrtCommand[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  const sorted = $derived(sortedCrtCommands(commands));

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      commands = (await listCrtCommands()).commands;
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : 'Could not load CRT commands. Please try again.';
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load();
  });

  // ── Create ──────────────────────────────────────────────────────────────
  let newSlug = $state('');
  let newCommand = $state('');
  let newOutput = $state('');
  let newSource = $state('static');
  let creating = $state(false);
  let createError = $state<string | null>(null);

  const newSlugInvalid = $derived(newSlug.trim() !== '' && !isValidCrtSlug(newSlug.trim()));
  const newOutputWarning = $derived(crtOverBudgetWarning(newOutput));

  async function submitCreate(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    const result = validateNewCrtCommand(newSlug, newCommand, newOutput);
    if ('error' in result) {
      createError = result.error;
      return;
    }
    creating = true;
    createError = null;
    try {
      // A new command is appended after the current highest sort_order,
      // matching Admin.svelte's own "submitCreateInterest" convention.
      const sortOrder = commands.length === 0 ? 0 : Math.max(...commands.map((c) => c.sort_order)) + 10;
      const created = await createCrtCommand(result.slug, result.command, result.output, newSource, sortOrder);
      commands = [...commands, created];
      newSlug = '';
      newCommand = '';
      newOutput = '';
      newSource = 'static';
    } catch (err) {
      createError =
        err instanceof ApiError
          ? err.message
          : 'Could not create the command. Please try again.';
    } finally {
      creating = false;
    }
  }

  // ── Inline edit (per row) ──────────────────────────────────────────────
  // Keyed by command id: the draft field values an admin is currently
  // editing for that row, only present while a row's own editor is open.
  let editing = $state<Record<number, { command: string; output: string; source: string; active: boolean }>>({});
  let savingId = $state<number | null>(null);
  let saveError = $state<Record<number, string>>({});

  function openEdit(c: CrtCommand): void {
    editing = { ...editing, [c.id]: { command: c.command, output: c.output, source: c.source, active: c.active } };
  }

  function closeEdit(id: number): void {
    const next = { ...editing };
    delete next[id];
    editing = next;
  }

  async function saveEdit(id: number): Promise<void> {
    const draft = editing[id];
    if (!draft) return;
    if (draft.command.trim() === '') {
      saveError = { ...saveError, [id]: 'Command cannot be empty.' };
      return;
    }
    if (draft.output.trim() === '') {
      saveError = { ...saveError, [id]: 'Output cannot be empty.' };
      return;
    }
    if (!isValidCrtSource(draft.source)) {
      saveError = { ...saveError, [id]: 'Choose a valid source.' };
      return;
    }
    savingId = id;
    saveError = { ...saveError, [id]: '' };
    try {
      const updated = await updateCrtCommand(id, {
        command: draft.command,
        output: draft.output,
        source: draft.source,
        active: draft.active,
      });
      commands = commands.map((c) => (c.id === updated.id ? updated : c));
      closeEdit(id);
    } catch (err) {
      saveError = {
        ...saveError,
        [id]: err instanceof ApiError ? err.message : 'Could not save the command. Please try again.',
      };
    } finally {
      savingId = null;
    }
  }

  // ── Active toggle (no separate editor needed) ──────────────────────────
  let togglingId = $state<number | null>(null);

  async function toggleActive(c: CrtCommand): Promise<void> {
    togglingId = c.id;
    saveError = { ...saveError, [c.id]: '' };
    try {
      const updated = await updateCrtCommand(c.id, { active: !c.active });
      commands = commands.map((it) => (it.id === updated.id ? updated : it));
    } catch (err) {
      saveError = {
        ...saveError,
        [c.id]: err instanceof ApiError ? err.message : 'Could not toggle active. Please try again.',
      };
    } finally {
      togglingId = null;
    }
  }

  // ── Reorder ─────────────────────────────────────────────────────────────
  let reorderingId = $state<number | null>(null);
  let reorderError = $state<string | null>(null);

  async function move(c: CrtCommand, direction: 'up' | 'down'): Promise<void> {
    const swap = crtReorderSwap(sorted, c.id, direction);
    if (!swap) return;
    reorderingId = c.id;
    reorderError = null;
    try {
      const [movedRes, otherRes] = await Promise.all([
        updateCrtCommand(swap.moved.id, { sort_order: swap.moved.sortOrder }),
        updateCrtCommand(swap.other.id, { sort_order: swap.other.sortOrder }),
      ]);
      commands = commands.map((it) => {
        if (it.id === movedRes.id) return movedRes;
        if (it.id === otherRes.id) return otherRes;
        return it;
      });
    } catch (err) {
      reorderError = err instanceof ApiError ? err.message : 'Could not reorder commands. Please try again.';
    } finally {
      reorderingId = null;
    }
  }

  // ── Delete ──────────────────────────────────────────────────────────────
  let deletingId = $state<number | null>(null);
  let deleteRowError = $state<Record<number, string>>({});

  async function handleDelete(c: CrtCommand): Promise<void> {
    deletingId = c.id;
    deleteRowError = { ...deleteRowError, [c.id]: '' };
    try {
      await deleteCrtCommand(c.id);
      commands = commands.filter((it) => it.id !== c.id);
      closeEdit(c.id);
    } catch (err) {
      deleteRowError = {
        ...deleteRowError,
        [c.id]: err instanceof ApiError ? err.message : 'Could not delete the command. Please try again.',
      };
    } finally {
      deletingId = null;
    }
  }
</script>

<Panel title="Add a command">
  <form class="crt-create-form" onsubmit={submitCreate}>
    <div class="field">
      <label for="new-crt-slug">Slug</label>
      <input
        id="new-crt-slug"
        type="text"
        bind:value={newSlug}
        disabled={creating}
        placeholder="fortune"
        oninput={() => (createError = null)}
      />
      <p class="text-warn" role="status">
        {newSlugInvalid
          ? 'Lowercase letters, numbers, and single hyphens only (e.g. "workshops-next").'
          : ''}
      </p>
    </div>
    <div class="field">
      <label for="new-crt-command">Command text</label>
      <input
        id="new-crt-command"
        type="text"
        bind:value={newCommand}
        disabled={creating}
        placeholder="fortune"
        oninput={() => (createError = null)}
      />
    </div>
    <div class="field">
      <label for="new-crt-output">Output (one line per row)</label>
      <textarea
        id="new-crt-output"
        rows="3"
        bind:value={newOutput}
        disabled={creating}
        oninput={() => (createError = null)}
      ></textarea>
      <p class="text-muted crt-width-hint">
        The CRT glass fits about {CRT_LINE_CHARS} characters per line; longer lines are truncated there.
      </p>
      <p class="text-warn" role="status">{newOutputWarning ?? ''}</p>
    </div>
    <div class="field">
      <label for="new-crt-source">Source</label>
      <select id="new-crt-source" bind:value={newSource} disabled={creating}>
        {#each CRT_SOURCES as s (s.value)}
          <option value={s.value}>{s.label}</option>
        {/each}
      </select>
    </div>
    {#if createError}
      <p class="text-error" role="alert">{createError}</p>
    {/if}
    <Button type="submit" variant="primary" disabled={creating}>
      {creating ? 'Adding…' : 'Add command'}
    </Button>
  </form>
</Panel>

<Panel title="CRT session" noPadding={sorted.length > 0 && !loading && !loadError}>
  <p class="text-muted crt-cache-note">
    The public screen caches for up to 60 seconds — a save here may take a minute to appear on the home page.
  </p>
  {#if loading}
    <p class="text-muted" role="status">Loading CRT commands…</p>
  {:else if loadError}
    <p class="text-error" role="alert">{loadError}</p>
    <Button variant="primary" onclick={load}>Retry</Button>
  {:else if sorted.length === 0}
    <p class="text-muted">No commands yet.</p>
  {:else}
    {#if reorderError}
      <p class="text-error" role="alert">{reorderError}</p>
    {/if}
    <div class="table-scroll">
      <table>
        <thead>
          <tr>
            <th class="actions-col">Order</th>
            <th>Active</th>
            <th>Slug</th>
            <th>Command</th>
            <th>Source</th>
            <th>Updated</th>
            <th class="actions-col">Actions</th>
          </tr>
        </thead>
        <tbody>
          {#each sorted as c, i (c.id)}
            <tr>
              <td class="actions-col">
                <button
                  type="button"
                  class="order-btn"
                  disabled={i === 0 || reorderingId !== null}
                  onclick={() => move(c, 'up')}
                  aria-label={`Move ${c.slug} up`}
                >↑</button>
                <button
                  type="button"
                  class="order-btn"
                  disabled={i === sorted.length - 1 || reorderingId !== null}
                  onclick={() => move(c, 'down')}
                  aria-label={`Move ${c.slug} down`}
                >↓</button>
              </td>
              <td>
                <input
                  type="checkbox"
                  checked={c.active}
                  disabled={togglingId === c.id}
                  onchange={() => toggleActive(c)}
                  aria-label={`Toggle ${c.slug} active`}
                />
              </td>
              <td class="mono">{c.slug}</td>
              <td>{c.command}</td>
              <td>
                <span class="badge">{c.source}</span>
              </td>
              <td>{c.updated_at ? formatDateTime(c.updated_at) : '—'}</td>
              <td class="actions-col">
                {#if editing[c.id]}
                  <Button
                    variant="primary"
                    disabled={savingId === c.id}
                    onclick={() => saveEdit(c.id)}
                  >
                    {savingId === c.id ? 'Saving…' : 'Save'}
                  </Button>
                  <Button onclick={() => closeEdit(c.id)} disabled={savingId === c.id}>Cancel</Button>
                {:else}
                  <Button onclick={() => openEdit(c)}>Edit</Button>
                  <Button variant="danger" disabled={deletingId === c.id} onclick={() => handleDelete(c)}>
                    {deletingId === c.id ? 'Deleting…' : 'Delete'}
                  </Button>
                {/if}
              </td>
            </tr>
            {#if editing[c.id]}
              {@const draft = editing[c.id]}
              <tr>
                <td colspan="7">
                  <div class="crt-edit-row">
                    <div class="field">
                      <label for={`crt-edit-command-${c.id}`}>Command text</label>
                      <input
                        id={`crt-edit-command-${c.id}`}
                        type="text"
                        bind:value={draft.command}
                        disabled={savingId === c.id}
                      />
                    </div>
                    <div class="field">
                      <label for={`crt-edit-output-${c.id}`}>Output (one line per row)</label>
                      <textarea
                        id={`crt-edit-output-${c.id}`}
                        rows="4"
                        bind:value={draft.output}
                        disabled={savingId === c.id}
                      ></textarea>
                      <p class="text-muted crt-width-hint">
                        The CRT glass fits about {CRT_LINE_CHARS} characters per line; longer lines are truncated there.
                      </p>
                      <!-- No role="status": this row's editor sits inside
                           {#if editing[c.id]}, and liveRegionGuard.
                           structuralGuard.test.ts's persistence rule (#0063)
                           requires either a swap target or an allowlist
                           entry for any in-branch live region. A per-
                           keystroke width hint isn't the kind of state
                           change that rule is protecting -- the create
                           form's equivalent hint (above) IS role="status"
                           because it sits in the always-mounted "Add a
                           command" panel, not behind a branch. -->
                      <p class="text-warn">{crtOverBudgetWarning(draft.output) ?? ''}</p>
                    </div>
                    <div class="field">
                      <label for={`crt-edit-source-${c.id}`}>Source</label>
                      <select id={`crt-edit-source-${c.id}`} bind:value={draft.source} disabled={savingId === c.id}>
                        {#each CRT_SOURCES as s (s.value)}
                          <option value={s.value}>{s.label}</option>
                        {/each}
                      </select>
                      {#if draft.source !== 'static'}
                        <p class="text-muted crt-fallback-note">
                          The output above is this row's fallback — shown only if the live fetch fails.
                        </p>
                      {/if}
                    </div>
                    <div class="field">
                      <label>
                        <input type="checkbox" bind:checked={draft.active} disabled={savingId === c.id} />
                        Active
                      </label>
                    </div>
                  </div>
                </td>
              </tr>
            {/if}
            {#if deleteRowError[c.id] || saveError[c.id]}
              <tr>
                <td colspan="7">
                  <p class="text-error" role="alert">{deleteRowError[c.id] || saveError[c.id]}</p>
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
  .crt-create-form,
  .crt-edit-row {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }
  .crt-cache-note {
    padding: var(--space-3) var(--space-3) 0;
  }
  .crt-fallback-note,
  .crt-width-hint {
    margin-top: var(--space-1);
  }
</style>
