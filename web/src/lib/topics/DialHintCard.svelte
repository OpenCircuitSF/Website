<script lang="ts">
  // Tells the reader which dial setting a multimeter guide needs: the symbol,
  // where it is on the meter in view, and a button that turns the dial there.
  // The meter itself pulses the same setting (MultimeterMeter's `hint`).
  import type { DialHint } from './multimeter';

  interface Props {
    hint: DialHint;
    dial: string;
    onset: (id: string) => void;
  }
  let { hint, dial, onset }: Props = $props();
  const done = $derived(dial === hint.target);
  const symbolName = { cont: 'Continuity symbol: a diode with sound waves', dcv: 'DC volts symbol', dca: 'DC current symbol' };
</script>

<div class="find" class:done>
  <svg class="sym" viewBox="0 0 64 32" role="img" aria-label={symbolName[hint.symbol]}>
    {#if hint.symbol === 'cont'}
      <path d="M4 16h8M12 6v20l16-10z M28 6v20 M28 16h6" />
      <path d="M41 11a7 7 0 0 1 0 10 M47 7a13 13 0 0 1 0 18 M53 3a19 19 0 0 1 0 26" />
    {:else}
      <text x="18" y="25" text-anchor="middle">{hint.symbol === 'dcv' ? 'V' : 'A'}</text>
      <path d="M34 12h24" />
      <path d="M34 20h6 M43 20h6 M52 20h6" />
    {/if}
  </svg>
  <div>
    <strong>{done ? `The dial is on ${hint.setting}` : 'Set the dial'}</strong>
    <p>{hint.what} {hint.where}</p>
    {#if !done}
      <button type="button" onclick={() => onset(hint.target)}>Turn the dial to {hint.setting}</button>
    {/if}
  </div>
</div>

<style>
  .find {
    display: flex;
    gap: var(--space-3);
    align-items: flex-start;
    padding: var(--space-3) var(--space-4);
    border: var(--border-w) solid var(--accent);
    border-radius: 6px;
    background: var(--bg-panel);
  }
  .find.done {
    border-color: var(--border);
  }
  .find p {
    margin: var(--space-1) 0 var(--space-2);
    font-size: var(--fs-md);
  }
  .find.done p {
    margin-bottom: 0;
  }
  .sym {
    flex: none;
    width: 64px;
    height: 32px;
    color: var(--text);
  }
  .sym path {
    fill: none;
    stroke: currentColor;
    stroke-width: 2.5;
    stroke-linecap: round;
    stroke-linejoin: round;
  }
  .sym text {
    font-family: var(--font-mono);
    font-weight: 700;
    font-size: 24px;
    fill: currentColor;
  }
  .find button {
    font: inherit;
    font-size: var(--fs-base);
    padding: var(--space-2) var(--space-3);
    border: var(--border-w) solid var(--accent);
    border-radius: var(--radius);
    background: var(--accent);
    color: var(--accent-text);
    cursor: pointer;
  }
  .find button:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
</style>
