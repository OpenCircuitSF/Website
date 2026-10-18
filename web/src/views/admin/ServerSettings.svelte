<!--
  Server settings: operational values (media directory, email identity, SES
  flags, send batch size) saved in the database from admin Settings (#0566).
  Rendered by Admin.svelte as its own panel under the Settings section. Each
  group lives in ServerSettingsGroup.svelte; this component loads the state
  and shows the precedence note.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { getServerSettings, ApiError } from '../../lib/api';
  import { PRECEDENCE_NOTE } from '../../lib/serverSettings';
  import type { ServerSettings } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import Panel from '../../lib/Panel.svelte';
  import ServerSettingsGroup from './ServerSettingsGroup.svelte';

  let settings = $state<ServerSettings | null>(null);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let unavailable = $state(false);

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    unavailable = false;
    try {
      settings = await getServerSettings();
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) unavailable = true;
      else loadError = 'Could not load server settings. Please try again.';
    } finally {
      loading = false;
    }
  }

  onMount(load);
</script>

<Panel title="Server settings">
  {#if loading}
    <p class="text-muted">Loading server settings…</p>
  {:else if unavailable}
    <p class="text-muted">Server settings are not available when the server runs without a database.</p>
  {:else if loadError}
    <p class="text-error" role="alert">{loadError}</p>
    <Button variant="primary" onclick={load}>Retry</Button>
  {:else if settings}
    <p class="text-muted srv-note">{PRECEDENCE_NOTE}</p>
    {#each settings.groups as g (g.name)}
      <ServerSettingsGroup group={g} onupdate={(next) => (settings = next)} />
    {/each}
  {/if}
</Panel>

<style>
  .srv-note {
    margin: 0 0 var(--space-3);
    font-size: var(--fs-sm);
  }
</style>
