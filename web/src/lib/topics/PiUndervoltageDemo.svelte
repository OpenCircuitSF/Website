<script lang="ts">
  // Pick a board, a supply, a cable and a load; watch the voltage at the Pi
  // sag. Then decode get_throttled underneath. Logic in piPower.ts (#0720).
  import ThrottledDecoder from './ThrottledDecoder.svelte';
  import {
    BOARDS,
    CABLES,
    LOADS,
    SAG_TITLES,
    SUPPLIES,
    UNDERVOLTAGE_V,
    hex,
    sag,
    type BoardId,
    type CableId,
    type LoadId,
    type SupplyId,
  } from './piPower';

  let board = $state<BoardId>('pi4');
  let supply = $state<SupplyId>('charger');
  let cable = $state<CableId>('typical');
  let load = $state<LoadId>('busy');

  const s = $derived(sag({ board, supply, cable, load }));
  const boardInfo = $derived(BOARDS.find((b) => b.id === board)!);
  const captive = $derived(SUPPLIES.find((x) => x.id === supply)!.captive);

  const STROKE: Record<number, number> = { 18: 6, 20: 5, 22: 4, 24: 3, 26: 2, 28: 1.5 };
  const V_MIN = 3.6;
  const V_MAX = 5.3;
  const X0 = 30;
  const X1 = 370;
  const xOf = (v: number) => X0 + ((Math.min(V_MAX, Math.max(V_MIN, v)) - V_MIN) / (V_MAX - V_MIN)) * (X1 - X0);
  const f2 = (v: number) => v.toFixed(2);

  const SENTENCES = {
    ok: 'The Pi gets enough voltage even while it works hard.',
    marginal: 'Just above the line. A burst of work or a USB device plugged in can dip it under.',
    under: 'Below 4.63 V the firmware flags under-voltage and slows the processor down. Expect reboots and SD card errors.',
  };
</script>

<div class="pu">
  <!-- #0722: illustration slot -->
  <div class="group" role="radiogroup" aria-label="Board">
    {#each BOARDS as b (b.id)}
      <button type="button" role="radio" aria-checked={board === b.id} class:on={board === b.id} onclick={() => (board = b.id)}>{b.label}</button>
    {/each}
  </div>
  <p class="hint">Official supply for a {boardInfo.label}: {boardInfo.officialSupply}</p>
  <div class="group" role="radiogroup" aria-label="Power supply">
    {#each SUPPLIES as x (x.id)}
      <button type="button" role="radio" aria-checked={supply === x.id} class:on={supply === x.id} onclick={() => (supply = x.id)}>{x.label}</button>
    {/each}
  </div>
  <div class="group" role="radiogroup" aria-label="Cable">
    {#each CABLES as c (c.id)}
      <button
        type="button"
        role="radio"
        aria-checked={!captive && cable === c.id}
        class:on={!captive && cable === c.id}
        disabled={captive}
        onclick={() => (cable = c.id)}>{c.label}</button
      >
    {/each}
  </div>
  {#if captive}
    <p class="hint">The official supply has its own cable built in: 1.5 m, 18 AWG.</p>
  {/if}
  <div class="group" role="radiogroup" aria-label="What the Pi is doing">
    {#each LOADS as l (l.id)}
      <button type="button" role="radio" aria-checked={load === l.id} class:on={load === l.id} onclick={() => (load = l.id)}>{l.label}</button>
    {/each}
  </div>

  <figure>
    <svg
      viewBox="0 0 400 200"
      role="img"
      aria-label="Supply {f2(s.supplyV)} V, the cable loses {f2(s.cableDropV)} V, {f2(s.piV)} V at the Pi: {SAG_TITLES[s.status]}"
    >
      <rect x="10" y="30" width="80" height="44" rx="6" class="box" />
      <text x="50" y="48" class="t" text-anchor="middle">supply</text>
      <text x="50" y="64" class="t" text-anchor="middle">{f2(s.supplyV)} V</text>
      <line x1="90" y1="52" x2="290" y2="52" class="cable" stroke-width={STROKE[s.cable.awg]} />
      <text x="190" y="40" class="t muted" text-anchor="middle">{s.cable.lengthM} m, {s.cable.awg} AWG</text>
      <text x="190" y="74" class="t" text-anchor="middle">−{f2(s.cableDropV)} V</text>
      <rect x="290" y="30" width="80" height="44" rx="6" class="box pi" />
      <text x="330" y="48" class="t" text-anchor="middle">Pi</text>
      <text x="330" y="64" class="t" text-anchor="middle">{f2(s.piV)} V</text>
      {#if s.status === 'under'}
        <path d="M384,26 L376,46 L383,46 L378,64 L392,40 L385,40 L390,26 Z" class="bolt" />
      {/if}

      <line x1={X0} y1="150" x2={X1} y2="150" class="axis" />
      {#each [4, 4.5, 5] as v (v)}
        <line x1={xOf(v)} y1="146" x2={xOf(v)} y2="154" class="axis" />
        <text x={xOf(v)} y="168" class="t muted" text-anchor="middle">{v} V</text>
      {/each}
      <line x1={xOf(UNDERVOLTAGE_V)} y1="110" x2={xOf(UNDERVOLTAGE_V)} y2="160" class="limit" />
      <text x={xOf(UNDERVOLTAGE_V) - 4} y="106" class="t danger" text-anchor="end">under-voltage {UNDERVOLTAGE_V} V</text>
      <path d="M{xOf(s.piV)},146 l-6,-12 l12,0 Z" class="marker {s.status}" />
      <text x={xOf(s.piV)} y="186" class="t" text-anchor="middle">{f2(s.piV)} V</text>
    </svg>
  </figure>

  <div class="out {s.status}" aria-live="polite">
    <p class="big"><strong>{f2(s.piV)} V</strong> at the Pi</p>
    <h3>{SAG_TITLES[s.status]}</h3>
    <p>{SENTENCES[s.status]}</p>
    <p>
      <code>vcgencmd get_throttled</code> → <code>throttled={hex(s.throttled)}</code>{s.status === 'marginal' ? ' after a brief dip' : ''}
    </p>
  </div>
  <p class="note">A simplified model with typical values: real supplies, cables and boards vary.</p>
  <p class="links">
    The same sag resets ESP32s and dims LED strips: <a href="/guides/wifi-brownout-resets">WiFi brownout resets</a>,
    <a href="/guides/led-power-budget">LED power budget</a>. <a href="/guides/pi-sd-card-corruption">Under-voltage also corrupts SD cards</a>.
  </p>

  <ThrottledDecoder />
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
  .group button:disabled {
    opacity: 0.45;
    cursor: not-allowed;
  }
  .hint,
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
  .box {
    fill: var(--bg-panel);
    stroke: var(--border-strong);
    stroke-width: 1.5;
  }
  .cable {
    stroke: var(--text);
  }
  .axis {
    stroke: var(--text-muted);
    stroke-width: 1.5;
  }
  .limit {
    stroke: var(--danger);
    stroke-width: 1.5;
    stroke-dasharray: 4 3;
  }
  .bolt {
    fill: var(--warning);
  }
  .marker {
    fill: var(--accent);
  }
  .marker.marginal {
    fill: var(--warning);
  }
  .marker.under {
    fill: var(--danger);
  }
  .t {
    font-family: var(--font-mono);
    font-size: 11px;
    fill: var(--text);
  }
  .t.muted {
    fill: var(--text-muted);
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
  .out.marginal h3 {
    color: var(--warning);
  }
  .out.under h3 {
    color: var(--danger);
  }
</style>
