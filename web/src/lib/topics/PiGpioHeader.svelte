<script lang="ts">
  // The 40-pin GPIO header (#0723): tap a pin to see what it does, switch
  // between physical and GPIO numbers, and highlight pins by function.
  // Every fact is in piStart.ts.
  import { PI_HEADER, gpiozeroLine, pinByPhysical, pinLabel, pinTitle, type HeaderPin, type PinFunction, type PinScheme } from './piStart';

  type Filter = PinFunction | 'Power' | 'Ground';
  const FILTERS: Filter[] = ['I2C', 'SPI', 'UART', 'PWM', 'I2S', 'HAT ID', 'Power', 'Ground'];

  let scheme = $state<PinScheme>('physical');
  let selected = $state<HeaderPin>(pinByPhysical(11));
  let filter = $state<Filter | null>(null);

  const line = $derived(gpiozeroLine(selected));
  const gpioNum = $derived(selected.gpio ?? 17);

  function matches(p: HeaderPin) {
    if (filter === null) return true;
    if (filter === 'Power') return p.kind === 'power5' || p.kind === 'power33';
    if (filter === 'Ground') return p.kind === 'ground';
    return p.functions.includes(filter);
  }
  function pos(p: HeaderPin): [number, number] {
    const row = Math.floor((p.physical - 1) / 2);
    return [p.physical % 2 === 1 ? 110 : 150, 20 + row * 21];
  }
</script>

