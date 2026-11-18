<script lang="ts">
  // #0717: renders a lazily loaded view or topic demo by its routeViews key.
  // While the chunk loads it shows App's existing "Loading…" placeholder (or
  // nothing, for a demo inside its own frame); if the chunk fails and the
  // once-per-URL reload has already been spent, it shows a reload prompt.
  import Button from './Button.svelte';
  import { cachedComponent, clearChunkRecovery, defaultDeps, loadComponent } from './routeViews';
  import { reloadToSameURL } from './versionCheck';

  let { key, placeholder = 'page' }: { key: string; placeholder?: 'page' | 'none' } = $props();

  // $effect.pre so a cached view is set before the first paint: no
  // placeholder flash for a chunk that is already in.
  let View = $state<ReturnType<typeof cachedComponent>>(null);
  let failed = $state(false);

  $effect.pre(() => {
    const k = key;
    const hit = cachedComponent(k);
    failed = false;
    if (hit) {
      View = hit;
      return;
    }
    View = null;
    let live = true;
    void loadComponent(k).then((c) => {
      if (!live) return;
      if (c) View = c;
      else failed = true;
    });
    return () => {
      live = false;
    };
  });

  function retry() {
    clearChunkRecovery(defaultDeps);
    reloadToSameURL();
  }
</script>

{#if View}
  <View />
{:else if failed}
  {#if placeholder === 'none'}
    <p class="text-muted">This demo didn't load. <a href={location.pathname}>Reload</a></p>
  {:else}
    <main id="main-content" class="app-shell">
      <h1 tabindex="-1">This page didn't load</h1>
      <p>Your connection may have dropped, or the site was just updated.</p>
      <Button onclick={retry}>Reload</Button>
    </main>
  {/if}
{:else if placeholder === 'page'}
  <div class="app-shell">
    <p class="text-muted">Loading…</p>
  </div>
{/if}
