<script lang="ts">
  // Current and supply sizing for HUB75 panels. Fully static: nothing here moves or blinks (WCAG 2.3.1).
  // Logic in piProjects2.ts (#0726).
  import {
    MAX_PANELS,
    PANEL_CONTENT,
    PANEL_SIZES,
    POWER_TITLES,
    SUPPLY_CHOICES_A,
    amps,
    panelPower,
    type ContentId,
    type PanelSizeId,
  } from './piProjects2';

  let size = $state<PanelSizeId>('64x32');
  let panels = $state(2);
  let brightness = $state(100);
  let content = $state<ContentId>('image');
  let supplyA = $state(4);

  const p = $derived(panelPower({ size, panels, brightness, content, supplyA }));
  const body = $derived.by(() => {
    if (p.status === 'over')
      return `The supply can’t deliver ${amps(p.totalA)}. Expect dimming, colour shifts or a supply that shuts down. Lower the brightness or fit a bigger supply.`;
    if (p.status === 'whiteOver') return 'Your content fits, but one white frame would overload it. Size for full white unless you control every frame.';
    if (p.status === 'noRoom') return 'Supplies run hot at their limit; leave about 20% spare.';
    return 'Room to spare, even at full white.';
  });

  const scaleMax = $derived(Math.max(supplyA, p.worstTotalA) * 1.1);
  const sx = (a: number) => 10 + (a / scaleMax) * 380;
  const PW = 60;
  const panelX = (i: number) => 140 + i * 68;
</script>

