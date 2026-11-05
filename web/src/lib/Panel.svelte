<script lang="ts">
  import type { Snippet } from 'svelte';

  interface Props {
    title?: string;
    noPadding?: boolean;
    /** Lets a sticky child pin to the page: overflow hidden would make this panel its scroll container (#0622). */
    pinnable?: boolean;
    children: Snippet;
  }

  let { title = '', noPadding = false, pinnable = false, children }: Props = $props();
</script>

<section class="panel" class:pinnable>
  {#if title}
    <header class="panel-header">{title}</header>
  {/if}
  <div class="panel-body" class:no-padding={noPadding}>
    {@render children()}
  </div>
</section>

<style>
  .panel {
    background: var(--bg-panel);
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
    margin-bottom: var(--space-4);
    overflow: hidden;
  }
  .panel.pinnable {
    overflow: clip;
  }
  .panel-header {
    background: var(--bg-header);
    border-bottom: var(--border-w) solid var(--border);
    padding: var(--space-2) var(--space-3);
    font-size: var(--fs-base);
    font-weight: 600;
    color: var(--text);
  }
  .panel-body {
    padding: var(--space-4);
  }
  .panel-body.no-padding {
    padding: 0;
  }
</style>
