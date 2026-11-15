<script lang="ts">
  // A cutaway USB-C cable between a laptop and a board. Switch between a
  // charge-only and a data cable, plug it in, and see what each end notices.
  // The board's power light comes on either way -- which is exactly why the
  // charge-only cable is so confusing.
  type Cable = 'charge' | 'data';
  let cable = $state<Cable>('charge');
  let plugged = $state(false);

  const WIRES = [
    { id: 'vbus', label: 'VBUS (5V)', cls: 'w-power', both: true },
    { id: 'gnd', label: 'GND', cls: 'w-gnd', both: true },
    { id: 'cc', label: 'CC', cls: 'w-cc', both: true },
    { id: 'dp', label: 'D+', cls: 'w-data', both: false },
    { id: 'dm', label: 'D−', cls: 'w-data', both: false },
  ];

  const hasData = $derived(cable === 'data');
  const present = $derived(WIRES.filter((w) => w.both || hasData));

  function choose(c: Cable) {
    cable = c;
    plugged = false;
  }
</script>

<div class="cable-demo">
  <div class="controls">
    <div class="seg" role="radiogroup" aria-label="Cable type">
      <button type="button" role="radio" aria-checked={cable === 'charge'} class:on={cable === 'charge'} onclick={() => choose('charge')}>
        Charge-only cable
      </button>
      <button type="button" role="radio" aria-checked={cable === 'data'} class:on={cable === 'data'} onclick={() => choose('data')}>
        Data cable
      </button>
    </div>
    <button type="button" class="plug" onclick={() => (plugged = !plugged)}>
      {plugged ? 'Unplug' : 'Plug it in'}
    </button>
  </div>

  <div class="scene">
    <div class="laptop" class:on={plugged}>
      <div class="screen">
        {#if !plugged}
          <p class="dim">Waiting…</p>
        {:else if hasData}
          <p class="ok">● New device</p>
          <p class="mono">/dev/cu.usbmodem101</p>
          <p class="mono small">USB JTAG/serial debug unit</p>
          <p class="ok small">PlatformIO can upload ✓</p>
        {:else}
          <p class="bad">No new device</p>
          <p class="mono small">Upload: no serial port found</p>
          <p class="dim small">The laptop has no idea anything is plugged in.</p>
        {/if}
      </div>
      <div class="base"></div>
      <span class="caption">Laptop</span>
    </div>

    <svg class="cable" viewBox="0 0 360 170" role="img" aria-label="Cutaway of the cable: {present.map((w) => w.label).join(', ')}">
      <rect class="jacket" x="0" y="15" width="360" height="140" rx="20" />
      {#each WIRES as w, i (w.id)}
        {@const y = 38 + i * 24}
        {#if w.both || hasData}
          <line class="wire {w.cls}" class:flow={plugged} x1="20" x2="340" y1={y} y2={y} />
          <text class="wlabel" x="180" y={y - 5}>{w.label}</text>
        {:else}
          <line class="missing" x1="20" x2="340" y1={y} y2={y} />
          <text class="wlabel missing-text" x="180" y={y - 5}>{w.label}: not in this cable</text>
        {/if}
      {/each}
    </svg>

    <div class="board">
      <div class="pcb">
        <span class="pwr" class:on={plugged} title="power light"></span>
        <span class="chip">C3</span>
      </div>
      <span class="caption">Board {plugged ? '· power light on' : ''}</span>
    </div>
  </div>

  <p class="verdict" aria-live="polite">
    {#if !plugged}
      Pick a cable and plug it in. Both look identical from the outside.
    {:else if hasData}
      Power <em>and</em> data. The board lights up and the laptop finds a serial port to upload to.
    {:else}
      The board's power light comes on, so everything looks fine. But with no D+ and D− wires, the laptop never hears from
      it.
    {/if}
  </p>
</div>

<style>
  .controls {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
    margin-bottom: var(--space-4);
  }
  .seg {
    display: inline-flex;
    border: var(--border-w) solid var(--border-strong);
    border-radius: var(--radius);
    overflow: hidden;
  }
  .seg button {
    font: inherit;
    font-size: var(--fs-md);
    padding: var(--space-2) var(--space-3);
    border: 0;
    background: var(--bg-panel);
    color: var(--text-muted);
    cursor: pointer;
  }
  .seg button.on {
    background: var(--accent);
    color: var(--accent-text);
  }
  .plug {
    font: inherit;
    font-size: var(--fs-md);
    font-weight: 700;
    padding: var(--space-2) var(--space-4);
    border-radius: 999px;
    border: 0;
    background: var(--text);
    color: var(--bg-page);
    cursor: pointer;
  }
  .scene {
    display: grid;
    grid-template-columns: 180px 1fr 120px;
    gap: var(--space-2);
    align-items: center;
  }
  @media (max-width: 640px) {
    .scene {
      grid-template-columns: 1fr;
    }
  }
  .caption {
    display: block;
    margin-top: var(--space-1);
    text-align: center;
    font-size: var(--fs-sm);
    color: var(--text-faint);
  }
  .screen {
    min-height: 110px;
    padding: var(--space-2) var(--space-3);
    background: var(--bg-page);
    border: 6px solid var(--guide-pcb-edge);
    border-radius: 8px 8px 2px 2px;
  }
  .screen p {
    margin: 0 0 4px;
    font-size: var(--fs-base);
  }
  .base {
    height: 10px;
    margin: 0 -10px;
    background: var(--guide-pcb-edge);
    border-radius: 0 0 8px 8px;
  }
  .mono {
    font-family: var(--font-mono);
  }
  .small {
    font-size: var(--fs-sm) !important;
  }
  .ok {
    color: var(--accent);
    font-weight: 700;
  }
  .bad {
    color: var(--danger);
    font-weight: 700;
  }
  .dim {
    color: var(--text-faint);
  }
  .cable {
    width: 100%;
    height: auto;
  }
  .jacket {
    fill: var(--guide-pcb);
    stroke: var(--guide-pcb-edge);
    stroke-width: 2;
  }
  .wire {
    stroke-width: 7;
    stroke-linecap: round;
  }
  .w-power {
    stroke: var(--guide-wire-5v);
  }
  .w-gnd {
    stroke: var(--guide-wire-gnd);
  }
  .w-cc {
    stroke: var(--text-faint);
  }
  .w-data {
    stroke: var(--guide-wire-data);
  }
  .wire.flow {
    stroke-dasharray: 14 8;
    animation: flow 0.7s linear infinite;
  }
  .w-data.flow {
    animation-direction: alternate;
    animation-duration: 0.4s;
  }
  @keyframes flow {
    to {
      stroke-dashoffset: -22;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .wire.flow {
      animation: none;
    }
  }
  .missing {
    stroke: var(--danger);
    stroke-width: 1.5;
    stroke-dasharray: 3 5;
    opacity: 0.6;
  }
  .wlabel {
    fill: var(--text-faint);
    font-family: var(--font-mono);
    font-size: 10px;
    text-anchor: middle;
  }
  .missing-text {
    fill: var(--danger);
  }
  .pcb {
    position: relative;
    height: 90px;
    background: var(--guide-pcb);
    border: 2px solid var(--guide-pcb-edge);
    border-radius: 6px;
    display: grid;
    place-items: center;
  }
  .chip {
    width: 40px;
    height: 40px;
    display: grid;
    place-items: center;
    background: var(--guide-pcb-edge);
    color: var(--text-faint);
    font-family: var(--font-mono);
    font-size: 11px;
    transform: rotate(45deg);
  }
  .pwr {
    position: absolute;
    top: 10px;
    left: 10px;
    width: 10px;
    height: 10px;
    border-radius: 2px;
    background: var(--guide-led-off);
  }
  .pwr.on {
    background: var(--guide-wire-5v);
    box-shadow: 0 0 10px 3px var(--guide-wire-5v);
  }
  .verdict {
    margin: var(--space-4) 0 0;
    font-size: var(--fs-md);
    max-width: var(--measure);
  }
</style>
