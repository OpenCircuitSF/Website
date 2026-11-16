<!--
  API tokens (#0652): the Settings section that lists, creates and revokes the
  bearer tokens another system (Home Assistant, an MCP client) uses to call
  /api/v1/. #0674 added an expiry choice (90 days by default), scopes picked
  by resource with an Agent preset, and the Expires column. The secret is
  shown once, right after create, and cleared from state on Done.
  Desktop-only like every admin page.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { listApiTokens, createApiToken, revokeApiToken, ApiError } from '../../lib/api';
  import {
    AGENT_PRESET,
    DEFAULT_EXPIRY,
    EXPIRY_OPTIONS,
    SCOPE_GROUPS,
    expiresLabel,
    expiryFromChoice,
    isExpired,
    orderScopes,
    scopeLabel,
    validateTokenName,
    lastUsedLabel,
    tokenStatusLabel,
    type ExpiryChoice,
  } from '../../lib/apiTokens';
  import { formatDateTime } from '../../lib/admin';
  import type { ApiToken } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import Panel from '../../lib/Panel.svelte';

  let tokens = $state<ApiToken[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let unavailable = $state(false);

  let name = $state('');
  let expiry = $state<ExpiryChoice>(DEFAULT_EXPIRY);
  let customDate = $state('');
  let scopes = $state<string[]>([]);
  let creating = $state(false);
  let createError = $state<string | null>(null);
  let secret = $state<string | null>(null);
  let copied = $state(false);
  let revokeError = $state<string | null>(null);

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    unavailable = false;
    try {
      tokens = (await listApiTokens()).tokens;
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) unavailable = true;
      else loadError = 'Could not load API tokens. Please try again.';
    } finally {
      loading = false;
    }
  }

  async function create(e: Event): Promise<void> {
    e.preventDefault();
    createError = null;
    const v = validateTokenName(name);
    if ('error' in v) {
      createError = v.error;
      return;
    }
    const chosen = orderScopes(scopes);
    if (chosen.length === 0) {
      createError = 'Choose at least one scope.';
      return;
    }
    const exp = expiryFromChoice(expiry, customDate, new Date());
    if ('error' in exp) {
      createError = exp.error;
      return;
    }
    creating = true;
    try {
      const res = await createApiToken(v.name, chosen, exp.expiresAt);
      secret = res.secret;
      copied = false;
      name = '';
      scopes = [];
      expiry = DEFAULT_EXPIRY;
      customDate = '';
      await load();
    } catch (err) {
      createError = err instanceof Error && err.message ? err.message : 'Could not create the token. Please try again.';
    } finally {
      creating = false;
    }
  }

  async function copy(): Promise<void> {
    if (secret === null) return;
    try {
      await navigator.clipboard.writeText(secret);
      copied = true;
    } catch {
      copied = false;
    }
  }

  function applyAgentPreset(): void {
    scopes = [...AGENT_PRESET];
  }

  function done(): void {
    secret = null;
    copied = false;
  }

  async function revoke(t: ApiToken): Promise<void> {
    if (!confirm(`Revoke "${t.name}"? Anything using it will stop working on its next request.`)) return;
    revokeError = null;
    try {
      await revokeApiToken(t.id);
      await load();
    } catch {
      revokeError = 'Could not revoke the token. Please try again.';
    }
  }

  onMount(load);
</script>

