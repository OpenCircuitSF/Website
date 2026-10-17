<!--
  Outreach connections: the ShortLinks, Bluesky and Mastodon credentials,
  saved in the database from admin Settings (#0564). Rendered by Admin.svelte
  as its own panel under the Settings section. Each group lives in
  OutreachConnectionGroup.svelte; this component loads the state and shows
  the precedence note.

  Test connection checks the SAVED values, so the flow is save, then test.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { getOutreachConnections, ApiError } from '../../lib/api';
  import { PRECEDENCE_NOTE } from '../../lib/outreachConnections';
  import type { OutreachConnections } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import Panel from '../../lib/Panel.svelte';
  import OutreachConnectionGroup from './OutreachConnectionGroup.svelte';

  let conns = $state<OutreachConnections | null>(null);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let unavailable = $state(false);

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    unavailable = false;
    try {
      conns = await getOutreachConnections();
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) unavailable = true;
      else loadError = 'Could not load outreach connections. Please try again.';
    } finally {
      loading = false;
    }
  }

  onMount(load);
</script>

<Panel title="Outreach connections">
  {#if loading}
    <p class="text-muted">Loading outreach connections…</p>
  {:else if unavailable}
    <p class="text-muted">Outreach connections are not available when the server runs without a database.</p>
  {:else if loadError}
    <p class="text-error" role="alert">{loadError}</p>
    <Button variant="primary" onclick={load}>Retry</Button>
  {:else if conns}
    <p class="text-muted conn-note">{PRECEDENCE_NOTE}</p>
    {#each conns.groups as g (g.name)}
      <OutreachConnectionGroup group={g} onupdate={(next) => (conns = next)} />
    {/each}
  {/if}
</Panel>

<style>
  .conn-note {
    margin: 0 0 var(--space-3);
    font-size: var(--fs-sm);
  }
</style>
