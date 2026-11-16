<script lang="ts">
  // Switch a coil off with no diode, a diode the right way round (band, the
  // cathode, to the positive supply) or a diode reversed, and plot the
  // voltage at the transistor. A teaching model, in transistors.ts.
  import { COILS, ON_VCE, SWITCH_RATING, flyback, flybackTrace, type CoilId, type DiodeMode } from './transistors';

  let coilId = $state<CoilId>('relay');
  let diode = $state<DiodeMode>('none');
  let on = $state(true);
  let runs = $state(0);

  const coil = $derived(COILS[coilId]);
  const r = $derived(flyback(coil, diode));
  const trace = $derived(flybackTrace(coil, diode, 120));

  const DIODES: { id: DiodeMode; label: string }[] = [
    { id: 'none', label: 'No diode' },
    { id: 'correct', label: 'Diode, band (cathode) to +' },
    { id: 'reversed', label: 'Diode reversed' },
  ];

  // Plot: 0 to 45 V up the side; the first 10% of the width is "still on".
  const VMAX = 45;
  const X0 = 34;
  const XOFF = 64;
  const X1 = 292;
  const Y0 = 112;
  const Y1 = 10;
  const y = (v: number) => Y0 - (v / VMAX) * (Y0 - Y1);
  const points = $derived(trace.map((p, k) => `${(XOFF + (k / (trace.length - 1)) * (X1 - XOFF)).toFixed(1)},${y(p.v).toFixed(1)}`).join(' '));
  const span = $derived(5 * r.tau);
  const timeLabel = (s: number) => (s >= 0.001 ? `${(s * 1000).toFixed(1)} ms` : `${(s * 1e6).toFixed(0)} µs`);

  const head = $derived(
    on
      ? diode === 'reversed'
        ? 'Short circuit!'
        : 'On: current flows through the coil'
      : diode === 'correct'
        ? `Clamped at ${r.peak.toFixed(1)} V`
        : diode === 'none'
          ? 'Spike: the transistor breaks down'
          : 'Spike: the diode is backwards',
  );
  const tone = $derived((on && diode !== 'reversed') || (!on && diode === 'correct') ? 'ok' : 'bad');

  function toggle() {
    on = !on;
    if (!on) runs += 1;
  }
</script>

