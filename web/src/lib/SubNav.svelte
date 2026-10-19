<!--
  #0600: the one sub-navigation style for Outreach's screens and Settings'
  section jump list, sitting below #0599's two nav rows. A <nav> of plain
  links: quiet muted chips, the current one in --text with a 2px --accent
  edge. It stays on one line and scrolls sideways when it must; the shared
  .scroll-fade-x class (app.css) shows a fade at the right edge while more
  items are off-screen.
-->
<script lang="ts">
  export type SubNavItem = {
    href: string;
    label: string;
    current?: boolean;
    onclick?: () => void;
  };

  let { label, items }: { label: string; items: SubNavItem[] } = $props();
</script>

<nav class="subnav scroll-fade-x" aria-label={label}>
  {#each items as item (item.href)}
    <a
      href={item.href}
      aria-current={item.current ? 'page' : undefined}
      onclick={item.onclick}>{item.label}</a>
  {/each}
</nav>

<style>
  .subnav {
    display: flex;
    gap: var(--space-2);
    margin-bottom: var(--space-3);
    overflow-x: auto;
    white-space: nowrap;
  }
  .subnav a {
    display: inline-flex;
    align-items: center;
    min-height: 36px;
    padding: var(--space-1) var(--space-2);
    border-bottom: 2px solid transparent;
    color: var(--text-muted);
    font-size: var(--fs-base);
    text-decoration: none;
  }
  .subnav a:hover {
    color: var(--text);
  }
  .subnav a[aria-current='page'] {
    color: var(--text);
    font-weight: 600;
    border-bottom-color: var(--accent);
  }
</style>
