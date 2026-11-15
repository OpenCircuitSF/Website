<script lang="ts">
  // Turn the iron's dial, pick the solder and the tip, and see what the
  // joint comes out like and how long it takes. The model (soldering.ts)
  // uses the heat that actually reaches the joint: a fine tip delivers less
  // than the dial says, which is why tip size matters more than the number.
  import GuideArt from './GuideArt.svelte';
  import { alloyById, jointAt, MAX_SECONDS, type TipShape } from './soldering';

  type Solder = 'sn63' | 'sac305';
  let dial = $state(250);
  let solder = $state<Solder>('sn63');
  let tip = $state<TipShape>('chisel');

  const result = $derived(jointAt(dial, solder, tip));
  const sweet = $derived(alloyById(solder).ironC!);
  const MIN = 150;
  const MAX = 450;
  const pct = (c: number) => ((c - MIN) / (MAX - MIN)) * 100;
  const tone = $derived(result.kind === 'good' ? 'good' : result.kind === 'slow' ? 'meh' : 'bad');
</script>

<div class="temp">
  <div class="controls">
    <label class="dial">
      <span>Iron set to <strong>{dial} °C</strong></span>
      <input type="range" min={MIN} max={MAX} step="5" bind:value={dial} />
      <span class="band" aria-hidden="true">
        <span class="win" style:left="{pct(sweet[0])}%" style:width="{pct(sweet[1]) - pct(sweet[0])}%"></span>
      </span>
      <span class="scale" aria-hidden="true"><span>{MIN}</span><span>sweet spot {sweet[0]}–{sweet[1]}</span><span>{MAX}</span></span>
    </label>
    <div class="group" role="radiogroup" aria-label="Solder">
      <button type="button" role="radio" aria-checked={solder === 'sn63'} class:on={solder === 'sn63'} onclick={() => (solder = 'sn63')}>Leaded 63/37</button>
      <button type="button" role="radio" aria-checked={solder === 'sac305'} class:on={solder === 'sac305'} onclick={() => (solder = 'sac305')}>Lead-free SAC305</button>
    </div>
    <div class="group" role="radiogroup" aria-label="Tip">
      <button type="button" role="radio" aria-checked={tip === 'chisel'} class:on={tip === 'chisel'} onclick={() => (tip = 'chisel')}>Chisel tip</button>
      <button type="button" role="radio" aria-checked={tip === 'fine'} class:on={tip === 'fine'} onclick={() => (tip = 'fine')}>Fine conical tip</button>
    </div>
  </div>

  <div class="layout">
    <GuideArt art={result.art} />
    <div class="result {tone}" aria-live="polite">
      <p class="time">
        {#if result.seconds === null}
          never flows
        {:else if result.seconds >= MAX_SECONDS}
          {MAX_SECONDS}+ s
        {:else}
          ~{result.seconds} s
        {/if}
        <span>to flow</span>
      </p>
      <h3>{result.title}</h3>
      <p>{result.body}</p>
    </div>
  </div>
</div>

<style>
  .controls {
    display: grid;
    gap: var(--space-3);
    margin-bottom: var(--space-4);
  }
  .dial {
    display: grid;
    gap: var(--space-1);
    margin: 0;
    font-size: var(--fs-md);
    font-weight: 400;
    color: var(--text);
  }
  input[type='range'] {
    accent-color: var(--accent);
  }
  .band {
    position: relative;
    height: 6px;
    border-radius: 3px;
    background: var(--bg-header);
  }
  .win {
    position: absolute;
    top: 0;
    bottom: 0;
    background: var(--accent);
    border-radius: 3px;
  }
  .scale {
    display: flex;
    justify-content: space-between;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    color: var(--text-faint);
  }
  .group {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }
  .group button {
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
  .layout {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: var(--space-5);
    align-items: center;
  }
  .result {
    padding: var(--space-4);
    border: 2px solid var(--accent);
    border-radius: 6px;
    background: var(--bg-panel);
  }
  .result.meh {
    border-color: var(--warning);
  }
  .result.bad {
    border-color: var(--danger);
  }
  .time {
    margin: 0;
    font-family: var(--font-mono);
    font-size: clamp(28px, 6vw, 40px);
    font-weight: 700;
    line-height: 1.1;
  }
  .time span {
    font-size: 0.45em;
    color: var(--text-faint);
  }
  h3 {
    margin: var(--space-2) 0 var(--space-1);
  }
  .result p:last-child {
    margin: 0;
    font-size: var(--fs-md);
  }
</style>
