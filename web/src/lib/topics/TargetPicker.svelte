<script lang="ts">
  // A row of choices for what the meter's probes touch (#0666).
  import type { Target } from './multimeter';

  let { label, targets, value = $bindable() }: { label: string; targets: readonly Target[]; value: string } = $props();
</script>

<div class="picker" role="radiogroup" aria-label={label}>
  <span class="lab">{label}</span>
  <div class="opts">
    {#each targets as t (t.id)}
      <button type="button" role="radio" aria-checked={value === t.id} class:on={value === t.id} onclick={() => (value = t.id)}>
        {t.name}
      </button>
    {/each}
  </div>
</div>

<style>
  .lab {
    display: block;
    font-size: var(--fs-sm);
    color: var(--text-muted);
    margin-bottom: var(--space-1);
  }
  .opts {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }
  button {
    font: inherit;
    font-size: var(--fs-base);
    padding: 4px 10px;
    border-radius: 999px;
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  button.on {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
</style>
