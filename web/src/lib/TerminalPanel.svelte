<script lang="ts">
  // Terminal motif (PRD §4.4): a bordered panel with the three-dot title bar.
  // Ported from placeholder/index.html's .terminal / .terminal-bar /
  // .terminal-body. Structurally similar to Panel.svelte (bordered panel +
  // optional header + noPadding), but with the terminal window-chrome dots
  // instead of a plain header bar.
  import type { Snippet } from 'svelte';

  interface Props {
    title?: string;
    noPadding?: boolean;
    children: Snippet;
  }

  let { title = '', noPadding = false, children }: Props = $props();
</script>

<section class="terminal-panel">
  <div class="terminal-bar">
    <span class="dots" aria-hidden="true">
      <span class="dot dot-1"></span><span class="dot dot-2"></span><span class="dot dot-3"></span>
    </span>
    {#if title}
      <span class="terminal-title">{title}</span>
    {/if}
  </div>
  <div class="terminal-body" class:no-padding={noPadding}>
    {@render children()}
  </div>
</section>

<style>
  .terminal-panel {
    background: var(--bg-panel);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
    overflow: hidden;
  }

  .terminal-bar {
    display: flex;
    align-items: center;
    gap: 7px;
    padding: var(--space-3) var(--space-4);
    background: var(--bg-header);
    border-bottom: var(--border-w) solid var(--border);
  }

  .dots {
    display: flex;
    align-items: center;
    gap: 7px;
  }

  .dot {
    flex: none;
    width: 10px;
    height: 10px;
    border-radius: 50%;
  }
  .dot-1 { background: var(--danger); }
  .dot-2 { background: var(--warning); }
  .dot-3 { background: var(--accent); }

  .terminal-title {
    margin-left: var(--space-2);
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--text-faint);
  }

  .terminal-body {
    padding: var(--space-6) var(--space-5) var(--space-5);
  }
  .terminal-body.no-padding {
    padding: 0;
  }
</style>
