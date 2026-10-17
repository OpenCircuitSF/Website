<!--
  One group (ShortLinks, Bluesky or Mastodon) of the Outreach connections
  area (#0564). Owns its own draft, so saving one group never discards edits
  in another, and keeps its status line at the component's top level so the
  live region persists for the life of the group.

  Secrets are write-only: the server never sends one, so a secret input is
  always empty on load and after every save, and the status line says only
  whether it is set and where it comes from.
-->
<script lang="ts">
  import { untrack } from 'svelte';
  import { updateOutreachConnections, testOutreachConnection, ApiError } from '../../lib/api';
  import {
    GROUP_LABELS,
    FIELD_LABELS,
    sourceLabel,
    secretStatus,
    initialDraft,
    buildPatch,
    validateServiceUrl,
  } from '../../lib/outreachConnections';
  import type { ConnectionGroup, OutreachConnections } from '../../lib/types';
  import Button from '../../lib/Button.svelte';

  let { group, onupdate }: { group: ConnectionGroup; onupdate: (next: OutreachConnections) => void } = $props();

  const URL_KEYS = new Set(['shortlinks_base_url', 'bluesky_pds_url', 'mastodon_base_url']);
  const label = $derived(GROUP_LABELS[group.name]);

  let draft = $state<Record<string, string>>(untrack(() => initialDraft(group)));
  let saving = $state(false);
  let testing = $state(false);
  let message = $state('');

  function urlHint(key: string): string | null {
    return URL_KEYS.has(key) ? validateServiceUrl(draft[key] ?? '') : null;
  }

  const canSave = $derived(
    !saving &&
      Object.keys(buildPatch(group, draft)).length > 0 &&
      !group.fields.some((f) => !f.secret && urlHint(f.key) !== null),
  );

  async function submit(values: Record<string, string>, success: string): Promise<void> {
    saving = true;
    message = '';
    try {
      const res = await updateOutreachConnections(values);
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

  async function runTest(): Promise<void> {
    testing = true;
    message = '';
    try {
      const res = await testOutreachConnection(group.name);
      message = res.ok ? (res.detail ?? 'Connected.') : (res.error ?? 'The connection check failed.');
    } catch {
      message = 'Could not run the connection check. Please try again.';
    } finally {
      testing = false;
    }
  }
</script>

<fieldset class="conn-group">
  <legend>{label}</legend>
  <p class="text-muted conn-status">{group.configured ? 'Configured' : 'Not configured'}</p>
  {#if group.error}
    <p class="text-error">{group.error}</p>
  {/if}
  {#each group.fields as f (f.key)}
    {@const id = `conn-${f.key}`}
    <div class="conn-field">
      <label class="conn-name" for={id}>{FIELD_LABELS[f.key].label}</label>
      {#if f.secret}
        <input {id} type="password" autocomplete="new-password" bind:value={draft[f.key]} disabled={saving} />
        <p class="text-muted conn-desc">{secretStatus(f)}</p>
        {#if f.source === 'database'}
          <Button disabled={saving} onclick={() => clearSaved(f.key)}>Clear saved value</Button>
        {/if}
      {:else}
        <input
          {id}
          type={f.key === 'bluesky_handle' ? 'text' : 'url'}
          bind:value={draft[f.key]}
          disabled={saving}
          placeholder={f.source !== 'database' && f.value ? f.value : FIELD_LABELS[f.key].placeholder}
        />
        <p class="text-muted conn-desc">{sourceLabel(f)}</p>
        {#if urlHint(f.key)}
          <p class="text-error" role="alert">{urlHint(f.key)}</p>
        {/if}
      {/if}
    </div>
  {/each}
  <div class="conn-actions">
    <Button variant="primary" disabled={!canSave} onclick={save}>
      {saving ? 'Saving…' : `Save ${label}`}
    </Button>
    <Button disabled={testing} onclick={runTest}>
      {testing ? 'Testing…' : 'Test connection'}
    </Button>
  </div>
  <!-- Unconditionally rendered, empty when there is nothing to say: an
       {#if}-created element with its text already in it is not reliably
       announced. Console-wide decision. -->
  <p class="text-notice" role="status">{message}</p>
</fieldset>

<style>
  .conn-group {
    margin: 0 0 var(--space-4);
    padding: var(--space-3) var(--space-4);
    border: var(--border-w) solid var(--border);
  }
  .conn-group legend {
    font-weight: 600;
    color: var(--text);
    padding: 0 var(--space-2);
  }
  .conn-status {
    margin: 0 0 var(--space-2);
    font-size: var(--fs-sm);
  }
  .conn-field {
    margin-top: var(--space-3);
  }
  .conn-name {
    display: block;
    font-weight: 600;
    color: var(--text);
    font-size: var(--fs-base);
  }
  .conn-desc {
    margin: var(--space-1) 0 0;
    font-size: var(--fs-sm);
  }
  .conn-field input {
    margin-top: var(--space-2);
  }
  .conn-field input:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
  .conn-actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin-top: var(--space-3);
  }
  .conn-group .text-notice {
    margin: var(--space-2) 0 0;
  }
</style>
