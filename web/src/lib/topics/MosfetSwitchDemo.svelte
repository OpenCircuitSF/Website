<script lang="ts">
  // A MOSFET as a low-side switch. Pick a part, set the gate, the gate
  // voltage, the load, and whether there’s a pull-down, and see Rds(on), the
  // heat in the MOSFET and how much of the load turns on. Model in transistors.ts.
  import { GATE_DRIVES, MOSFETS, MOSFET_LOADS, amps, mosfetSwitch, watts, type GateState, type MosfetId } from './transistors';

  let partId = $state<MosfetId>('AO3400A');
  let gate = $state<GateState>('high');
  let drive = $state<number>(3.3);
  let pulldown = $state(true);
  let loadIdx = $state(MOSFET_LOADS.indexOf(1));
  let supply = $state(5);

  const part = $derived(MOSFETS[partId]);
  const load = $derived(MOSFET_LOADS[loadIdx]);
  const r = $derived(mosfetSwitch(part, gate, drive, pulldown, load, supply));
  const share = $derived(r.idWanted > 0 ? r.id / r.idWanted : 0);

  const GATES: { id: GateState; label: string }[] = [
    { id: 'low', label: 'Pin LOW' },
    { id: 'high', label: 'Pin HIGH' },
    { id: 'floating', label: 'Board booting (pin floats)' },
  ];
  const DRIVE_LABEL: Record<number, string> = { 3.3: '3.3 V (ESP32 pin)', 5: '5 V', 10: '10 V (gate driver)' };

  const head = $derived(
    r.state === 'off'
      ? 'Off'
      : r.state === 'unknown'
        ? 'Floating: it could be half on'
        : r.state === 'on'
          ? 'Fully on'
          : share < 0.5
            ? 'Barely on'
            : 'Only partly on',
  );
  const tone = $derived(r.state === 'off' || (r.state === 'on' && (r.heat === 'cool' || r.heat === 'warm')) ? 'ok' : r.state === 'unknown' || r.heat === 'hot' ? 'warn' : 'bad');
  const rdsText = (x: number) => (x >= 1 ? `${x.toFixed(1)} Ω` : `${(x * 1000).toFixed(x < 0.03 ? 1 : 0)} mΩ`);
</script>