<Panel title="API tokens">
  <p class="text-muted tok-intro">
    Tokens let another system, such as Home Assistant or Claude Code, use this site's API. A token can do only what its
    scopes allow, stops working when it expires, and never signs in to the admin console.
  </p>
  {#if loading}
    <p class="text-muted">Loading API tokens…</p>
  {:else if unavailable}
    <p class="text-muted">API tokens are not available when the server runs without a database.</p>
  {:else if loadError}
    <p class="text-error" role="alert">{loadError}</p>
    <Button variant="primary" onclick={load}>Retry</Button>
  {:else}
    {#if secret !== null}
      <div class="tok-secret">
        <p><strong>Copy this token now. It will not be shown again.</strong></p>
        <p><code class="mono">{secret}</code></p>
        <div class="tok-secret-actions">
          <Button onclick={copy}>{copied ? 'Copied' : 'Copy'}</Button>
          <Button variant="primary" onclick={done}>Done</Button>
        </div>
      </div>
    {/if}

    <form class="tok-form" onsubmit={create}>
      <div class="field">
        <label for="api-token-name">Name</label>
        <input id="api-token-name" type="text" bind:value={name} placeholder="Home Assistant" />
      </div>
      <div class="field">
        <label for="api-token-expiry">Expires</label>
        <select id="api-token-expiry" bind:value={expiry}>
          {#each EXPIRY_OPTIONS as o (o.value)}
            <option value={o.value}>{o.label}</option>
          {/each}
        </select>
      </div>
      {#if expiry === 'custom'}
        <div class="field">
          <label for="api-token-expiry-date">Expiry date</label>
          <input id="api-token-expiry-date" type="date" bind:value={customDate} />
        </div>
      {/if}
      <fieldset class="tok-scopes">
        <legend>Scopes</legend>
        <Button onclick={applyAgentPreset}>Agent (all guide and event scopes)</Button>
        {#each SCOPE_GROUPS as g (g.resource)}
          <div class="tok-scope-group" role="group" aria-label={g.resource}>
            <span class="tok-scope-resource">{g.resource}</span>
            {#each g.scopes as s (s.scope)}
              <label class="tok-scope">
                <input type="checkbox" value={s.scope} bind:group={scopes} />
                {s.label} <code class="mono">{s.scope}</code>
              </label>
            {/each}
          </div>
        {/each}
      </fieldset>
      {#if createError}
        <p class="text-error" role="alert">{createError}</p>
      {/if}
      <Button variant="primary" type="submit" disabled={creating}>{creating ? 'Creating…' : 'Create token'}</Button>
    </form>

    {#if revokeError}
      <p class="text-error" role="alert">{revokeError}</p>
    {/if}

    {#if tokens.length === 0}
      <p class="text-muted">No API tokens yet.</p>
    {:else}
      <div class="table-scroll">
        <table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Access</th>
              <th>Prefix</th>
              <th>Created</th>
              <th>Last used</th>
              <th>Expires</th>
              <th>Status</th>
              <th class="actions-col"></th>
            </tr>
          </thead>
          <tbody>
            {#each tokens as t (t.id)}
              <tr>
                <td>{t.name}</td>
                <td>{t.scopes.map(scopeLabel).join(', ')}</td>
                <td><code class="mono">{`${t.prefix}…`}</code></td>
                <td>{formatDateTime(t.created_at)}</td>
                <td>{lastUsedLabel(t)}</td>
                <td class:text-error={isExpired(t)}>{expiresLabel(t)}</td>
                <td>{tokenStatusLabel(t)}</td>
                <td class="actions-col">
                  {#if t.revoked_at === null}
                    <Button variant="danger" onclick={() => revoke(t)}>Revoke</Button>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {/if}
</Panel>

<style>
  .tok-intro {
    margin: 0 0 var(--space-3);
  }
  .tok-secret {
    border: 1px solid var(--accent);
    padding: var(--space-3);
    margin: 0 0 var(--space-3);
  }
  .tok-secret code {
    word-break: break-all;
  }
  .tok-secret-actions {
    display: flex;
    gap: var(--space-2);
  }
  .tok-form {
    margin: 0 0 var(--space-3);
  }
  .tok-scopes {
    border: 1px solid var(--border);
    padding: var(--space-2) var(--space-3);
    margin: 0 0 var(--space-3);
  }
  .tok-scope-group {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2) var(--space-3);
    align-items: baseline;
    margin-top: var(--space-2);
  }
  .tok-scope-resource {
    min-width: 8rem;
    font-weight: 600;
  }
</style>
