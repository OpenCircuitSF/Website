<script lang="ts">
  // RC charging guide (#0691): a 3.3 V source, a switch, a resistor and a
  // capacitor. Flip the switch to charge or discharge, pick R and C, and watch
  // the voltage curve draw itself with τ and 5τ marked. Maths in capacitors.ts.
  import { capUnits, capVoltage, fmtAmps, fmtOhms, fmtSeconds, percentAfter, tau } from './capacitors';

  const SOURCE_V = 3.3;
  const RS = [1000, 2200, 4700, 10000, 22000, 47000, 100000];
  const CS = [1e-6, 4.7e-6, 10e-6, 47e-6, 100e-6, 470e-6];
  const SPAN = 6; // the plot runs to 6τ
  const STEP = 0.06; // τ per animation tick: 6τ in about three seconds

  let ri = $state(3); // 10 kΩ
  let ci = $state(4); // 100 µF
  let mode = $state<'charge' | 'discharge'>('discharge');
  let v0 = $state(0);
  let tn = $state(SPAN); // time since the switch, in τ
  let timer: ReturnType<typeof setInterval> | null = null;

  const r = $derived(RS[ri]);
  const c = $derived(CS[ci]);
  const t = $derived(tau(r, c));
  const target = $derived(mode === 'charge' ? SOURCE_V : 0);
  const v = $derived(capVoltage(tn, 1, target, v0));
  const amps = $derived((target - v) / r);

  function stop() {
    if (timer) clearInterval(timer);
    timer = null;
  }

  function flip(next: 'charge' | 'discharge') {
    stop();
    v0 = v;
    mode = next;
    tn = 0;
    const reduce = typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
    if (reduce) {
      tn = SPAN;
      return;
    }
    timer = setInterval(() => {
      tn = Math.min(SPAN, Math.round((tn + STEP) * 100) / 100);
      if (tn >= SPAN) stop();
    }, 30);
  }

  $effect(() => stop);

  // Plot space: 0..6τ across, 0..3.3 V up.
  const W = 400;
  const H = 190;
  const L = 36;
  const B = 24;
  const px = (n: number) => L + (n / SPAN) * (W - L - 8);
  const py = (volts: number) => 8 + (1 - volts / SOURCE_V) * (H - B - 8);
  function path(from: number, to: number): string {
    const pts: string[] = [];
    for (let n = from; n <= to + 1e-9; n += 0.05) pts.push(`${px(n).toFixed(1)},${py(capVoltage(n, 1, target, v0)).toFixed(1)}`);
    return pts.join(' ');
  }
  const full = $derived(path(0, SPAN));
  const done = $derived(path(0, tn));
  const pct = $derived(Math.abs(target - v0) < 1e-6 ? 100 : (Math.abs(v - v0) / Math.abs(target - v0)) * 100);
</script>