<div class="fly">
  <div class="group" role="radiogroup" aria-label="Coil">
    {#each Object.values(COILS) as c (c.id)}
      <button type="button" role="radio" aria-checked={coilId === c.id} class:on={coilId === c.id} onclick={() => ((coilId = c.id), (runs += 1))}>{c.label}</button>
    {/each}
  </div>
  <div class="group" role="radiogroup" aria-label="Diode">
    {#each DIODES as d (d.id)}
      <button type="button" role="radio" aria-checked={diode === d.id} class:on={diode === d.id} onclick={() => ((diode = d.id), (runs += 1))}>{d.label}</button>
    {/each}
  </div>

  <div class="layout">
    <svg viewBox="0 0 260 220" role="img" aria-label="Circuit: a {coil.label} from +{coil.supply} V to an NPN transistor, {diode === 'none' ? 'with no diode' : diode === 'correct' ? 'with a diode across the coil, band to the positive supply' : 'with a diode across the coil the wrong way round'}. The transistor is {on ? 'on' : 'off'}.">
      <text x="20" y="16" class="t">+{coil.supply} V</text>
      <line x1="20" y1="22" x2="210" y2="22" class="w pwr" />
      <!-- coil -->
      <line x1="110" y1="22" x2="110" y2="44" class="w" class:live={on && diode !== 'reversed'} />
      <path d="M110,44 a8,8 0 0 1 0,16 a8,8 0 0 1 0,16 a8,8 0 0 1 0,16 a8,8 0 0 1 0,16" class="coil" />
      <text x="80" y="80" class="t end">{coil.label.replace('Small ', '')}</text>
      <line x1="110" y1="108" x2="110" y2="134" class="w" class:live={on && diode !== 'reversed'} />
      <!-- diode across the coil -->
      {#if diode === 'none'}
        <rect x="186" y="50" width="48" height="44" rx="4" class="empty" />
        <text x="210" y="70" class="t mid num">no</text>
        <text x="210" y="82" class="t mid num">diode</text>
      {:else}
        <line x1="210" y1="22" x2="210" y2="56" class="w" class:loop={!on && diode === 'correct'} class:short={on && diode === 'reversed'} />
        {#if diode === 'correct'}
          <polygon points="198,88 222,88 210,58" class="diode" />
          <line x1="196" y1="57" x2="224" y2="57" class="band" />
          <text x="230" y="61" class="t">band</text>
        {:else}
          <polygon points="198,57 222,57 210,87" class="diode bad" />
          <line x1="196" y1="88" x2="224" y2="88" class="band" />
          <text x="230" y="92" class="t">band</text>
        {/if}
        <line x1="210" y1="88" x2="210" y2="122" class="w" class:loop={!on && diode === 'correct'} class:short={on && diode === 'reversed'} />
        <line x1="110" y1="122" x2="210" y2="122" class="w" class:loop={!on && diode === 'correct'} class:short={on && diode === 'reversed'} />
      {/if}
      <!-- transistor -->
      <circle cx="96" cy="152" r="20" class="body" />
      <line x1="86" y1="140" x2="86" y2="164" class="leg" />
      <line x1="86" y1="146" x2="110" y2="134" class="leg" />
      <line x1="86" y1="158" x2="110" y2="170" class="leg" />
      <line x1="110" y1="170" x2="110" y2="198" class="w gnd" />
      <line x1="98" y1="198" x2="122" y2="198" class="w gnd" />
      <line x1="103" y1="204" x2="117" y2="204" class="w gnd" />
      <line x1="40" y1="152" x2="86" y2="152" class="w" class:live={on} />
      <text x="36" y="156" class="t end">{on ? 'ON' : 'OFF'}</text>
      {#if !on && diode !== 'correct'}
        <text x="124" y="152" class="t spark">⚡ {Math.round(r.peak)} V</text>
      {/if}
      {#if on && diode === 'reversed'}
        <text x="124" y="152" class="t spark">short!</text>
      {/if}
    </svg>

    <svg viewBox="0 0 300 136" role="img" aria-label="Voltage at the transistor: {on ? `about ${ON_VCE} V while on` : `peaks at ${Math.round(r.peak)} V after switch-off`}">
      <line x1={X0} y1={Y0} x2={X1} y2={Y0} class="axis" />
      <line x1={X0} y1={Y1} x2={X0} y2={Y0} class="axis" />
      <line x1={X0} y1={y(SWITCH_RATING)} x2={X1} y2={y(SWITCH_RATING)} class="rating" />
      <text x={X1} y={y(SWITCH_RATING) - 3} class="t end bad">{SWITCH_RATING} V: transistor’s limit</text>
      <line x1={X0} y1={y(coil.supply)} x2={X1} y2={y(coil.supply)} class="supply" />
      <text x={X0 - 4} y={y(coil.supply) + 4} class="t end num">{coil.supply}</text>
      <text x={X0 - 4} y={Y0 + 4} class="t end num">0</text>
      <text x={X0 - 4} y={y(40) + 4} class="t end num">40</text>
      <text x={XOFF} y={Y0 + 14} class="t mid num">off</text>
      <text x={X1} y={Y0 + 14} class="t end num">{on ? '' : `+${timeLabel(span)}`}</text>
      <polyline points="{X0},{y(ON_VCE)} {XOFF},{y(ON_VCE)}" class="trace" />
      {#if on}
        <text x={(XOFF + X1) / 2} y={y(20)} class="t mid num">Switch it off to see the spike</text>
      {:else}
        {#key runs}
          <polyline points="{XOFF},{y(ON_VCE)} {points}" pathLength="1000" class="trace draw" class:bad={diode !== 'correct'} />
        {/key}
      {/if}
    </svg>
  </div>

  <button type="button" class="toggle" onclick={toggle}>{on ? 'Switch off' : 'Switch on'}</button>

  <dl class="cells">
    <div><dt>Coil current</dt><dd>{(r.i0 * 1000).toFixed(0)} mA</dd></div>
    <div><dt>Peak at transistor</dt><dd class:bad={!on && r.overRating}>{on ? '–' : `${r.peak.toFixed(1)} V`}</dd></div>
    <div><dt>Without a clamp</dt><dd>~{Math.round(r.unclamped)} V</dd></div>
    <div><dt>Stored energy</dt><dd>{(r.energy * 1000).toFixed(2)} mJ</dd></div>
  </dl>

  <div class="result {tone}" aria-live="polite">
    <h3>{head}</h3>
    {#if on && diode === 'reversed'}
      <p>A diode with its band at the transistor end conducts from +{coil.supply} V straight into the transistor whenever it’s on. It shorts the supply, and the diode or the transistor burns out.</p>
    {:else if on}
      <p>The coil stores energy in its magnetic field while current flows. A coil fights any change in its current, so watch what happens when the transistor stops it.</p>
    {:else if diode === 'correct'}
      <p>The coil keeps its {(r.i0 * 1000).toFixed(0)} mA flowing round the loop through the diode, and the voltage stops one diode drop above the supply. The current dies away in about {timeLabel(span)}.</p>
    {:else}
      <p>
        With nowhere to go, the coil drives the voltage up until something gives: on paper about {Math.round(r.unclamped)} V. The transistor’s
        {SWITCH_RATING} V limit is passed and it breaks down. Each spike does a little damage, and sooner or later it fails.
        {#if diode === 'reversed'}A reversed diode doesn’t help: the spike only pushes it harder the wrong way.{/if}
      </p>
    {/if}
  </div>
  <p class="note">A teaching model: coil inductance and the stray capacitance that sets the unclamped spike are typical guesses, not datasheet values.</p>
</div>

<style>
  .group {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin-bottom: var(--space-3);
  }
  .group button,
  .toggle {
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
  .toggle {
    margin: var(--space-3) 0;
    font-weight: 700;
  }
  .layout {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1.3fr);
    gap: var(--space-4);
    align-items: center;
  }
  @media (max-width: 600px) {
    .layout {
      grid-template-columns: 1fr;
    }
  }
  svg {
    display: block;
    width: 100%;
    height: auto;
  }
  .t {
    font-family: var(--font-mono);
    font-size: 10px;
    fill: var(--text);
  }
  .t.mid {
    text-anchor: middle;
  }
  .t.end {
    text-anchor: end;
  }
  .t.num {
    fill: var(--text-muted);
  }
  .t.bad,
  .t.spark {
    fill: var(--danger);
    font-weight: 700;
  }
  .w {
    stroke: var(--text-muted);
    stroke-width: 2;
    fill: none;
  }
  .w.live,
  .w.loop {
    stroke: var(--accent);
    stroke-width: 3;
  }
  .w.loop {
    stroke-dasharray: 6 4;
    animation: flow 0.6s linear infinite;
  }
  .w.short {
    stroke: var(--danger);
    stroke-width: 3;
  }
  .w.pwr {
    stroke: var(--guide-wire-5v);
  }
  .w.gnd {
    stroke: var(--text);
  }
  .coil {
    fill: none;
    stroke: var(--text);
    stroke-width: 2;
  }
  .leg {
    stroke: var(--text);
    stroke-width: 2;
  }
  .body {
    fill: var(--bg-panel);
    stroke: var(--border-strong);
    stroke-width: 2;
  }
  .empty {
    fill: none;
    stroke: var(--border-strong);
    stroke-dasharray: 4 3;
  }
  .diode {
    fill: var(--text);
  }
  .diode.bad {
    fill: var(--danger);
  }
  .band {
    stroke: var(--text);
    stroke-width: 4;
  }
  .axis {
    stroke: var(--border-strong);
    stroke-width: 1;
  }
  .rating {
    stroke: var(--danger);
    stroke-dasharray: 5 4;
  }
  .supply {
    stroke: var(--border);
    stroke-dasharray: 2 3;
  }
  .trace {
    fill: none;
    stroke: var(--accent);
    stroke-width: 2.5;
    stroke-linejoin: round;
  }
  .trace.bad {
    stroke: var(--danger);
  }
  .trace.draw {
    stroke-dasharray: 1000;
    animation: draw 1.2s ease-out both;
  }
  @keyframes draw {
    from {
      stroke-dashoffset: 1000;
    }
    to {
      stroke-dashoffset: 0;
    }
  }
  @keyframes flow {
    to {
      stroke-dashoffset: -20;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .trace.draw,
    .w.loop {
      animation: none;
    }
  }
  .cells {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));
    gap: var(--space-2);
    margin: 0 0 var(--space-3);
  }
  .cells div {
    padding: var(--space-2) var(--space-3);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
    background: var(--bg-panel);
    min-width: 0;
  }
  dt {
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  dd {
    margin: 0;
    font-family: var(--font-mono);
    font-size: var(--fs-lg);
    font-weight: 700;
  }
  dd.bad {
    color: var(--danger);
  }
  .result {
    padding: var(--space-3) var(--space-4);
    border-left: 4px solid var(--accent);
    background: var(--bg-panel);
    font-size: var(--fs-md);
  }
  .result.bad {
    border-left-color: var(--danger);
  }
  .result h3 {
    margin: 0 0 var(--space-1);
    font-size: var(--fs-lg);
  }
  .result p {
    margin: 0;
  }
  .note {
    margin: var(--space-2) 0 0;
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
</style>
