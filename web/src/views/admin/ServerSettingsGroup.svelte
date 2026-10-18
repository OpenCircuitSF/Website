<!--
  One group (Media, Email, Amazon SES, Sending or Outreach) of the Server settings area
  (#0566). Owns its own draft, so saving one group never discards edits in
  another, and keeps its status line at the component's top level so the live
  region persists for the life of the group.
-->
<script lang="ts">
  import { untrack } from 'svelte';
  import { updateServerSettings, ApiError } from '../../lib/api';
  import {
    GROUP_LABELS,
    FIELD_LABELS,
    RESTART_NOTE,
    LIVE_NOTE,
    PENDING_NOTE,
    initialDraft,
    buildPatch,
    fieldHint,
    isSaved,
  } from '../../lib/serverSettings';
  import { sourceLabel } from '../../lib/outreachConnections';
  import type { ServerSettingGroup, ServerSettings } from '../../lib/types';
  import Button from '../../lib/Button.svelte';

  let { group, onupdate }: { group: ServerSettingGroup; onupdate: (next: ServerSettings) => void } = $props();

  const label = $derived(GROUP_LABELS[group.name]);

  let draft = $state<Record<string, string>>(untrack(() => initialDraft(group)));
  let saving = $state(false);
  let message = $state('');

  const canSave = $derived(
    !saving &&
      Object.keys(buildPatch(group, draft)).length > 0 &&
      !group.fields.some((f) => fieldHint(f.key, draft[f.key] ?? '') !== null),
  );

  async function submit(values: Record<string, string>, success: string): Promise<void> {
    saving = true;
    message = '';
    try {
      const res = await updateServerSettings(values);
      const next = res.groups.find((g) => g.name === group.name);
      if (next) draft = initialDraft(next);
      onupdate(res);
      message = success;
    } catch (e) {
      message = e instanceof ApiError && e.message ? e.message : 'Could not save. Please try again.';
    } finally {
      saving = false;
    }
  }

  function save(): Promise<void> {
    return submit(buildPatch(group, draft), `${label} saved.`);
  }

  function clearSaved(key: string): Promise<void> {
    return submit({ [key]: '' }, 'Cleared.');
  }
</script>

<fieldset class="srv-group">
  <legend>{label}</legend>
  {#if group.error}
    <p class="text-error">{group.error}</p>
  {/if}
  {#each group.fields as f (f.key)}
    {@const id = `srv-${f.key}`}
    {@const info = FIELD_LABELS[f.key]}
    {@const hint = fieldHint(f.key, draft[f.key] ?? '')}
    <div class="srv-field">
      <label class="srv-name" for={id}>{info.label}</label>
      {#if f.key === 'ses_sandbox'}
        <select {id} bind:value={draft[f.key]} disabled={saving}>
          <option value="">Not saved — using {sourceLabel(f)}: {f.value}</option>
          <option value="true">Sandboxed</option>
          <option value="false">Production access granted</option>
        </select>
      {:else if f.key === 'bluesky_card_url'}
        <select {id} bind:value={draft[f.key]} disabled={saving}>
          <option value="">Not saved — using {sourceLabel(f)}: {f.value}</option>
          <option value="short">Short link (tracked)</option>
          <option value="canonical">Workshop page (opencircuitsf.com)</option>
        </select>
      {:else if f.key === 'send_batch_size'}
        <input {id} type="number" min="1" max="500" bind:value={draft[f.key]} disabled={saving} placeholder={f.source !== 'database' && f.value ? f.value : info.placeholder} />
      {:else}
        <input
          {id}
          type="text"
          bind:value={draft[f.key]}
          disabled={saving}
          placeholder={f.source !== 'database' && f.value ? f.value : info.placeholder}
        />
      {/if}
      <p class="text-muted srv-desc">{sourceLabel(f)}</p>
      <p class="text-muted srv-desc">{info.help}</p>
      <p class="text-muted srv-desc">{f.restart_required ? RESTART_NOTE : LIVE_NOTE}</p>
      {#if f.pending_restart}
        <p class="text-notice srv-desc">{PENDING_NOTE}</p>
      {/if}
      {#if hint}
        <p class="text-error" role="alert">{hint}</p>
      {/if}
      {#if isSaved(f)}
        <Button disabled={saving} onclick={() => clearSaved(f.key)}>Clear saved value</Button>
      {/if}
    </div>
  {/each}
  <div class="srv-actions">
    <Button variant="primary" disabled={!canSave} onclick={save}>
      {saving ? 'Saving…' : `Save ${label}`}
    </Button>
  </div>
  <!-- Unconditionally rendered, empty when there is nothing to say: an
       {#if}-created element with its text already in it is not reliably
       announced. Console-wide decision. -->
  <p class="text-notice" role="status">{message}</p>
</fieldset>

<style>
  .srv-group {
    margin: 0 0 var(--space-4);
    padding: var(--space-3) var(--space-4);
    border: var(--border-w) solid var(--border);
  }
  .srv-group legend {
    font-weight: 600;
    color: var(--text);
    padding: 0 var(--space-2);
  }
  .srv-field {
    margin-top: var(--space-3);
  }
  .srv-name {
    display: block;
    font-weight: 600;
    color: var(--text);
    font-size: var(--fs-base);
  }
  .srv-desc {
    margin: var(--space-1) 0 0;
    font-size: var(--fs-sm);
  }
  .srv-field input,
  .srv-field select {
    margin-top: var(--space-2);
  }
  .srv-field input:focus-visible,
  .srv-field select:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
  .srv-actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin-top: var(--space-3);
  }
  .srv-group .text-notice {
    margin: var(--space-2) 0 0;
  }
</style>
