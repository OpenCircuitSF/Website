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
  import ApplyBadge from '../../lib/ApplyBadge.svelte';

  let { group, onupdate }: { group: ConnectionGroup; onupdate: (next: OutreachConnections) => void } = $props();

  const URL_KEYS = new Set(['shortlinks_base_url', 'bluesky_pds_url', 'mastodon_base_url']);
  const label = $derived(GROUP_LABELS[group.name]);

  let draft = $state<Record<string, string>>(untrack(() => initialDraft(group)));
  let saving = $state(false);
  let testing = $state(false);
  // Saved / cleared confirmations and successful tests go to the role=status
  // line; failures go to the role=alert line (#0584), so colour is never the
  // only signal. `lastTest` feeds the Configured line.
  let message = $state('');
  let failure = $state('');
  let lastTest = $state<'ok' | 'failed' | null>(null);

  // Test connection checks the SAVED values, never the form, so it is only
  // offered when there is nothing unsaved.
  const dirty = $derived(Object.keys(buildPatch(group, draft)).length > 0);

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
    failure = '';
    try {
      const res = await updateOutreachConnections(values);
      const next = res.groups.find((g) => g.name === group.name);
      if (next) draft = initialDraft(next);
      onupdate(res);
      message = success;
      lastTest = null;
    } catch (e) {
      failure = e instanceof ApiError && e.message ? e.message : 'Could not save. Please try again.';
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
    failure = '';
    try {
      const res = await testOutreachConnection(group.name);
      if (res.ok) {
        lastTest = 'ok';
        message = `Tested the saved credentials: ${res.detail ?? 'Connected.'}`;
      } else {
        lastTest = 'failed';
        failure = `Could not connect: ${res.error ?? 'The connection check failed.'}`;
      }
    } catch {
      lastTest = null;
      failure = 'Could not run the connection check. Please try again.';
    } finally {
      testing = false;
    }
  }
</script>

<fieldset class="conn-group">
  <legend>{label}</legend>
  <p class="conn-badge"><ApplyBadge kind="immediate" /></p>
  <p class="text-muted conn-status">
    {group.configured ? 'Configured' : 'Not configured'}{lastTest === 'failed'
      ? ' · last test failed'
      : lastTest === 'ok'
        ? ' · last test passed'
        : ''}
  </p>
  {#if group.error}
    <p class="text-error">{group.error}</p>
  {/if}
  {#each group.fields as f (f.key)}
    {@const id = `conn-${f.key}`}
    <div class="conn-field">
      <div class="conn-label-row">
        <label class="conn-name" for={id}>{FIELD_LABELS[f.key].label}</label>
        {#if f.secret && f.set}
          <span class="conn-saved">{f.source === 'database' ? 'Saved' : 'From environment'}</span>
        {/if}
      </div>
      {#if f.secret}
        <input
          {id}
          type="password"
          autocomplete="new-password"
          bind:value={draft[f.key]}
          disabled={saving}
          placeholder={f.set ? '••••••••' : ''}
        />
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
        {#if f.key === 'bluesky_handle'}
          <p class="text-muted conn-desc">Your Bluesky handle (such as www.opencircuitsf.com) or the email you sign in with. Test connection shows the handle Bluesky reports.</p>
        {/if}
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
    <Button disabled={testing || saving || dirty} onclick={runTest}>
      {testing ? 'Testing…' : dirty ? 'Save, then test' : 'Test connection'}
    </Button>
  </div>
  <!-- Unconditionally rendered, empty when there is nothing to say: an
       {#if}-created element with its text already in it is not reliably
       announced. Console-wide decision. -->
  <p class="text-notice" role="status">{message}</p>
  <p class="text-error conn-alert" role="alert">{failure}</p>
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
  .conn-label-row {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }
  .conn-badge {
    margin: 0 0 var(--space-1);
  }
  .conn-saved {
    display: inline-block;
    padding: 0 var(--space-2);
    border: var(--border-w) solid var(--border-strong);
    border-radius: var(--radius);
    background: var(--bg-subtle);
    color: var(--text);
    font-size: var(--fs-sm);
    font-weight: 600;
  }
  .conn-alert:empty {
    margin: 0;
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