<div class="rc">
  <div class="switch" role="group" aria-label="Switch position">
    <button type="button" class:on={mode === 'charge'} aria-pressed={mode === 'charge'} onclick={() => flip('charge')}>
      Charge (switch to 3.3 V)
    </button>
    <button type="button" class:on={mode === 'discharge'} aria-pressed={mode === 'discharge'} onclick={() => flip('discharge')}>
      Discharge (switch to GND)
    </button>
  </div>

  <div class="layout">
    <svg class="circuit" viewBox="0 0 270 150" role="img" aria-label="Circuit: 3.3 volt source, switch set to {mode}, {fmtOhms(r)} resistor, {capUnits(c * 1e12).best} capacitor at {v.toFixed(2)} volts">
      <!-- Source: + at top -->
      <line class="w" x1="20" y1="30" x2="20" y2="62" />
      <line class="w" x1="8" y1="62" x2="32" y2="62" />
      <line class="w thin" x1="13" y1="70" x2="27" y2="70" />
      <line class="w" x1="20" y1="70" x2="20" y2="130" />
      <text class="t" x="4" y="20">3.3 V</text>
      <!-- Switch: common terminal at (80,30); throws to source (60,30) or ground (80,60) -->
      <line class="w" x1="20" y1="30" x2="60" y2="30" />
      <circle class="term" cx="60" cy="30" r="3" />
      <circle class="term" cx="80" cy="56" r="3" />
      <line class="w" x1="80" y1="59" x2="80" y2="130" />
      <circle class="term" cx="92" cy="30" r="3" />
      <line class="lever" x1="92" y1="30" x2={mode === 'charge' ? 62 : 81} y2={mode === 'charge' ? 27 : 53} />
      <!-- Resistor -->
      <line class="w" x1="95" y1="30" x2="110" y2="30" />
      <polyline class="w" points="110,30 114,22 120,38 126,22 132,38 138,22 144,38 148,30" />
      <text class="t" x="110" y="15">{fmtOhms(r)}</text>
      <line class="w" x1="148" y1="30" x2="180" y2="30" />
      <!-- Capacitor -->
      <line class="w" x1="180" y1="30" x2="180" y2="74" />
      <line class="w plate" x1="166" y1="74" x2="194" y2="74" />
      <line class="w plate" x1="166" y1="84" x2="194" y2="84" />
      <rect class="fill" x="168" y={84 - (v / SOURCE_V) * 10} width="24" height={(v / SOURCE_V) * 10} />
      <line class="w" x1="180" y1="84" x2="180" y2="130" />
      <text class="t" x="198" y="76">{capUnits(c * 1e12).best}</text>
      <text class="t strong" x="198" y="92">{v.toFixed(2)} V</text>
      <!-- Ground -->
      <line class="w" x1="20" y1="130" x2="180" y2="130" />
      <line class="w" x1="92" y1="130" x2="92" y2="138" />
      <line class="w" x1="84" y1="138" x2="100" y2="138" />
      <line class="w thin" x1="88" y1="142" x2="96" y2="142" />
      <text class="t" x="110" y="58">{Math.abs(amps) < 1e-6 ? 'no current' : `${fmtAmps(Math.abs(amps))} ${amps > 0 ? '→' : '←'}`}</text>
    </svg>

    <div class="readout" aria-live="polite">
      <p class="big">{v.toFixed(2)} V</p>
      <p>τ = R × C = <strong>{fmtSeconds(t)}</strong>. 5τ = {fmtSeconds(5 * t)}.</p>
      <p>
        {mode === 'charge' ? 'Charging' : 'Discharging'}: {tn.toFixed(1)}τ ({fmtSeconds(tn * t)}) after the switch,
        {Math.round(pct)}% of the way.
      </p>
    </div>
  </div>

  <svg class="plot" viewBox="0 0 {W} {H}" role="img" aria-label="Capacitor voltage over time, {mode === 'charge' ? 'rising towards 3.3' : 'falling towards 0'} volts. Now {v.toFixed(2)} volts at {tn.toFixed(1)} tau.">
    {#each [0, 1.65, 3.3] as g (g)}
      <line class="grid" x1={L} x2={W - 8} y1={py(g)} y2={py(g)} />
      <text class="t" x={L - 4} y={py(g) + 4} text-anchor="end">{g}</text>
    {/each}
    {#each [1, 5] as n (n)}
      <line class="mark" x1={px(n)} x2={px(n)} y1="8" y2={H - B} />
      <text class="t" x={px(n)} y={H - 8} text-anchor="middle">{n === 1 ? 'τ' : '5τ'} = {fmtSeconds(n * t)}</text>
      <circle class="pt" cx={px(n)} cy={py(capVoltage(n, 1, target, v0))} r="3" />
    {/each}
    <polyline class="ghost" points={full} />
    <polyline class="curve" points={done} />
    <circle class="now" cx={px(tn)} cy={py(v)} r="5" />
  </svg>

  <label class="slider">
    <span>Time since the switch: <strong>{tn.toFixed(1)}τ</strong> ({fmtSeconds(tn * t)})</span>
    <input type="range" min="0" max={SPAN} step="0.1" value={tn} oninput={(e) => { stop(); tn = Number(e.currentTarget.value); }} />
  </label>
  <div class="sliders">
    <label class="slider">
      <span>Resistor R: <strong>{fmtOhms(r)}</strong></span>
      <input type="range" min="0" max={RS.length - 1} step="1" bind:value={ri} />
    </label>
    <label class="slider">
      <span>Capacitor C: <strong>{capUnits(c * 1e12).best}</strong></span>
      <input type="range" min="0" max={CS.length - 1} step="1" bind:value={ci} />
    </label>
  </div>
  <p class="note">
    After 1τ a charge is {percentAfter(1).toFixed(1)}% done; after 5τ it is {percentAfter(5).toFixed(1)}%, which counts as full.
    Ideal parts: real capacitors leak a little, and electrolytics can be 20% off their marked value.
  </p>
</div>

<style>
  .switch {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin-bottom: var(--space-3);
  }
  .switch button {
    font: inherit;
    font-size: var(--fs-base);
    padding: 4px 12px;
    border-radius: 999px;
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  .switch button.on {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .layout {
    display: grid;
    grid-template-columns: minmax(0, 300px) 1fr;
    gap: var(--space-4);
    align-items: center;
  }
  svg {
    display: block;
    width: 100%;
    height: auto;
  }
  .w {
    fill: none;
    stroke: var(--text);
    stroke-width: 2;
    stroke-linecap: round;
    stroke-linejoin: round;
  }
  .w.thin {
    stroke-width: 1.5;
  }
  .w.plate {
    stroke-width: 3;
  }
  .lever {
    stroke: var(--accent);
    stroke-width: 3;
    stroke-linecap: round;
  }
  .term {
    fill: var(--bg-panel);
    stroke: var(--text);
    stroke-width: 1.5;
  }
  .fill {
    fill: var(--accent);
    opacity: 0.6;
  }
  .t {
    font-family: var(--font-mono);
    font-size: 10px;
    fill: var(--text-muted);
  }
  .t.strong {
    fill: var(--text);
    font-weight: 700;
  }
  .plot {
    margin-top: var(--space-3);
  }
  .plot .t {
    font-size: 11px;
  }
  .grid {
    stroke: var(--border);
    stroke-width: 1;
  }
  .mark {
    stroke: var(--text-muted);
    stroke-dasharray: 4 3;
  }
  .ghost {
    fill: none;
    stroke: var(--border-strong);
    stroke-width: 1.5;
    stroke-dasharray: 2 3;
  }
  .curve {
    fill: none;
    stroke: var(--accent);
    stroke-width: 3;
    stroke-linejoin: round;
  }
  .pt {
    fill: var(--text-muted);
  }
  .now {
    fill: var(--accent);
    stroke: var(--bg-panel);
    stroke-width: 2;
  }
  .readout p {
    margin: 0 0 var(--space-1);
  }
  .big {
    font-size: var(--fs-xl);
    font-weight: 700;
  }
  .sliders {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--space-4);
  }
  .slider {
    display: grid;
    gap: var(--space-1);
    margin: var(--space-2) 0;
    font-size: var(--fs-base);
    font-weight: 400;
    color: var(--text);
  }
  input[type='range'] {
    accent-color: var(--accent);
  }
  input[type='range']:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
  .note {
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  @media (max-width: 640px) {
    .layout,
    .sliders {
      grid-template-columns: 1fr;
    }
  }
</style>
