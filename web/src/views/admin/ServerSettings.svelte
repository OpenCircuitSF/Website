<!--
  Server settings: operational values (media directory, email identity, SES
  flags, send batch size) saved in the database from admin Settings (#0566).
  Rendered by Admin.svelte as its own panel under the Settings section. Each
  group lives in ServerSettingsGroup.svelte; this component loads the state
  and shows the precedence note.
-->
<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { PRECEDENCE_NOTE } from '../../lib/serverSettings';
  import { ServerSettingsStore } from '../../lib/serverSettingsStore.svelte';
  import Button from '../../lib/Button.svelte';
  import Panel from '../../lib/Panel.svelte';
  import ServerSettingsGroup from './ServerSettingsGroup.svelte';

  // `scope` splits the groups between Settings sections (#0584): the Outreach
  // group lives in the Outreach section, the rest under Server settings.
  // `bare` drops the panel chrome when a parent section already supplies it.
  // `onloaded` fires once the first load settles (success or not), so a
  // section deep link below this panel can wait for it to reach full height.
  // `store` lets two mounts share one fetch (#0593); without it the component
  // owns its own. `showNote` is off for the second mount so the precedence
  // note appears once.
  let {
    scope = 'all',
    bare = false,
    onloaded,
    store: sharedStore,
    showNote = true,
  }: {
    scope?: 'all' | 'server' | 'outreach';
    bare?: boolean;
    onloaded?: () => void;
    store?: ServerSettingsStore;
    showNote?: boolean;
  } = $props();

  const store = untrack(() => sharedStore ?? new ServerSettingsStore());

  onMount(() => {
    void store.ensure().finally(() => onloaded?.());
  });

</script>

{#snippet body()}
  {#if store.loading}
    <p class="text-muted">Loading server settings…</p>
  {:else if store.unavailable}
    <p class="text-muted">Server settings are not available when the server runs without a database.</p>
  {:else if store.loadError}
    <!-- One announcement per shared store (#0597): the mount that owns the
         precedence note (showNote) is the one that announces and offers Retry;
         the second mount shows the same text quietly. -->
    {#if showNote}
      <p class="text-error" role="alert">{store.loadError}</p>
      <Button variant="primary" onclick={() => store.reload().finally(() => onloaded?.())}>Retry</Button>
    {:else}
      <p class="text-error">{store.loadError}</p>
    {/if}
  {:else if store.settings}
    {#if showNote}
      <p class="text-muted srv-note">{PRECEDENCE_NOTE}</p>
    {/if}
    {#each store.settings.groups.filter((g) => scope === 'all' || (scope === 'outreach') === (g.name === 'outreach')) as g (g.name)}
      <ServerSettingsGroup group={g} onupdate={(next) => (store.settings = next)} />
    {/each}
  {/if}
{/snippet}

{#if bare}
  {@render body()}
{:else}
  <Panel title="Server settings">
    {@render body()}
  </Panel>
{/if}

<style>
  .srv-note {
    margin: 0 0 var(--space-3);
    font-size: var(--fs-sm);
  }
</style>