<!-- #0727: illustration slot -->
<div class="rp">
  <div class="group" role="radiogroup" aria-label="Panel size">
    {#each PANEL_SIZES as s (s.id)}
      <button type="button" role="radio" aria-checked={size === s.id} class:on={size === s.id} onclick={() => (size = s.id)}>{s.label}</button>
    {/each}
  </div>
  <div class="group" role="radiogroup" aria-label="What the panels show">
    {#each PANEL_CONTENT as c (c.id)}
      <button type="button" role="radio" aria-checked={content === c.id} class:on={content === c.id} onclick={() => (content = c.id)}>{c.label}</button>
    {/each}
  </div>
  <div class="group" role="radiogroup" aria-label="Supply rating">
    {#each SUPPLY_CHOICES_A as a (a)}
      <button type="button" role="radio" aria-checked={supplyA === a} class:on={supplyA === a} onclick={() => (supplyA = a)}>{a} A</button>
    {/each}
  </div>
  <label class="range">
    Panels
    <input type="range" min="1" max={MAX_PANELS} step="1" bind:value={panels} />
    <output>{panels}</output>
  </label>
  <label class="range">
    Brightness (--led-brightness)
    <input type="range" min="10" max="100" step="10" bind:value={brightness} />
    <output>{brightness}%</output>
  </label>

  <p class="big">{amps(p.totalA)}</p>
  <p class="lbl">Full white at this brightness: {amps(p.worstTotalA)}</p>

  <figure>
    <svg viewBox="0 0 400 60" role="img" aria-label="Current {amps(p.totalA)}, full white {amps(p.worstTotalA)}, supply {supplyA} A. {POWER_TITLES[p.status]}.">
      <line class="axis" x1="10" y1="40" x2="390" y2="40" />
      <rect class="worst" x="10" y="16" width={sx(p.worstTotalA) - 10} height="20" />
      <rect class="now" class:over={p.status === 'over'} x="10" y="16" width={sx(p.totalA) - 10} height="20" />
      <line class="supply" x1={sx(supplyA)} y1="8" x2={sx(supplyA)} y2="44" />
      <text class="t" x={sx(supplyA)} y="56" text-anchor="middle">supply {supplyA} A</text>
    </svg>
  </figure>

  <div class="status {p.tone}" aria-live="polite">
    <h3>{POWER_TITLES[p.status]}</h3>
    <p>{body}</p>
  </div>

  <figure>
    <svg viewBox="0 0 560 200" role="img" aria-label="A 5 V supply feeds every panel with its own leads; the Pi shares only ground and never powers the panels.">
      <rect class="box" x="8" y="40" width="80" height="60" rx="4" />
      <text class="t" x="48" y="74" text-anchor="middle">5 V supply</text>
      <line class="bus plus" x1="88" y1="52" x2="550" y2="52" />
      <line class="bus gnd" x1="88" y1="88" x2="550" y2="88" />
      <text class="t" x="96" y="46">+5 V</text>
      <text class="t" x="96" y="102">GND</text>
      {#each Array.from({ length: panels }, (_, i) => i) as i (i)}
        <line class="lead plus" x1={panelX(i) + 20} y1="52" x2={panelX(i) + 20} y2="14" />
        <line class="lead gnd" x1={panelX(i) + 40} y1="88" x2={panelX(i) + 40} y2="14" />
        <rect class="panel" x={panelX(i)} y="2" width={PW} height="14" />
      {/each}
      <text class="t" x="140" y="120">power injected at every panel</text>
      <rect class="box" x="300" y="150" width="60" height="36" rx="4" />
      <text class="t" x="330" y="172" text-anchor="middle">Pi 4</text>
      <line class="thin" x1="330" y1="150" x2="330" y2="88" />
      <text class="t" x="336" y="138">shared ground</text>
      <line class="never" x1="300" y1="168" x2="200" y2="168" />
      <line class="cross" x1="244" y1="160" x2="256" y2="176" />
      <line class="cross" x1="256" y1="160" x2="244" y2="176" />
      <text class="t" x="296" y="196" text-anchor="end">5 V pin: never power panels from here</text>
    </svg>
  </figure>

  <ul class="advice">
    <li>{p.recommendedA !== null ? `Supply: at least ${p.recommendedA} A at 5 V` : 'More than one supply: split the panels between them'}</li>
    <li>Each panel’s leads: {p.panelWire}</li>
    <li>{p.mainWire !== null ? `Supply to the first panel: ${p.mainWire}` : 'Too much for one wire: run separate leads from the supply to each group of panels'}</li>
    <li>Join the supply’s ground to a Pi ground pin. Never power panels from the Pi’s 5 V pin.</li>
  </ul>
  <p class="lbl">Datasheet-typical currents (a 64-wide panel about 4 A at full white); check your panel’s label. Content figures are illustrative.</p>

  <p class="see-also">
    See also: <a href="/guides/led-power-budget">Power budgets for LED strips</a>,
    <a href="/guides/pi-rgb-matrix-basics">Rows, chains and parallel</a>,
    <a href="/guides/pi-undervoltage">The Pi’s own power supply</a>.
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
  .range {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    font-size: var(--fs-base);
    color: var(--text);
    margin-bottom: var(--space-3);
  }
  figure {
    margin: 0 0 var(--space-2);
  }
  svg {
    display: block;
    width: 100%;
    max-width: 560px;
    height: auto;
  }
  .big {
    font-size: var(--fs-xl, 1.6rem);
    font-weight: 700;
    margin: 0;
  }
  .axis {
    stroke: var(--border);
  }
  .worst {
    fill: none;
    stroke: var(--text-faint);
    stroke-dasharray: 3 3;
  }
  .now {
    fill: var(--accent);
  }
  .now.over {
    fill: var(--danger);
  }
  .supply {
    stroke: var(--text);
    stroke-width: 2;
  }
  .t {
    font-size: 10px;
    fill: var(--text-faint);
  }
  .box,
  .panel {
    fill: var(--bg-panel);
    stroke: var(--border);
  }
  .panel {
    stroke: var(--accent);
  }
  .bus {
    stroke-width: 5;
  }
  .lead {
    stroke-width: 2.5;
  }
  .plus {
    stroke: var(--danger);
  }
  .gnd {
    stroke: var(--text);
  }
  .thin {
    stroke: var(--text);
    stroke-width: 1;
  }
  .never {
    stroke: var(--danger);
    stroke-dasharray: 4 4;
  }
  .cross {
    stroke: var(--danger);
    stroke-width: 2;
  }
  .status h3 {
    margin: var(--space-3) 0 var(--space-1);
    font-size: var(--fs-lg);
  }
  .status p {
    margin: 0 0 var(--space-1);
    font-size: var(--fs-md);
  }
  .status.good h3 {
    color: var(--accent);
  }
  .status.meh h3 {
    color: var(--warning);
  }
  .status.bad h3 {
    color: var(--danger);
  }
  .advice {
    font-size: var(--fs-md);
  }
  .lbl {
    color: var(--text-muted);
    font-size: var(--fs-sm);
  }
  .see-also {
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  .see-also a {
    color: var(--accent);
  }
</style>
