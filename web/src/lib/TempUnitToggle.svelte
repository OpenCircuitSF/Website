<script lang="ts">
  // The °F/°C switch shown once on guides that print a temperature.
  import { TEMP_UNIT_SYMBOL, type TempUnit } from './temperature';
  import { tempUnit, setTempUnit } from './tempUnit.svelte';

  const UNITS: TempUnit[] = ['F', 'C'];
</script>

<div class="units" role="radiogroup" aria-label="Temperature units">
  {#each UNITS as u (u)}
    <button type="button" role="radio" aria-checked={tempUnit.value === u} class:on={tempUnit.value === u} onclick={() => setTempUnit(u)}
      >{TEMP_UNIT_SYMBOL[u]}</button
    >
  {/each}
</div>

<style>
  .units {
    display: flex;
    gap: var(--space-2);
  }
  .units button {
    font: inherit;
    font-size: var(--fs-base);
    padding: 4px 12px;
    border-radius: 999px;
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  .units button.on {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
</style>
