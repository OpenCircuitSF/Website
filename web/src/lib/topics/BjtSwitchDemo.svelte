<script lang="ts">
  // An ESP32-C3 pin switching a 5 V load through an NPN transistor. Toggle
  // the pin, pick the base resistor and the load current, and watch the base
  // current, Ic/Ib, Vce and the heat. Model in transistors.ts.
  import { BASE_RESISTORS, BJTS, FORCED_GAIN, LOAD_CURRENTS, amps, bjtSwitch, ohms, watts, type BjtId } from './transistors';

  let partId = $state<BjtId>('PN2222A');
  let pinHigh = $state(true);
  let rbIdx = $state(BASE_RESISTORS.indexOf(470));
  let loadIdx = $state(LOAD_CURRENTS.indexOf(0.05));

  const part = $derived(BJTS[partId]);
  const rb = $derived(BASE_RESISTORS[rbIdx]);
  const load = $derived(LOAD_CURRENTS[loadIdx]);
  const r = $derived(bjtSwitch(part, pinHigh, rb, load));
  const glow = $derived(r.icWanted > 0 ? Math.min(1, r.ic / r.icWanted) : 0);

  const HEAD = { off: 'Off', saturated: 'Saturated: fully on', marginal: 'On, but not guaranteed', linear: 'Not saturated' } as const;
  const BADGE = { off: 'Load off', saturated: 'Good switch', marginal: 'Marginal', linear: 'Too little base current' } as const;
  const tone = $derived(r.state === 'saturated' || r.state === 'off' ? 'ok' : r.state === 'marginal' ? 'warn' : 'bad');
</script>

