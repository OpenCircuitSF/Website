<script lang="ts">
  // Pick a Pi 4 HDMI port, when the screen comes on and the cable; see whether
  // there is a picture and how to force one. Logic in piHardware.ts (#0724).
  import { HDMI_CABLES, HDMI_PORTS, SCREEN_ON, hdmiOutcome, type HdmiCableId, type HdmiPortId, type ScreenOnId } from './piHardware';

  let port = $state<HdmiPortId>('hdmi0');
  let screenOn = $state<ScreenOnId>('before');
  let cable = $state<HdmiCableId>('microCable');

  const r = $derived(hdmiOutcome({ port, screenOn, cable }));
  const EDGE = [
    { id: 'usbc', label: 'USB-C power', x: 20 },
    { id: 'hdmi0', label: 'HDMI0', x: 80 },
    { id: 'hdmi1', label: 'HDMI1', x: 140 },
    { id: 'audio', label: 'audio', x: 200 },
  ];
  const cx = $derived(EDGE.find((e) => e.id === port)!.x + 25);
  const SCREEN_TEXT = { yes: 'picture', maybe: 'maybe?', no: 'no signal' };
</script>

<div class="ph">
  <!-- #0727: illustration slot -->
  <div class="group" role="radiogroup" aria-label="HDMI port">
    {#each HDMI_PORTS as p (p.id)}
      <button type="button" role="radio" aria-checked={port === p.id} class:on={port === p.id} onclick={() => (port = p.id)}>{p.label}</button>
    {/each}
  </div>
  <div class="group" role="radiogroup" aria-label="Screen switched on">
    {#each SCREEN_ON as s (s.id)}
      <button type="button" role="radio" aria-checked={screenOn === s.id} class:on={screenOn === s.id} onclick={() => (screenOn = s.id)}>{s.label}</button>
    {/each}
  </div>
  <div class="group" role="radiogroup" aria-label="Cable">
    {#each HDMI_CABLES as c (c.id)}
      <button type="button" role="radio" aria-checked={cable === c.id} class:on={cable === c.id} onclick={() => (cable = c.id)}>{c.label}</button>
    {/each}
  </div>

  <figure>
    <svg viewBox="0 0 400 200" role="img" aria-label="Pi 4 {port === 'hdmi0' ? 'HDMI0' : 'HDMI1'} to the monitor: {SCREEN_TEXT[r.picture]}. {r.title}">
      <rect x="10" y="20" width="250" height="90" rx="8" class="board" />
      {#each EDGE as e (e.id)}
        <rect x={e.x} y="90" width="50" height="20" rx="3" class="port" class:sel={e.id === port} />
        <text x={e.x + 25} y="128" class="t" text-anchor="middle">{e.label}</text>
      {/each}
      <path d="M{cx},110 L{cx},160 L300,160 L300,120" class="lead" class:dashed={r.picture === 'no'} />
      <rect x="280" y="30" width="110" height="80" rx="4" class="monitor" />
      <rect x="288" y="38" width="94" height="64" class="screen {r.picture}" />
      <text x="335" y="74" class="t" text-anchor="middle">{SCREEN_TEXT[r.picture]}</text>
    </svg>
  </figure>

  <div class="out {r.tone}" aria-live="polite">
    <h3>{r.title}</h3>
    <p>{r.body}</p>
    {#if r.fixLine}
      <pre><code>sudo nano /boot/firmware/cmdline.txt
# add to the end of the one line:
{r.fixLine}</code></pre>
      <p>
        Linux calls the ports HDMI-A-1 and HDMI-A-2; Raspberry Pi calls them 0 and 1. The old hdmi_force_hotplug setting in config.txt was
        for the legacy graphics driver; Bookworm uses KMS, where it isn’t implemented.
      </p>
    {/if}
  </div>
  <p class="links">
    See also <a href="/guides/pi-zero-usb-ports">the Zero’s mini-HDMI and USB ports</a> and
    <a href="/guides/pi-bookworm-changes">what else moved in Bookworm</a>.
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
  .board,
  .monitor {
    fill: var(--bg-panel);
    stroke: var(--border-strong);
    stroke-width: 1.5;
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
  .lead {
    fill: none;
    stroke: var(--text);
    stroke-width: 3;
  }
  .lead.dashed {
    stroke-dasharray: 4 3;
  }
  .screen {
    stroke: var(--border);
    stroke-width: 1.5;
  }
  .screen.yes {
    fill: var(--accent);
    fill-opacity: 0.2;
  }
  .screen.maybe {
    fill: var(--bg-panel);
    stroke: var(--warning);
    stroke-dasharray: 4 3;
  }
  .screen.no {
    fill: var(--text-faint);
    fill-opacity: 0.35;
  }
  .t {
    font-family: var(--font-mono);
    font-size: 11px;
    fill: var(--text);
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
  .out pre {
    overflow-x: auto;
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
