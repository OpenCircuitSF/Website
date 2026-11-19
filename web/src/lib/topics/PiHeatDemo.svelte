<script lang="ts">
  // Pick a load and the cooling; watch ten minutes of a Pi 4's temperature
  // and clock speed. An illustrative model, logic in piHardware.ts (#0724).
  import { formatTemp } from '../temperature';
  import { tempUnit } from '../tempUnit.svelte';
  import { COOLING, HARD_C, HEAT_LOADS, HEAT_TITLES, MAX_MHZ, SOFT_C, heatRun, hex, minSec, type CoolingId, type HeatLoadId } from './piHardware';

  let load = $state<HeatLoadId>('rgb');
  let cooling = $state<CoolingId>('closedBox');

  const r = $derived(heatRun(load, cooling));
  const ft = (c: number) => formatTemp(c, tempUnit.value);

  const X0 = 40;
  const X1 = 380;
  const xOf = (t: number) => X0 + (t / 600) * (X1 - X0);
  const yT = (c: number) => 160 - ((Math.min(95, Math.max(20, c)) - 20) / 75) * 140;
  const yM = (m: number) => 280 - (m / MAX_MHZ) * 80;
  const every10 = $derived(r.samples.filter((s) => s.t % 10 === 0));
  const tempPts = $derived(every10.map((s) => `${xOf(s.t).toFixed(1)},${yT(s.tempC).toFixed(1)}`).join(' '));
  const mhzPts = $derived(every10.map((s) => `${xOf(s.t).toFixed(1)},${yM(s.mhz).toFixed(1)}`).join(' '));
  const TICKS = [0, 2, 4, 6, 8, 10];
</script>

<div class="pt">
  <!-- #0727: illustration slot -->
  <div class="group" role="radiogroup" aria-label="What the Pi is doing">
    {#each HEAT_LOADS as l (l.id)}
      <button type="button" role="radio" aria-checked={load === l.id} class:on={load === l.id} onclick={() => (load = l.id)}>{l.label}</button>
    {/each}
  </div>
  <div class="group" role="radiogroup" aria-label="Cooling">
    {#each COOLING as c (c.id)}
      <button type="button" role="radio" aria-checked={cooling === c.id} class:on={cooling === c.id} onclick={() => (cooling = c.id)}>{c.label}</button>
    {/each}
  </div>

  <figure>
    <svg
      viewBox="0 0 400 300"
      role="img"
      aria-label="Over 10 minutes the temperature goes from {ft(r.startC)} to {ft(r.endC)}; the clock ends at {r.endMhz} MHz"
    >
      <line x1={X0} y1="160" x2={X1} y2="160" class="axis" />
      <line x1={X0} y1="280" x2={X1} y2="280" class="axis" />
      {#each TICKS as m (m)}
        <line x1={xOf(m * 60)} y1="280" x2={xOf(m * 60)} y2="285" class="axis" />
        <text x={xOf(m * 60)} y="296" class="t muted" text-anchor="middle">{m === 10 ? '10 min' : m}</text>
      {/each}
      <line x1={X0} y1={yT(SOFT_C)} x2={X1} y2={yT(SOFT_C)} class="limit warn" />
      <text x={X0 + 4} y={yT(SOFT_C) + 12} class="t warn">throttling starts {ft(SOFT_C)}</text>
      <line x1={X0} y1={yT(HARD_C)} x2={X1} y2={yT(HARD_C)} class="limit danger" />
      <text x={X0 + 4} y={yT(HARD_C) - 4} class="t danger">Arm and GPU {ft(HARD_C)}</text>
      <polyline points={tempPts} class="curve" />
      {#if r.softAtS !== null}
        <circle cx={xOf(r.softAtS)} cy={yT(SOFT_C)} r="4" class="dot" />
        <text x={xOf(r.softAtS)} y={yT(SOFT_C) + 24} class="t" text-anchor="middle">{minSec(r.softAtS)}</text>
      {/if}
      <polyline points={mhzPts} class="curve clock" />
      <text x={X1} y="196" class="t" text-anchor="end">clock {r.endMhz} MHz</text>
    </svg>
  </figure>

  <div class="out {r.status}" aria-live="polite">
    <p class="big"><strong>{ft(r.endC)}</strong> after 10 minutes</p>
    <h3>{HEAT_TITLES[r.status]}</h3>
    {#if (r.status === 'soft' || r.status === 'hard') && r.softAtS !== null}
      <p>Crossed {ft(SOFT_C)} after {minSec(r.softAtS)}; the clock fell to {r.endMhz} of 1500 MHz.</p>
    {:else if r.status === 'warm'}
      <p>Under the line, but not by much: a warm day or a closed lid tips it over.</p>
    {:else}
      <p>Full speed the whole time.</p>
    {/if}
    <p><code>vcgencmd measure_temp</code> → <code>temp={r.endC.toFixed(1)}'C</code> (vcgencmd always prints Celsius.)</p>
    <p><code>vcgencmd get_throttled</code> → <code>throttled={hex(r.throttled)}</code></p>
  </div>
  <p class="note">An illustrative model: real boards, cases and rooms vary. Newer Pi 4 boards run at 1.8 GHz; the shape is the same.</p>
  <pre><code>vcgencmd measure_temp
vcgencmd get_throttled
watch -n 2 vcgencmd measure_temp</code></pre>
  <p class="links">
    <a href="/guides/pi-undervoltage">Decode get_throttled</a>; <a href="/guides/pi-rgb-matrix-flicker">RGB panels keep a core busy</a>.
  </p>
</div>

<style>
  .group {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin-bottom: var(--space-3);
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
  .note,
  .links {
    margin: 0 0 var(--space-3);
    font-size: var(--fs-base);
  }
  .note {
    color: var(--text-muted);
  }
  figure {
    margin: 0;
  }
  svg {
    display: block;
    width: 100%;
    max-width: 520px;
    height: auto;
  }
  .axis {
    stroke: var(--text-muted);
    stroke-width: 1.5;
  }
  .limit {
    stroke-width: 1.5;
    stroke-dasharray: 4 3;
  }
  .limit.warn {
    stroke: var(--warning);
  }
  .limit.danger {
    stroke: var(--danger);
  }
  .curve {
    fill: none;
    stroke: var(--accent);
    stroke-width: 2.5;
  }
  .curve.clock {
    stroke: var(--text);
  }
  .dot {
    fill: var(--warning);
  }
  .t {
    font-family: var(--font-mono);
    font-size: 11px;
    fill: var(--text);
  }
  .t.muted {
    fill: var(--text-muted);
  }
  .t.warn {
    fill: var(--warning);
  }
  .t.danger {
    fill: var(--danger);
  }
  .out {
    margin: var(--space-3) 0;
    padding: var(--space-3);
    border: var(--border-w) solid var(--border);
    border-radius: 8px;
    background: var(--bg-panel);
  }
  .out p {
    margin: var(--space-1) 0;
  }
  .big {
    font-size: var(--fs-lg);
  }
  .out h3 {
    margin: var(--space-1) 0;
    font-size: var(--fs-lg);
    color: var(--accent);
  }
  .out.warm h3,
  .out.soft h3 {
    color: var(--warning);
  }
  .out.hard h3 {
    color: var(--danger);
  }
</style>
