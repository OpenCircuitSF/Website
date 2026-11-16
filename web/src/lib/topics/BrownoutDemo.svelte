<script lang="ts">
  // WiFi brownout guide (#0672): the 5V pin's voltage over 20 ms while WiFi
  // transmits in short bursts. A thin cable sags on every burst; a capacitor
  // smooths it; an LED strip on the same 5V pin pulls it down for good.
  // The circuit model is simulateSupply() in wireless.ts.
  import {
    BROWNOUT_V,
    BURSTS,
    SIM_MS,
    STEP_MS,
    USB_V,
    simulateSupply,
    type SupplyOptions,
  } from './wireless';

  let o = $state<SupplyOptions>({ thinCable: true, capacitor: false, strip: false, lowTx: false });
  const trace = $derived(simulateSupply(o));

  // Chart space: 0..SIM_MS across, 2.5..5 V up.
  const W = 400;
  const H = 180;
  const PAD_L = 34;
  const PAD_B = 22;
  const V_MIN = 2.5;
  const V_MAX = 5;
  const x = (ms: number) => PAD_L + (ms / SIM_MS) * (W - PAD_L - 6);
  const y = (v: number) => 6 + ((V_MAX - Math.max(V_MIN, Math.min(V_MAX, v))) / (V_MAX - V_MIN)) * (H - PAD_B - 6);
  const points = $derived(trace.volts.map((v, i) => `${x(i * STEP_MS).toFixed(1)},${y(v).toFixed(1)}`).join(' '));
  const tone = $derived(trace.resets ? 'bad' : trace.marginal ? 'meh' : 'good');
  const title = $derived(trace.resets ? 'Brownout: the board resets' : trace.marginal ? 'Stable, but close to the edge' : 'Stable');
  const body = $derived(
    trace.resets
      ? o.strip
        ? 'The strip’s current sags the 5V line all the time, and every WiFi burst pushes it under the line. Give the strip its own 5V supply and join the grounds.'
        : 'Each WiFi burst drops the voltage across the thin cable for a millisecond. That is enough for the brownout detector. Try a better cable, the capacitor, or lower TX power.'
      : trace.marginal
        ? 'It survives, but a warmer cable or a weaker port could tip it over. Better to fix the supply too.'
        : o.strip
          ? 'The voltage holds, but the strip plus WiFi is more than a USB port’s 500 mA. Use a separate supply for the strip.'
          : 'Plenty of margin. The bursts barely show.',
  );
</script>

<div class="brown">
  <div class="opts">
    <label><input type="checkbox" bind:checked={o.thinCable} /> Thin, long USB cable</label>
    <label><input type="checkbox" bind:checked={o.capacitor} /> 470 µF capacitor across 5V and GND</label>
    <label><input type="checkbox" bind:checked={o.strip} /> LED strip on the board’s 5V pin</label>
    <label><input type="checkbox" bind:checked={o.lowTx} /> Lower TX power</label>
  </div>

  <div class="layout">
    <svg viewBox="0 0 {W} {H}" role="img" aria-label="Voltage at the 5V pin over 20 milliseconds. Lowest point {trace.minV.toFixed(2)} volts.">
      {#each BURSTS as [s, len] (s)}
        <rect class="burst" x={x(s)} y="6" width={x(s + len) - x(s)} height={H - PAD_B - 6} />
      {/each}
      {#each [5, 4, 3] as v (v)}
        <line class="grid" x1={PAD_L} x2={W - 6} y1={y(v)} y2={y(v)} />
        <text class="tick" x={PAD_L - 4} y={y(v) + 4} text-anchor="end">{v} V</text>
      {/each}
      <line class="limit" x1={PAD_L} x2={W - 6} y1={y(BROWNOUT_V)} y2={y(BROWNOUT_V)} />
      <text class="limit-label" x={W - 8} y={y(BROWNOUT_V) + 13} text-anchor="end">reset below ≈{BROWNOUT_V} V</text>
      <polyline class="trace {tone}" points={points} />
      <text class="tick" x={x(BURSTS[0][0])} y={H - 6}>WiFi bursts</text>
      <text class="tick" x={W - 6} y={H - 6} text-anchor="end">{SIM_MS} ms</text>
    </svg>

    <div class="result {tone}" aria-live="polite">
      <p class="big">{trace.minV.toFixed(2)}<span> V lowest</span></p>
      <h3>{title}</h3>
      <p>{body}</p>
      <p class="serial" class:shown={trace.resets}>
        <code>{trace.resets ? 'Brownout detector was triggered' : `5V pin from USB: ${USB_V} V`}</code>
      </p>
    </div>
  </div>
</div>

<style>
  .opts {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
    gap: var(--space-2);
    margin-bottom: var(--space-4);
  }
  .opts label {
    display: flex;
    gap: var(--space-2);
    align-items: flex-start;
    margin: 0;
    font-size: var(--fs-md);
    font-weight: 400;
    color: var(--text);
  }
  input {
    accent-color: var(--accent);
    margin-top: 4px;
  }
  .layout {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: var(--space-4);
    align-items: center;
  }
  svg {
    width: 100%;
    height: auto;
    background: var(--bg-panel);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
  }
  .burst {
    fill: var(--accent-subtle);
  }
  .grid {
    stroke: var(--border);
    stroke-width: 1;
  }
  .tick,
  .limit-label {
    font-family: var(--font-mono);
    font-size: 11px;
    fill: var(--text-faint);
  }
  .limit {
    stroke: var(--danger);
    stroke-width: 1.5;
    stroke-dasharray: 5 4;
  }
  .limit-label {
    fill: var(--danger);
  }
  .trace {
    fill: none;
    stroke: var(--accent);
    stroke-width: 2.5;
    stroke-linejoin: round;
  }
  .trace.meh {
    stroke: var(--warning);
  }
  .trace.bad {
    stroke: var(--danger);
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
  .big {
    margin: 0;
    font-family: var(--font-mono);
    font-size: clamp(28px, 6vw, 40px);
    font-weight: 700;
    line-height: 1.1;
  }
  .big span {
    font-size: 0.45em;
    color: var(--text-faint);
  }
  h3 {
    margin: var(--space-2) 0 var(--space-1);
  }
  .result p {
    font-size: var(--fs-md);
  }
  .serial {
    margin: var(--space-2) 0 0;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    color: var(--text-faint);
  }
  .serial.shown {
    color: var(--danger);
  }
</style>
