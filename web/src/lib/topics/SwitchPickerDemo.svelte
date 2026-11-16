<script lang="ts">
  // BJT or MOSFET? Set the load current, the supply and what drives the
  // switch, and see which works, which doesn’t and why, drawn as the two
  // circuits side by side. Rules in transistors.ts.
  import { PICK_CURRENTS, PICK_DRIVES, PICK_SUPPLIES, amps, pickSwitch, watts } from './transistors';

  let currentIdx = $state(PICK_CURRENTS.indexOf(0.05));
  let supply = $state(5);
  let drive = $state<number>(3.3);
  let throughHole = $state(true);

  const load = $derived(PICK_CURRENTS[currentIdx]);
  const r = $derived(pickSwitch(load, supply, drive, throughHole));
  const DRIVE_LABEL: Record<number, string> = { 3.3: '3.3 V pin (ESP32)', 5: '5 V pin (Arduino Uno)' };
  // Heat bars, scaled so 2 W fills the bar.
  const bar = (w: number) => Math.max(1, Math.min(100, (w / 2) * 100));
</script>

<div class="pick">
  <label class="slider">
    <span>Load current: <strong>{amps(load)}</strong></span>
    <input type="range" min="0" max={PICK_CURRENTS.length - 1} step="1" bind:value={currentIdx} aria-valuetext={amps(load)} />
  </label>
  <div class="group" role="radiogroup" aria-label="Load supply">
    {#each PICK_SUPPLIES as v (v)}
      <button type="button" role="radio" aria-checked={supply === v} class:on={supply === v} onclick={() => (supply = v)}>{v} V</button>
    {/each}
  </div>
  <div class="group" role="radiogroup" aria-label="Switched from">
    {#each PICK_DRIVES as d (d)}
      <button type="button" role="radio" aria-checked={drive === d} class:on={drive === d} onclick={() => (drive = d)}>{DRIVE_LABEL[d]}</button>
    {/each}
  </div>
  <label class="check">
    <input type="checkbox" bind:checked={throughHole} />
    Through-hole parts (for a breadboard)
  </label>

  <div class="pair">
    {#each [{ key: 'bjt', title: 'NPN transistor', part: '2N2222A', ok: r.bjt.ok, loss: r.bjt.loss, why: r.bjt.why }, { key: 'fet', title: 'Logic-level MOSFET', part: r.mosfet.part, ok: r.mosfet.ok, loss: r.mosfet.loss, why: r.mosfet.why }] as c (c.key)}
      <section class="card" class:ok={c.ok} aria-label="{c.title}: {c.ok ? 'works' : 'not suitable'}">
        <svg viewBox="0 0 140 120" role="img" aria-label="{c.title} {c.part} switching the load from the {drive} V pin">
          <text x="100" y="12" class="t">+{supply} V</text>
          <line x1="90" y1="16" x2="90" y2="26" class="w pwr" />
          <rect x="76" y="26" width="28" height="24" rx="3" class="load" class:lit={c.ok} />
          <line x1="90" y1="50" x2="90" y2="62" class="w" class:live={c.ok} />
          {#if c.key === 'bjt'}
            <circle cx="80" cy="76" r="15" class="body" />
            <line x1="73" y1="67" x2="73" y2="85" class="leg" />
            <line x1="73" y1="71" x2="90" y2="62" class="leg" />
            <line x1="73" y1="81" x2="90" y2="90" class="leg" />
            <rect x="34" y="72" width="26" height="8" rx="1" class="res" />
            <line x1="60" y1="76" x2="73" y2="76" class="w" class:live={c.ok} />
            <text x="47" y="94" class="t mid num">{amps(r.bjt.ib)}</text>
          {:else}
            <line x1="90" y1="62" x2="90" y2="66" class="w" class:live={c.ok} />
            <line x1="80" y1="66" x2="90" y2="66" class="leg" />
            <line x1="80" y1="76" x2="90" y2="76" class="leg" />
            <line x1="80" y1="86" x2="90" y2="86" class="leg" />
            <line x1="90" y1="76" x2="90" y2="86" class="leg" />
            <line x1="80" y1="63" x2="80" y2="89" class="ch" class:live={c.ok} />
            <line x1="74" y1="64" x2="74" y2="88" class="leg" />
            <rect x="34" y="84" width="26" height="8" rx="1" class="res" />
            <line x1="60" y1="88" x2="74" y2="88" class="w" />
            <text x="47" y="104" class="t mid num">~0 mA</text>
          {/if}
          <line x1="90" y1="90" x2="90" y2="108" class="w gnd" />
          <line x1="82" y1="108" x2="98" y2="108" class="w gnd" />
          <rect x="2" y={c.key === 'bjt' ? 68 : 80} width="30" height="16" rx="2" class="pinbox" />
          <text x="17" y={c.key === 'bjt' ? 79 : 91} class="t mid">{drive}V</text>
          <text x="118" y="44" class="t mid mark" class:bad={!c.ok}>{c.ok ? '✓' : '✗'}</text>
        </svg>
        <h4>{c.title} <span class="part">{c.part}</span></h4>
        <p class="why">{c.why}</p>
        <div class="heat" aria-hidden="true"><span style:width="{bar(c.loss)}%" class:bad={c.loss > 1}></span></div>
        <p class="loss">Heat in the switch: {watts(c.loss)}</p>
      </section>
    {/each}
  </div>

  <div class="result {r.pick}" aria-live="polite">
    <h3>{r.headline}</h3>
    <p class="part-line">{r.part}</p>
    <ul>
      {#each r.reasons as reason (reason)}<li>{reason}</li>{/each}
    </ul>
  </div>
</div>

<style>
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
  .check {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    margin: 0 0 var(--space-3);
    font-size: var(--fs-base);
    font-weight: 400;
    color: var(--text);
  }
  .pair {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
    gap: var(--space-3);
    margin-bottom: var(--space-3);
  }
  .card {
    padding: var(--space-3);
    border: 2px solid var(--border);
    border-radius: 8px;
    background: var(--bg-panel);
    opacity: 0.75;
    min-width: 0;
  }
  .card.ok {
    border-color: var(--accent);
    opacity: 1;
  }
  svg {
    display: block;
    width: 100%;
    max-width: 220px;
    height: auto;
  }
  .t {
    font-family: var(--font-mono);
    font-size: 9px;
    fill: var(--text);
  }
  .t.mid {
    text-anchor: middle;
  }
  .t.num {
    fill: var(--text-muted);
  }
  .t.mark {
    font-size: 22px;
    fill: var(--accent);
  }
  .t.mark.bad {
    fill: var(--danger);
  }
  .w {
    stroke: var(--text-muted);
    stroke-width: 2;
    fill: none;
  }
  .w.live,
  .ch.live {
    stroke: var(--accent);
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
  .body,
  .res,
  .pinbox,
  .load {
    fill: var(--bg-page);
    stroke: var(--border-strong);
    stroke-width: 1.5;
  }
  .load.lit {
    fill: color-mix(in srgb, var(--warning) 70%, var(--bg-page));
  }
  h4 {
    margin: var(--space-2) 0 var(--space-1);
    font-size: var(--fs-md);
  }
  .part {
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  .why,
  .loss {
    margin: 0 0 var(--space-2);
    font-size: var(--fs-base);
  }
  .loss {
    margin: 0;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  .heat {
    height: 8px;
    margin-bottom: var(--space-1);
    border-radius: 4px;
    background: var(--bg-header);
    overflow: hidden;
  }
  .heat span {
    display: block;
    height: 100%;
    background: var(--warning);
  }
  .heat span.bad {
    background: var(--danger);
  }
  .result {
    padding: var(--space-3) var(--space-4);
    border-left: 4px solid var(--accent);
    background: var(--bg-panel);
    font-size: var(--fs-md);
  }
  .result.neither {
    border-left-color: var(--danger);
  }
  .result h3 {
    margin: 0;
    font-size: var(--fs-lg);
  }
  .part-line {
    margin: var(--space-1) 0;
    font-family: var(--font-mono);
    font-weight: 700;
  }
  .result ul {
    margin: 0;
    padding-left: var(--space-5);
  }
</style>
