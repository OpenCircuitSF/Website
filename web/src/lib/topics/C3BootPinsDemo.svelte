<script lang="ts">
  // "Will it boot?" Hang a part on a strapping pin (or a plain one), press
  // RESET, and see what the chip does. Rules in esp32c3PinsPower.ts.
  import C3BoardPicker from './C3BoardPicker.svelte';
  import { BOOT_DEMO_PINS, BOOT_PARTS, bootResult, levelAtReset, pinById, type BootPart, type BootPin, type BootResult } from './esp32c3PinsPower';

  let pin = $state<BootPin>('gpio9');
  let part = $state<BootPart>('button');
  let held = $state(false);
  let result = $state<BootResult | null>(null);

  const level = $derived(levelAtReset(pin, part, held));
  const LEVEL_TEXT = { high: 'high', low: 'low', floating: 'floating (fine)', unsure: 'about 2 V: not clearly high or low' } as const;

  function changed() {
    result = null;
  }
  function reset() {
    result = bootResult(pin, part, held);
  }
</script>

<div class="boot">
  <div class="layout">
    <C3BoardPicker
      pickable={BOOT_DEMO_PINS}
      selected={pin}
      onpick={(id) => ((pin = id as BootPin), changed())}
      tone={(id) => (id === 'gpio4' ? 'good' : 'care')}
    />
    <div class="side">
      <p class="pick">Pin: <strong>{pinById(pin).title}</strong></p>
      <div class="group" role="radiogroup" aria-label="Pin">
        {#each BOOT_DEMO_PINS as id (id)}
          <button type="button" role="radio" aria-checked={pin === id} class:on={pin === id} onclick={() => ((pin = id), changed())}>
            GPIO {id.replace('gpio', '')}
          </button>
        {/each}
      </div>
      <div class="group" role="radiogroup" aria-label="Part on the pin">
        {#each BOOT_PARTS as p (p.id)}
          <button type="button" role="radio" aria-checked={part === p.id} class:on={part === p.id} onclick={() => ((part = p.id), changed())}>
            {p.label}
          </button>
        {/each}
      </div>
      <label class="held" class:off={part !== 'button'}>
        <input type="checkbox" bind:checked={held} disabled={part !== 'button'} onchange={changed} />
        Hold the button down while pressing RESET
      </label>
      <p class="level">At reset the chip sees: <strong>{LEVEL_TEXT[level]}</strong></p>
      <button type="button" class="reset" onclick={reset}>Press RESET</button>
      <div class="result {result?.outcome ?? 'idle'}" aria-live="polite">
        {#if result}
          <h3>{result.title}</h3>
          <p>{result.body}</p>
        {:else}
          <p>Pick a pin and a part, then press RESET.</p>
        {/if}
      </div>
    </div>
  </div>
</div>

<style>
  .layout {
    display: grid;
    grid-template-columns: minmax(0, 220px) 1fr;
    gap: var(--space-4);
    align-items: start;
  }
  @media (max-width: 600px) {
    .layout {
      grid-template-columns: 1fr;
    }
    .layout :global(.board) {
      max-width: 180px;
    }
  }
  .side {
    display: grid;
    gap: var(--space-3);
    min-width: 0;
  }
  .pick,
  .level {
    margin: 0;
    font-size: var(--fs-md);
  }
  .group {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }
  .group button,
  .reset {
    font: inherit;
    font-size: var(--fs-base);
    padding: 4px 12px;
    border-radius: 999px;
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  .group button.on {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .reset {
    justify-self: start;
    font-weight: 700;
    border-radius: var(--radius);
  }
  .held {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    margin: 0;
    font-size: var(--fs-base);
    font-weight: 400;
    color: var(--text);
  }
  .held.off {
    color: var(--text-muted);
  }
  .result {
    padding: var(--space-3) var(--space-4);
    border-left: 4px solid var(--border-strong);
    background: var(--bg-panel);
    font-size: var(--fs-md);
  }
  .result.runs {
    border-left-color: var(--accent);
  }
  .result.download {
    border-left-color: var(--danger);
  }
  .result.risky {
    border-left-color: var(--warning);
  }
  .result h3 {
    margin: 0 0 var(--space-1);
    font-size: var(--fs-lg);
  }
  .result p {
    margin: 0;
  }
</style>
