<script lang="ts">
  // Decoupling guide (#0691): a regulator, a long 3.3 V trace and a chip that
  // pulls fast current spikes. Add a capacitor, pick its size, slide it away
  // from the chip, and watch the rail dip on the scope trace. The teaching
  // model is railDip() / railTrace() in capacitors.ts.
  import {
    DECOUPLING_MODEL,
    MIN_V,
    RAIL_V,
    SPIKES_NS,
    WINDOW_NS,
    capPathNH,
    effectiveNH,
    railDip,
    railTrace,
  } from './capacitors';

  const SIZES = [
    { f: 10e-9, name: '10 nF' },
    { f: 100e-9, name: '100 nF' },
    { f: 1e-6, name: '1 µF' },
  ];
  let fitted = $state(false);
  let size = $state(100e-9);
  let mm = $state(2);

  const opts = $derived({ capF: fitted ? size : null, distanceMm: mm });
  const dip = $derived(railDip(opts));
  const trace = $derived(railTrace(opts, 0.5));
  const below = $derived(dip.minV < MIN_V);
  const lowNow = $derived(Math.min(...trace.map((p) => p[1])));

  // Board drawing: regulator at x=20, chip at x=330; 1 mm of trace = 5 px.
  const CHIP_X = 300;
  const capX = $derived(CHIP_X - 6 - mm * 5);

  // Scope: 0..WINDOW_NS across, 2.6..3.6 V up.
  const W = 400;
  const H = 180;
  const L = 36;
  const B = 22;
  const VLO = 2.6;
  const VHI = 3.8;
  const sx = (ns: number) => L + (ns / WINDOW_NS) * (W - L - 6);
  const sy = (volts: number) => 6 + ((VHI - Math.max(VLO, Math.min(VHI, volts))) / (VHI - VLO)) * (H - B - 6);
  const points = $derived(trace.map(([t, volts]) => `${sx(t).toFixed(1)},${sy(volts).toFixed(1)}`).join(' '));

  const title = $derived(below ? `Dips below ${MIN_V.toFixed(1)} V` : dip.minV < 3.15 ? 'Holding, but close' : 'Steady');
  const body = $derived(
    !fitted
      ? 'Every current spike has to come all the way from the regulator, through the wiring’s inductance. The rail sags each time, below what the chip is rated for.'
      : below
        ? 'The capacitor is too far away. The trace between it and the chip adds inductance, so it can’t deliver the current fast enough. Move it right next to the power pin.'
        : size < 100e-9
          ? 'Close enough, but 10 nF runs low on charge during each spike. 100 nF is the usual choice.'
          : 'The capacitor sits right at the pin and supplies each spike itself. The regulator only has to top it up.',
  );
</script>

