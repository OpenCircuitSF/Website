<script lang="ts">
  // Pick a power source and where it connects, and see whether that's
  // safe, works with limits, won't run, or can damage the board. Rules in
  // esp32c3PinsPower.ts.
  import { ENTRIES, SOURCES, powerResult, type Entry, type PowerVerdict, type Source } from './esp32c3PinsPower';

  let source = $state<Source>('usb-charger');
  let entry = $state<Entry>('usb-c');
  let usbToo = $state(false);

  const result = $derived(powerResult(source, entry, usbToo));
  const BADGE: Record<PowerVerdict, string> = { safe: 'Safe', works: 'Works, with limits', wont: 'Won’t run', damage: 'Damage risk' };
</script>

<div class="power">
  <div class="group" role="radiogroup" aria-label="Power source">
    {#each SOURCES as s (s.id)}
      <button type="button" role="radio" aria-checked={source === s.id} class:on={source === s.id} onclick={() => (source = s.id)}>
        {s.label} <span class="v">{s.volts}</span>
      </button>
    {/each}
  </div>
  <div class="group" role="radiogroup" aria-label="Connect it to">
    {#each ENTRIES as e (e.id)}
      <button type="button" role="radio" aria-checked={entry === e.id} class:on={entry === e.id} onclick={() => (entry = e.id)}>{e.label}</button>
    {/each}
  </div>
  <label class="check">
    <input type="checkbox" bind:checked={usbToo} />
    USB cable also plugged into a computer
  </label>

  <div class="layout">
    <svg viewBox="0 0 220 120" role="img" aria-label="Power path: {SOURCES.find((s) => s.id === source)?.label} into the {ENTRIES.find((e) => e.id === entry)?.label}">
      <rect x="4" y="40" width="64" height="40" rx="6" class="src" />
      <text x="36" y="64" class="lbl">{SOURCES.find((s) => s.id === source)?.volts}</text>
      <rect x="120" y="10" width="92" height="100" rx="6" class="board" />
      <text x="166" y="28" class="lbl">USB-C</text>
      <text x="166" y="62" class="lbl">5V → reg</text>
      <text x="166" y="96" class="lbl">3.3</text>
      <path
        d={entry === 'usb-c' ? 'M68,60 C95,60 95,24 120,24' : entry === '5v' ? 'M68,60 L120,58' : 'M68,60 C95,60 95,92 120,92'}
        class="wire {result.verdict}"
      />
    </svg>
    <div class="result {result.verdict}" aria-live="polite">
      <p class="badge">{BADGE[result.verdict]}</p>
      <h3>{result.title}</h3>
      <p>{result.body}</p>
    </div>
  </div>
  <p class="gnd">Whatever powers it, every ground connects together: the supply’s minus goes to G.</p>
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
  .check {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    margin: 0 0 var(--space-4);
    font-size: var(--fs-base);
    font-weight: 400;
    color: var(--text);
  }
  .layout {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
    gap: var(--space-4);
    align-items: center;
  }
  svg {
    display: block;
    width: 100%;
    max-width: 320px;
    height: auto;
  }
  .src,
  .board {
    fill: var(--bg-panel);
    stroke: var(--border-strong);
    stroke-width: 2;
  }
  .lbl {
    font-family: var(--font-mono);
    font-size: 11px;
    fill: var(--text);
    text-anchor: middle;
  }
  .wire {
    fill: none;
    stroke-width: 4;
    stroke: var(--accent);
  }
  .wire.works {
    stroke: var(--warning);
  }
  .wire.wont {
    stroke: var(--text-muted);
    stroke-dasharray: 6 5;
  }
  .wire.damage {
    stroke: var(--danger);
  }
  .result {
    padding: var(--space-3) var(--space-4);
    border-left: 4px solid var(--accent);
    background: var(--bg-panel);
    font-size: var(--fs-md);
  }
  .result.works {
    border-left-color: var(--warning);
  }
  .result.wont {
    border-left-color: var(--border-strong);
  }
  .result.damage {
    border-left-color: var(--danger);
  }
  .badge {
    margin: 0;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }
  .result.damage .badge {
    color: var(--danger);
  }
  .result h3 {
    margin: var(--space-1) 0;
    font-size: var(--fs-lg);
  }
  .result p:last-child {
    margin: 0;
  }
  .gnd {
    margin: var(--space-3) 0 0;
    font-size: var(--fs-base);
    color: var(--text-muted);
  }
</style>