<div class="fet">
  <div class="group" role="radiogroup" aria-label="MOSFET">
    {#each Object.values(MOSFETS) as p (p.id)}
      <button type="button" role="radio" aria-checked={partId === p.id} class:on={partId === p.id} onclick={() => (partId = p.id)}>
        {p.label} <span class="v">{p.logicLevel ? 'logic level' : 'standard'}</span>
      </button>
    {/each}
  </div>
  <div class="group" role="radiogroup" aria-label="Gate">
    {#each GATES as g (g.id)}
      <button type="button" role="radio" aria-checked={gate === g.id} class:on={gate === g.id} onclick={() => (gate = g.id)}>{g.label}</button>
    {/each}
  </div>
  <div class="group" role="radiogroup" aria-label="Gate voltage when HIGH">
    {#each GATE_DRIVES as d (d)}
      <button type="button" role="radio" aria-checked={drive === d} class:on={drive === d} onclick={() => (drive = d)}>{DRIVE_LABEL[d]}</button>
    {/each}
  </div>

  <svg viewBox="0 0 320 210" role="img" aria-label="Circuit: a {amps(load)} load on {supply} V, switched by an {part.label} whose gate is driven through 100 Ω{pulldown ? ' with a 10 kΩ pull-down' : ''}. {head}.">
    <text x="230" y="14" class="t">+{supply} V</text>
    <line x1="220" y1="18" x2="220" y2="34" class="w pwr" />
    <rect x="200" y="34" width="40" height="46" rx="4" class="load" style:--glow={share} />
    <text x="248" y="56" class="t">load</text>
    <text x="248" y="70" class="t num">{amps(r.id)}</text>
    <line x1="220" y1="80" x2="220" y2="104" class="w" class:live={r.id > 0} />
    <!-- N-channel enhancement MOSFET -->
    <line x1="220" y1="104" x2="220" y2="114" class="w" class:live={r.id > 0} />
    <line x1="204" y1="114" x2="220" y2="114" class="leg" />
    <line x1="204" y1="128" x2="220" y2="128" class="leg" />
    <line x1="204" y1="142" x2="220" y2="142" class="leg" />
    <line x1="220" y1="128" x2="220" y2="142" class="leg" />
    <polygon points="204,128 212,124 212,132" class="arrow" />
    <line x1="204" y1="110" x2="204" y2="118" class="ch" class:onch={r.state === 'on' || r.state === 'partly'} />
    <line x1="204" y1="124" x2="204" y2="132" class="ch" class:onch={r.state === 'on' || r.state === 'partly'} />
    <line x1="204" y1="138" x2="204" y2="146" class="ch" class:onch={r.state === 'on' || r.state === 'partly'} />
    <line x1="198" y1="112" x2="198" y2="144" class="leg" />
    <text x="226" y="112" class="t pin">D</text>
    <text x="226" y="160" class="t pin">S</text>
    <text x="186" y="108" class="t pin">G</text>
    <text x="236" y="132" class="t num">{r.state === 'on' || r.state === 'partly' ? `Rds ${rdsText(r.rds)}` : 'open'}</text>
    <line x1="220" y1="142" x2="220" y2="186" class="w gnd" />
    <line x1="206" y1="186" x2="234" y2="186" class="w gnd" />
    <line x1="211" y1="192" x2="229" y2="192" class="w gnd" />
    <line x1="216" y1="198" x2="224" y2="198" class="w gnd" />
    <!-- gate drive: pin, 100 Ω, pull-down -->
    <rect x="6" y="129" width="64" height="30" rx="4" class="pinbox" class:high={gate === 'high'} />
    <text x="38" y="142" class="t mid">{drive === 3.3 ? 'GPIO 4' : 'driver'}</text>
    <text x="38" y="154" class="t mid num">{gate === 'high' ? `${drive} V` : gate === 'low' ? '0 V' : '?'}</text>
    <line x1="70" y1="144" x2="96" y2="144" class="w" />
    <rect x="96" y="136" width="36" height="16" rx="2" class="res" />
    <text x="114" y="130" class="t mid">100 Ω</text>
    <line x1="132" y1="144" x2="198" y2="144" class="w" />
    {#if pulldown}
      <line x1="160" y1="144" x2="160" y2="158" class="w" />
      <rect x="152" y="158" width="16" height="24" rx="2" class="res" />
      <text x="146" y="174" class="t end">10 kΩ</text>
      <line x1="160" y1="182" x2="160" y2="198" class="w gnd" />
      <line x1="150" y1="198" x2="170" y2="198" class="w gnd" />
    {:else}
      <text x="160" y="176" class="t mid num">no pull-down</text>
    {/if}
  </svg>

  <label class="check">
    <input type="checkbox" bind:checked={pulldown} />
    10 kΩ pull-down from gate to ground
  </label>
  <div class="group" role="radiogroup" aria-label="Load supply">
    {#each [5, 12] as v (v)}
      <button type="button" role="radio" aria-checked={supply === v} class:on={supply === v} onclick={() => (supply = v)}>{v} V supply</button>
    {/each}
  </div>
  <label class="slider">
    <span>Load current: <strong>{amps(load)}</strong></span>
    <input type="range" min="0" max={MOSFET_LOADS.length - 1} step="1" bind:value={loadIdx} aria-valuetext={amps(load)} />
  </label>

  <dl class="cells">
    <div><dt>Vgs</dt><dd>{Number.isNaN(r.vgs) ? '?' : `${r.vgs} V`}</dd></div>
    <div><dt>Rds(on)</dt><dd>{r.state === 'on' || r.state === 'partly' ? rdsText(r.rds) : '–'}{r.rdsEstimate ? ' *' : ''}</dd></div>
    <div><dt>Load gets</dt><dd>{Math.round(share * 100)}%</dd></div>
    <div><dt>Heat in it</dt><dd class:warn={r.heat === 'hot'} class:bad={r.heat === 'too-hot'}>{watts(r.power)}</dd></div>
    <div><dt>Temperature</dt><dd class:warn={r.heat === 'hot'} class:bad={r.heat === 'too-hot'}>{Math.round(r.tj)} °C</dd></div>
  </dl>

  <div class="result {tone}" aria-live="polite">
    <p class="badge">{part.label}, {part.pkg}</p>
    <h3>{head}</h3>
    {#if r.state === 'off'}
      <p>{gate === 'floating' ? 'The pull-down holds the gate at 0 V while the pin floats, so the load stays off during boot.' : 'With 0 V on the gate the channel is open. No current flows.'}</p>
    {:else if r.state === 'unknown'}
      <p>While the board boots, the pin isn’t driven. The gate holds whatever charge it last had, so the load might be off, on, or half on and heating the MOSFET. Add a 10 kΩ pull-down.</p>
    {:else if r.state === 'on'}
      <p>
        With {drive} V on the gate, Rds(on) is about {rdsText(r.rds)}. At {amps(r.id)} that’s {watts(r.power)} of heat, so it sits near {Math.round(r.tj)} °C with no heatsink.
        {#if r.heat === 'hot'}That’s hot: add copper or a heatsink.{:else if r.heat === 'too-hot'}Too hot: it needs a heatsink or a bigger part.{/if}
      </p>
    {:else}
      <p>
        {drive} V isn’t enough gate voltage for the {part.label}. It passes only about {amps(r.id)}, acting like {rdsText(r.rds)}, so the load gets {Math.round(share * 100)}% and the MOSFET turns {watts(r.power)} into heat.
        {#if !part.logicLevel}It’s a standard part, specified at 10 V on the gate. Use a logic-level one.{/if}
      </p>
    {/if}
    {#if r.overVoltage}<p class="warnline">{supply} V is more than its {part.vdsMax} V rating.</p>{/if}
    {#if r.overCurrent}<p class="warnline">{amps(r.id)} is more than its {part.idMax} A rating.</p>{/if}
    {#if r.rdsEstimate && r.state !== 'off'}<p class="note">* An estimate: the datasheet doesn’t guarantee a value at {drive} V.</p>{/if}
  </div>
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
  .v {
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    opacity: 0.8;
  }
  svg {
    display: block;
    width: 100%;
    max-width: 420px;
    height: auto;
    margin-bottom: var(--space-3);
  }
  .t {
    font-family: var(--font-mono);
    font-size: 11px;
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
  .ch {
    stroke: var(--text-muted);
    stroke-width: 3;
  }
  .ch.onch {
    stroke: var(--accent);
  }
  .arrow {
    fill: var(--text);
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
  .check {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    margin: 0 0 var(--space-3);
    font-size: var(--fs-base);
    font-weight: 400;
    color: var(--text);
  }
  .slider {
    display: grid;
    gap: var(--space-1);
    margin: 0 0 var(--space-3);
    font-size: var(--fs-md);
    font-weight: 400;
    color: var(--text);
  }
  input[type='range'],
  input[type='checkbox'] {
    accent-color: var(--accent);
  }
  input:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
  .cells {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(110px, 1fr));
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
  .note {
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
</style>