<div class="dec">
  <div class="controls">
    <label class="check"><input type="checkbox" bind:checked={fitted} /> Fit a decoupling capacitor</label>
    <div class="group" role="radiogroup" aria-label="Capacitor size">
      {#each SIZES as s (s.f)}
        <button type="button" role="radio" aria-checked={size === s.f} class:on={size === s.f} disabled={!fitted} onclick={() => (size = s.f)}>{s.name}</button>
      {/each}
    </div>
    <label class="slider">
      <span>Distance from the chip’s power pin: <strong>{mm} mm</strong></span>
      <input type="range" min="1" max="40" step="1" bind:value={mm} disabled={!fitted} />
    </label>
  </div>

  <svg class="board" viewBox="0 0 360 120" role="img" aria-label={fitted ? `Board: capacitor ${mm} millimetres from the chip` : 'Board: no decoupling capacitor'}>
    <rect class="pcb" x="0" y="0" width="360" height="120" rx="6" />
    <rect class="part" x="10" y="20" width="50" height="40" rx="3" />
    <text class="lbl" x="35" y="44" text-anchor="middle">REG</text>
    <line class="trace" x1="60" y1="30" x2={CHIP_X} y2="30" />
    <text class="t" x="70" y="22">3.3 V, {DECOUPLING_MODEL.supplyNH} nH of wiring</text>
    <line class="trace gnd" x1="60" y1="96" x2={CHIP_X} y2="96" />
    <text class="t" x="70" y="112">GND</text>
    <rect class="chip" x={CHIP_X} y="18" width="50" height="90" rx="3" />
    <text class="lbl light" x={CHIP_X + 25} y="66" text-anchor="middle">C3</text>
    <line class="trace" x1={CHIP_X - 6} y1="30" x2={CHIP_X} y2="30" />
    {#if fitted}
      <line class="trace hot" x1={capX} y1="30" x2={CHIP_X} y2="30" />
      <line class="trace" x1={capX} y1="30" x2={capX} y2="54" />
      <rect class="cap" x={capX - 8} y="54" width="16" height="22" rx="2" />
      <line class="trace" x1={capX} y1="76" x2={capX} y2="96" />
      <text class="t" x={capX} y="88" text-anchor="end" dx="-12">{capPathNH(mm)} nH</text>
    {/if}
    <!-- Spike arrow into the chip's power pin -->
    <path class="spike" d="M {CHIP_X - 2} 42 l -10 0 l 6 -6 m -6 6 l 6 6" />
  </svg>

  <div class="layout">
    <svg class="scope" viewBox="0 0 {W} {H}" role="img" aria-label="Rail voltage at the chip over {WINDOW_NS} nanoseconds. Lowest point {dip.minV.toFixed(2)} volts.">
      {#each SPIKES_NS as s (s)}
        <rect class="burst" x={sx(s)} y="6" width={sx(s + DECOUPLING_MODEL.spikeNs) - sx(s)} height={H - B - 6} />
      {/each}
      {#each [3.6, 3.3, 3.0, 2.6] as g (g)}
        <line class="grid" x1={L} x2={W - 6} y1={sy(g)} y2={sy(g)} />
        <text class="t" x={L - 4} y={sy(g) + 4} text-anchor="end">{g.toFixed(1)}</text>
      {/each}
      <line class="limit" x1={L} x2={W - 6} y1={sy(MIN_V)} y2={sy(MIN_V)} />
      <text class="t warn" x={W - 8} y={sy(MIN_V) + 13} text-anchor="end">chip minimum {MIN_V.toFixed(1)} V</text>
      <polyline class="v" class:bad={below} points={points} />
      <text class="t" x={sx(SPIKES_NS[0])} y={H - 6}>current spikes</text>
      <text class="t" x={W - 6} y={H - 6} text-anchor="end">{WINDOW_NS} ns</text>
    </svg>

    <div class="result" class:bad={below} aria-live="polite">
      <p class="big">{dip.minV.toFixed(2)}<span> V lowest</span></p>
      <h3>{title}</h3>
      <p>{body}</p>
      <p class="sm">
        Dip {Math.round((RAIL_V - lowNow) * 1000)} mV. The spike sees {effectiveNH(opts).toFixed(1)} nH.
      </p>
    </div>
  </div>
  <p class="note">
    A teaching model with round numbers: a {DECOUPLING_MODEL.spikeA * 1000} mA spike that rises in {DECOUPLING_MODEL.riseNs} ns,
    about 1 nH per millimetre of trace, and no ringing. Real boards differ, but the shape of the lesson holds.
  </p>
</div>

<style>
  .controls {
    display: grid;
    gap: var(--space-2);
    margin-bottom: var(--space-3);
  }
  .check {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    font-size: var(--fs-md);
    font-weight: 400;
    color: var(--text);
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
  .group button:disabled,
  input:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
  .slider {
    display: grid;
    gap: var(--space-1);
    font-size: var(--fs-base);
    font-weight: 400;
    color: var(--text);
  }
  input[type='range'] {
    accent-color: var(--accent);
  }
  input:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
  svg {
    display: block;
    width: 100%;
    height: auto;
  }
  .board {
    max-width: 480px;
    margin-bottom: var(--space-3);
  }
  .pcb {
    fill: #1f4d2e;
  }
  .part {
    fill: #333;
    stroke: #888;
  }
  .chip {
    fill: #222;
    stroke: #888;
  }
  .lbl {
    font-family: var(--font-mono);
    font-size: 11px;
    fill: #ddd;
  }
  .trace {
    stroke: #c9a24a;
    stroke-width: 4;
    stroke-linecap: round;
  }
  .trace.hot {
    stroke: #ff8a3d;
  }
  .cap {
    fill: #c8a27a;
    stroke: #6b4a2a;
  }
  .spike {
    fill: none;
    stroke: #ffd23d;
    stroke-width: 2;
  }
  .board .t {
    fill: #cfe3d4;
  }
  .t {
    font-family: var(--font-mono);
    font-size: 10px;
    fill: var(--text-muted);
  }
  .t.warn {
    fill: var(--danger);
  }
  .layout {
    display: grid;
    grid-template-columns: 1fr 200px;
    gap: var(--space-4);
    align-items: center;
  }
  .burst {
    fill: var(--accent);
    opacity: 0.08;
  }
  .grid {
    stroke: var(--border);
    stroke-width: 1;
  }
  .limit {
    stroke: var(--danger);
    stroke-dasharray: 5 4;
  }
  .v {
    fill: none;
    stroke: var(--accent);
    stroke-width: 2;
    stroke-linejoin: round;
  }
  .v.bad {
    stroke: var(--danger);
  }
  .result {
    padding: var(--space-3);
    border-radius: 6px;
    border: var(--border-w) solid var(--accent);
  }
  .result.bad {
    border-color: var(--danger);
  }
  .result p,
  .result h3 {
    margin: 0 0 var(--space-1);
  }
  .result h3 {
    font-size: var(--fs-md);
  }
  .big {
    font-size: var(--fs-xl);
    font-weight: 700;
  }
  .big span {
    font-size: var(--fs-base);
    font-weight: 400;
  }
  .sm,
  .note {
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  @media (max-width: 640px) {
    .layout {
      grid-template-columns: 1fr;
    }
  }
</style>
