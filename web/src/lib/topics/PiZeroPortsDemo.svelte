<script lang="ts">
  // Plug a device into one of the Zero 2W's three ports and see what happens.
  // Logic in piHardware.ts (#0724).
  import { ZERO_DEVICES, ZERO_PORTS, plugZero, type ZeroDeviceId, type ZeroPortId } from './piHardware';

  let device = $state<ZeroDeviceId>('keyboard');
  let port = $state<ZeroPortId>('pwr');
  let adapter = $state(true);

  const dev = $derived(ZERO_DEVICES.find((d) => d.id === device)!);
  const needsAdapter = $derived(dev.plug === 'usbA');
  const r = $derived(plugZero(device, port, adapter));

  const PORT_X: Record<ZeroPortId, number> = { hdmi: 110, usb: 250, pwr: 300 };
  const SILK: Record<ZeroPortId, string> = { hdmi: 'mini HDMI', usb: 'USB', pwr: 'PWR IN' };
  const px = $derived(PORT_X[port]);
  const pins = Array.from({ length: 20 }, (_, i) => 110 + i * 9);
</script>

<div class="pz">
  <!-- #0727: illustration slot -->
  <div class="group" role="radiogroup" aria-label="Device">
    {#each ZERO_DEVICES as d (d.id)}
      <button type="button" role="radio" aria-checked={device === d.id} class:on={device === d.id} onclick={() => (device = d.id)}>{d.label}</button>
    {/each}
  </div>
  <div class="group" role="radiogroup" aria-label="Port">
    {#each ZERO_PORTS as p (p.id)}
      <button type="button" role="radio" aria-checked={port === p.id} class:on={port === p.id} onclick={() => (port = p.id)}>{p.label}</button>
    {/each}
  </div>
  <label class="check">
    <input type="checkbox" bind:checked={adapter} disabled={!needsAdapter} />
    OTG adapter on the plug (micro-USB to USB-A)
  </label>

  <figure>
    <svg viewBox="0 0 400 220" role="img" aria-label="{dev.label} into {SILK[port]}: {r.title}">
      <rect x="50" y="10" width="300" height="120" rx="12" class="board" />
      {#each pins as x (x)}
        <circle cx={x} cy="22" r="2" class="pin" />
        <circle cx={x} cy="31" r="2" class="pin" />
      {/each}
      {#each ZERO_PORTS as p (p.id)}
        <rect x={PORT_X[p.id] - 15} y="116" width="30" height="14" rx="2" class="port" class:sel={port === p.id} />
        <text x={PORT_X[p.id]} y="108" class="t" text-anchor="middle">{SILK[p.id]}</text>
      {/each}
      {#if r.fits}
        <line x1={px} y1="130" x2={px} y2="170" class="lead" />
      {:else}
        <line x1={px} y1="134" x2={px} y2="170" class="lead dashed" />
        <path d="M{px - 6},144 l12,12 M{px + 6},144 l-12,12" class="cross" />
        <text x={px + 12} y="155" class="t danger">doesn’t fit</text>
      {/if}
      <rect x={Math.min(Math.max(px - 90, 5), 215)} y="170" width="180" height="34" rx="6" class="box" />
      <text x={Math.min(Math.max(px, 95), 305)} y="191" class="t" text-anchor="middle">{dev.label}</text>
    </svg>
  </figure>

  <div class="out {r.tone}" aria-live="polite">
    <h3>{r.title}</h3>
    <p>{r.body}</p>
  </div>
  <p class="links">
    Power goes in PWR IN, everything else in USB. See also <a href="/guides/pi-undervoltage">a supply that holds 5 V</a>,
    <a href="/guides/pi-zero-audio-out">sound from a Zero</a> and <a href="/guides/pi-models-compared">Zero 2W, Pi 4 or Pi 5?</a>
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
  .check {
    display: block;
    margin-bottom: var(--space-3);
    font-size: var(--fs-base);
  }
  .links {
    margin: 0 0 var(--space-3);
    font-size: var(--fs-base);
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
  .board {
    fill: var(--bg-panel);
    stroke: var(--accent);
    stroke-width: 1.5;
  }
  .pin {
    fill: var(--text-faint);
  }
  .port {
    fill: var(--bg-panel);
    stroke: var(--border-strong);
    stroke-width: 1.5;
  }
  .port.sel {
    stroke: var(--accent);
    stroke-width: 3;
  }
  .box {
    fill: var(--bg-panel);
    stroke: var(--border-strong);
    stroke-width: 1.5;
  }
  .lead {
    stroke: var(--text);
    stroke-width: 3;
  }
  .lead.dashed {
    stroke-dasharray: 4 3;
  }
  .cross {
    stroke: var(--danger);
    stroke-width: 2.5;
  }
  .t {
    font-family: var(--font-mono);
    font-size: 11px;
    fill: var(--text);
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
  .out h3 {
    margin: var(--space-1) 0;
    font-size: var(--fs-lg);
    color: var(--accent);
  }
  .out.meh h3 {
    color: var(--warning);
  }
  .out.bad h3 {
    color: var(--danger);
  }
</style>