<div class="bjt">
  <div class="group" role="radiogroup" aria-label="Transistor">
    {#each Object.values(BJTS) as p (p.id)}
      <button type="button" role="radio" aria-checked={partId === p.id} class:on={partId === p.id} onclick={() => (partId = p.id)}>{p.label}</button>
    {/each}
  </div>

  <div class="layout">
    <svg viewBox="0 0 320 210" role="img" aria-label="Circuit: GPIO 4 through a {ohms(rb)} base resistor to an NPN transistor switching a {amps(load)} load on 5 V. The pin is {pinHigh ? 'high' : 'low'} and the load is {r.ic > 0 ? 'on' : 'off'}.">
      <!-- 5 V rail and the load -->
      <text x="230" y="14" class="t">+5 V (USB)</text>
      <line x1="220" y1="18" x2="220" y2="34" class="w pwr" />
      <rect x="200" y="34" width="40" height="46" rx="4" class="load" style:--glow={glow} />
      <text x="248" y="56" class="t">load</text>
      <text x="248" y="70" class="t num">{amps(r.ic)}</text>
      <line x1="220" y1="80" x2="220" y2="104" class="w" class:live={r.ic > 0} />
      <!-- transistor -->
      <circle cx="205" cy="128" r="24" class="body" />
      <line x1="194" y1="114" x2="194" y2="142" class="leg" />
      <line x1="194" y1="120" x2="220" y2="104" class="leg" />
      <line x1="194" y1="136" x2="220" y2="152" class="leg" />
      <polygon points="220,152 209,151 215,143" class="arrow" />
      <text x="226" y="104" class="t pin">C</text>
      <text x="226" y="160" class="t pin">E</text>
      <text x="180" y="112" class="t pin">B</text>
      <text x="236" y="132" class="t num">Vce {r.vce.toFixed(2)} V</text>
      <!-- emitter to ground -->
      <line x1="220" y1="152" x2="220" y2="186" class="w gnd" />
      <line x1="206" y1="186" x2="234" y2="186" class="w gnd" />
      <line x1="211" y1="192" x2="229" y2="192" class="w gnd" />
      <line x1="216" y1="198" x2="224" y2="198" class="w gnd" />
      <!-- base drive -->
      <rect x="6" y="114" width="64" height="30" rx="4" class="pinbox" class:high={pinHigh} />
      <text x="38" y="127" class="t mid">GPIO 4</text>
      <text x="38" y="139" class="t mid num">{pinHigh ? '3.3 V' : '0 V'}</text>
      <line x1="70" y1="128" x2="100" y2="128" class="w" class:live={r.ib > 0} />
      <rect x="100" y="120" width="44" height="16" rx="2" class="res" />
      <text x="122" y="112" class="t mid">{ohms(rb)}</text>
      <line x1="144" y1="128" x2="194" y2="128" class="w" class:live={r.ib > 0} />
      <text x="122" y="154" class="t mid num">Ib {amps(r.ib)}</text>
    </svg>

    <figure class="pkg">
      <svg viewBox="0 0 100 110" role="img" aria-label="{part.label} in TO-92, flat face toward you: pins {part.pins.join(', ')} from left to right">
        <path d="M18,10 L82,10 L82,52 Q50,66 18,52 Z" class="body flat" />
        <text x="50" y="34" class="t mid">{part.id}</text>
        {#each part.pins as p, i (i)}
          <line x1={32 + i * 18} y1="58" x2={32 + i * 18} y2="92" class="leg" />
          <text x={32 + i * 18} y="106" class="t mid pin">{p}</text>
        {/each}
      </svg>
      <figcaption>Flat face toward you. {part.pinNote}</figcaption>
    </figure>
  </div>

  <button type="button" class="toggle" aria-pressed={pinHigh} onclick={() => (pinHigh = !pinHigh)}>
    GPIO 4 is {pinHigh ? 'HIGH' : 'LOW'}: tap to set it {pinHigh ? 'LOW' : 'HIGH'}
  </button>

  <label class="slider">
    <span>Base resistor: <strong>{ohms(rb)}</strong></span>
    <input type="range" min="0" max={BASE_RESISTORS.length - 1} step="1" bind:value={rbIdx} aria-valuetext={ohms(rb)} />
  </label>
  <label class="slider">
    <span>Load current: <strong>{amps(load)}</strong> at 5 V</span>
    <input type="range" min="0" max={LOAD_CURRENTS.length - 1} step="1" bind:value={loadIdx} aria-valuetext={amps(load)} />
  </label>

  <dl class="cells">
    <div><dt>Base current</dt><dd class:warn={r.pinLevel === 'high'} class:bad={r.pinLevel === 'too-high'}>{amps(r.ib)}</dd></div>
    <div><dt>Ic / Ib needed</dt><dd>{r.state === 'off' ? '–' : r.gainNeeded.toFixed(0)}</dd></div>
    <div><dt>hFE (datasheet min)</dt><dd>{r.hfeMin.toFixed(0)}</dd></div>
    <div><dt>Vce</dt><dd>{r.vce.toFixed(2)} V</dd></div>
    <div><dt>Heat in it</dt><dd class:bad={r.overPower}>{watts(r.power)}</dd></div>
  </dl>

  <div class="result {tone}" aria-live="polite">
    <p class="badge">{BADGE[r.state]}</p>
    <h3>{HEAD[r.state]}</h3>
    {#if r.state === 'off'}
      <p>No base current, so no collector current. The whole 5 V sits across the transistor and the load is dark.</p>
    {:else if r.state === 'saturated'}
      <p>Ic/Ib is {r.gainNeeded.toFixed(1)}, at or below {FORCED_GAIN}. The transistor is fully on, with only {r.vce.toFixed(2)} V across it, the most its datasheet allows at this current.</p>
    {:else if r.state === 'marginal'}
      <p>
        Ic/Ib is {r.gainNeeded.toFixed(0)}. That’s under the minimum gain of {r.hfeMin.toFixed(0)}, so it should switch on, but the
        datasheet only promises a low Vce at Ic/Ib = {FORCED_GAIN}, so the real Vce may be higher than shown.
        {#if r.suggestedRb}Use {ohms(r.suggestedRb)} or less.{/if}
      </p>
    {:else}
      <p>
        The base current can only hold up {amps(r.ic)}, but the load wants {amps(r.icWanted)}. The rest of the 5 V drops across the transistor,
        which turns {watts(r.power)} into heat.
        {#if r.suggestedRb}Use {ohms(r.suggestedRb)} or less.{:else}This load needs too much base current: use a MOSFET.{/if}
      </p>
    {/if}
    {#if r.pinLevel === 'too-high'}
      <p class="warnline">{amps(r.ib)} is more than an ESP32-C3 pin should give. Use a bigger base resistor.</p>
    {:else if r.pinLevel === 'high'}
      <p class="warnline">{amps(r.ib)} from the pin works, but keep it under about 10 mA if you can.</p>
    {/if}
    {#if r.overCurrent}
      <p class="warnline">{amps(r.ic)} is more than the {part.label}’s {amps(part.icMax)} rating.</p>
    {/if}
  </div>
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
  .group button.on,
  .toggle[aria-pressed='true'] {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .toggle {
    margin: var(--space-3) 0;
  }
  .layout {
    display: grid;
    grid-template-columns: minmax(0, 3fr) minmax(0, 1fr);
    gap: var(--space-4);
    align-items: center;
  }
  @media (max-width: 560px) {
    .layout {
      grid-template-columns: 1fr;
    }
    .pkg svg {
      max-width: 120px;
    }
  }
  svg {
    display: block;
    width: 100%;
    height: auto;
  }
  .pkg {
    margin: 0;
  }
  figcaption {
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  .t {
    font-family: var(--font-mono);
    font-size: 11px;
    fill: var(--text);
  }
  .t.mid {
    text-anchor: middle;
  }
  .t.num {
    fill: var(--text-muted);
  }
  .t.pin {
    font-weight: 700;
  }
  .w {
    stroke: var(--text-muted);
    stroke-width: 2;
    fill: none;
  }
  .w.live {
    stroke: var(--accent);
    stroke-width: 3;
  }
  .w.pwr {
    stroke: var(--guide-wire-5v);
  }
  .w.gnd {
    stroke: var(--text);
  }
  .leg {
    stroke: var(--text);
    stroke-width: 2;
  }
  .arrow {
    fill: var(--text);
  }
  .body {
    fill: var(--bg-panel);
    stroke: var(--border-strong);
    stroke-width: 2;
  }
  .load {
    stroke: var(--border-strong);
    stroke-width: 2;
    fill: color-mix(in srgb, var(--warning) calc(var(--glow) * 85%), var(--bg-panel));
  }
  .res,
  .pinbox {
    fill: var(--bg-panel);
    stroke: var(--border-strong);
    stroke-width: 2;
  }
  .pinbox.high {
    stroke: var(--accent);
  }
  .slider {
    display: grid;
    gap: var(--space-1);
    margin: 0 0 var(--space-3);
    font-size: var(--fs-md);
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
  dd.warn {
    color: var(--warning);
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
  .result.warn {
    border-left-color: var(--warning);
  }
  .result.bad {
    border-left-color: var(--danger);
  }
  .badge {
    margin: 0;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }
  .result h3 {
    margin: var(--space-1) 0;
    font-size: var(--fs-lg);
  }
  .result p {
    margin: 0 0 var(--space-2);
  }
  .result p:last-child {
    margin: 0;
  }
  .warnline {
    color: var(--danger);
    font-weight: 600;
  }
</style>