<!-- #0727: illustration slot -->
<div class="explorer">
  <div class="controls">
    <div class="seg" role="group" aria-label="Pin numbers">
      <button type="button" class:on={scheme === 'physical'} aria-pressed={scheme === 'physical'} onclick={() => (scheme = 'physical')}>Physical</button>
      <button type="button" class:on={scheme === 'gpio'} aria-pressed={scheme === 'gpio'} onclick={() => (scheme = 'gpio')}>GPIO (BCM)</button>
    </div>
    <div class="filters" role="group" aria-label="Highlight pins">
      {#each FILTERS as f (f)}
        <button type="button" class="chip" class:on={filter === f} aria-pressed={filter === f} onclick={() => (filter = filter === f ? null : f)}>{f}</button>
      {/each}
    </div>
  </div>

  <div class="layout">
    <svg viewBox="0 0 260 440" role="group" aria-label="The Raspberry Pi 40-pin GPIO header. {pinTitle(selected)} is selected.">
      <rect x="95" y="6" width="70" height="428" rx="4" class="hdr" />
      {#each PI_HEADER as p (p.physical)}
        {@const [x, y] = pos(p)}
        {@const odd = p.physical % 2 === 1}
        <g
          class="pin {p.kind}"
          class:sel={selected.physical === p.physical}
          class:dim={!matches(p)}
          role="button"
          tabindex="0"
          aria-label={pinTitle(p)}
          aria-pressed={selected.physical === p.physical}
          onclick={() => (selected = p)}
          onkeydown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), (selected = p))}
        >
          <circle class="hit" cx={x} cy={y} r="11" />
          {#if p.kind === 'power5' || p.kind === 'power33'}
            <rect class="shape" x={x - 6} y={y - 6} width="12" height="12" />
          {:else if p.kind === 'ground'}
            <circle class="shape" cx={x} cy={y} r="6" />
            <line class="gbar" x1={x - 6} y1={y} x2={x + 6} y2={y} />
          {:else}
            <circle class="shape" cx={x} cy={y} r="6" />
          {/if}
          <text class="plbl" x={odd ? x - 14 : x + 14} y={y + 4} text-anchor={odd ? 'end' : 'start'}>{pinLabel(p, scheme)}</text>
        </g>
      {/each}
    </svg>

    <div class="info" aria-live="polite">
      <p class="kind">{selected.kind === 'gpio' ? 'GPIO' : selected.kind === 'ground' ? 'ground' : 'power'}</p>
      <h3>{pinTitle(selected)}</h3>
      <ul class="tags">
        {#each selected.roles as t, i (i)}<li>{t}</li>{/each}
      </ul>
      <ul class="facts">
        {#each selected.facts as f, i (i)}<li>{f}</li>{/each}
      </ul>
      {#if selected.warning}
        <p class="warn" role="note">⚠ {selected.warning}</p>
      {/if}
      <div class="legend" aria-hidden="true">
        <span><svg viewBox="0 0 12 12"><circle cx="6" cy="6" r="5" class="lg gpio" /></svg>GPIO</span>
        <span><svg viewBox="0 0 12 12"><rect x="1" y="1" width="10" height="10" class="lg power" /></svg>power</span>
        <span><svg viewBox="0 0 12 12"><circle cx="6" cy="6" r="5" class="lg ground" /><line x1="1" y1="6" x2="11" y2="6" class="lgbar" /></svg>ground</span>
      </div>
    </div>
  </div>

  <h4>On the Pi</h4>
  <pre><code>pinout            # GPIO Zero prints this map
pinctrl get {gpioNum}    # what GPIO {gpioNum} is doing now</code></pre>
  {#if line}
    <pre><code>from gpiozero import LED
{line}
led.on()</code></pre>
  {:else}
    <p class="note">Pick a GPIO pin to see the gpiozero line.</p>
  {/if}
  <p class="note">Lighting an LED? <a href="/guides/led-resistor-calculator">pick the LED’s resistor</a>.</p>
</div>

<style>
  .controls {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
    align-items: center;
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
    font-size: var(--fs-base);
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
  .filters {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }
  .chip {
    font: inherit;
    font-size: var(--fs-base);
    padding: 4px 10px;
    border-radius: 999px;
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  .chip.on {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .layout {
    display: grid;
    grid-template-columns: minmax(0, 260px) 1fr;
    gap: var(--space-5);
    align-items: start;
  }
  @media (max-width: 720px) {
    .layout {
      grid-template-columns: 1fr;
    }
  }
  .layout > svg {
    display: block;
    width: 100%;
    max-width: 260px;
    margin: 0 auto;
  }
  .hdr {
    fill: var(--bg-panel);
    stroke: var(--border-strong);
  }
  .pin {
    cursor: pointer;
    outline: none;
  }
  .pin.dim {
    opacity: 0.2;
  }
  .hit {
    fill: transparent;
  }
  .shape {
    stroke-width: 2;
    fill: var(--bg-panel);
  }
  .gpio .shape {
    stroke: var(--accent);
  }
  .power5 .shape,
  .power33 .shape {
    stroke: var(--warning);
  }
  .ground .shape {
    stroke: var(--text-faint);
  }
  .gbar {
    stroke: var(--text-faint);
    stroke-width: 2;
  }
  .plbl {
    fill: var(--text);
    font: 10px var(--font-mono);
  }
  .pin:hover .shape,
  .pin:focus-visible .shape {
    stroke-width: 4;
  }
  .pin:focus-visible .hit {
    stroke: var(--accent);
    stroke-width: 2;
  }
  .pin.sel .shape {
    stroke-width: 4;
    fill: color-mix(in srgb, var(--accent) 45%, transparent);
  }
  @media (prefers-reduced-motion: no-preference) {
    .pin {
      transition: opacity 0.2s;
    }
  }
  .info {
    padding: var(--space-4);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
    background: var(--bg-panel);
  }
  .kind {
    margin: 0;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--accent);
  }
  h3 {
    margin: var(--space-1) 0 var(--space-2);
    font-size: var(--fs-xl);
  }
  .tags {
    list-style: none;
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1);
    margin: 0 0 var(--space-3);
    padding: 0;
  }
  .tags li {
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    padding: 2px 8px;
    border-radius: 999px;
    background: var(--accent-subtle);
    color: var(--accent);
  }
  .facts {
    margin: 0 0 var(--space-3);
    padding-left: var(--space-5);
    font-size: var(--fs-md);
  }
  .facts li {
    margin-bottom: var(--space-2);
  }
  .warn {
    margin: 0 0 var(--space-3);
    padding: var(--space-2) var(--space-3);
    border: 2px solid var(--danger);
    border-radius: 6px;
    font-weight: 600;
  }
  .legend {
    display: flex;
    gap: var(--space-4);
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  .legend svg {
    width: 12px;
    height: 12px;
    margin-right: 4px;
    vertical-align: -1px;
  }
  .lg {
    fill: none;
    stroke-width: 2;
  }
  .lg.gpio {
    stroke: var(--accent);
  }
  .lg.power {
    stroke: var(--warning);
  }
  .lg.ground,
  .lgbar {
    stroke: var(--text-faint);
    stroke-width: 2;
  }
  h4 {
    margin: var(--space-4) 0 var(--space-1);
  }
  .note {
    font-size: var(--fs-sm);
    color: var(--text-faint);
  }
  pre {
    margin: var(--space-2) 0 0;
    padding: var(--space-3);
    overflow-x: auto;
    background: var(--bg-panel);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
  }
</style>
