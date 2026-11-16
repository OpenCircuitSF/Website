<script lang="ts">
  // Press the real BOOT and RST buttons on a SuperMini photo. Each click
  // toggles a button between held and released, so the "hold BOOT, tap RST"
  // sequence can be done one step at a time. Rules live in codeOnBoard.ts.
  import { BOARD_VIEWS } from '../guide/programmingLeds';
  import {
    SKETCH_LABELS,
    initialButtons,
    modeLabel,
    portPresent,
    toggleBoot,
    toggleReset,
    upload,
    type Sketch,
  } from './codeOnBoard';

  const photo = BOARD_VIEWS.top;
  // Button centers in the top photo's own pixels (532 × 702).
  const BOOT_AT = { x: 188 / 532, y: 297 / 702 };
  const RST_AT = { x: 336 / 532, y: 295 / 702 };

  let sketch = $state<Sketch>('sleep');
  let s = $state(initialButtons('sleep'));

  const port = $derived(portPresent(s));

  function pickSketch(k: Sketch) {
    sketch = k;
    s = initialButtons(k);
  }
</script>

<div class="bb">
  <fieldset class="sketch">
    <legend>The code on the board right now</legend>
    {#each Object.keys(SKETCH_LABELS) as k (k)}
      <label>
        <input type="radio" name="bb-sketch" checked={sketch === k} onchange={() => pickSketch(k as Sketch)} />
        {SKETCH_LABELS[k as Sketch]}
      </label>
    {/each}
  </fieldset>

  <div class="layout">
    <figure class="board">
      <img src={photo.src} width={photo.width} height={photo.height} alt={photo.alt} />
      <button
        type="button"
        class="hot"
        class:held={s.boot}
        style="left: {BOOT_AT.x * 100}%; top: {BOOT_AT.y * 100}%"
        aria-pressed={s.boot}
        onclick={() => (s = toggleBoot(s))}
      >
        <span class="sr-only">BOOT button</span>
      </button>
      <button
        type="button"
        class="hot"
        class:held={s.reset}
        style="left: {RST_AT.x * 100}%; top: {RST_AT.y * 100}%"
        aria-pressed={s.reset}
        onclick={() => (s = toggleReset(s))}
      >
        <span class="sr-only">RST button</span>
      </button>
      <figcaption>Tap a button to hold it down. Tap again to let go.</figcaption>
    </figure>

    <div class="panel">
      <dl class="state">
        <div>
          <dt>BOOT (GPIO9)</dt>
          <dd class:on={s.boot}>{s.boot ? 'held' : 'up'}</dd>
        </div>
        <div>
          <dt>RST (EN)</dt>
          <dd class:on={s.reset}>{s.reset ? 'held' : 'up'}</dd>
        </div>
        <div>
          <dt>Chip</dt>
          <dd class="mode {s.mode}">{modeLabel(s)}</dd>
        </div>
        <div>
          <dt>USB port</dt>
          <dd class="port" class:yes={port}>{port ? '/dev/cu.usbmodem101' : 'none'}</dd>
        </div>
      </dl>

      <div class="actions">
        <button type="button" class="primary" onclick={() => (s = upload(s).state)}>Upload</button>
        <button type="button" onclick={() => (s = initialButtons(sketch))}>Start over</button>
      </div>

      <ol class="log" aria-live="polite">
        {#each s.log as line, i (i)}<li>{line}</li>{/each}
      </ol>
    </div>
  </div>
</div>

<style>
  .sketch {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2) var(--space-4);
    margin: 0 0 var(--space-4);
    padding: var(--space-3);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
    font-size: var(--fs-base);
  }
  .sketch legend {
    padding: 0 var(--space-1);
    color: var(--text-muted);
  }
  .layout {
    display: grid;
    grid-template-columns: minmax(0, 280px) 1fr;
    gap: var(--space-5);
    align-items: start;
  }
  @media (max-width: 640px) {
    .layout {
      grid-template-columns: 1fr;
    }
    .board {
      max-width: 280px;
    }
  }
  .board {
    position: relative;
    margin: 0;
  }
  .board img {
    display: block;
    width: 100%;
    height: auto;
    border-radius: 6px;
  }
  .hot {
    position: absolute;
    width: 22%;
    aspect-ratio: 1;
    transform: translate(-50%, -50%);
    border-radius: 50%;
    border: 4px solid var(--accent);
    background: transparent;
    cursor: pointer;
    box-shadow: 0 0 0 2px rgb(0 0 0 / 0.5);
  }
  .hot:hover,
  .hot:focus-visible {
    outline: 3px solid var(--accent);
    outline-offset: 2px;
  }
  .hot.held {
    background: color-mix(in srgb, var(--accent) 45%, transparent);
    border-width: 7px;
  }
  figcaption {
    margin-top: var(--space-2);
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  .panel {
    min-width: 0;
  }
  .state {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: var(--space-2);
    margin: 0 0 var(--space-3);
  }
  .state div {
    padding: var(--space-2) var(--space-3);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
    background: var(--bg-panel);
  }
  dt {
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  dd {
    margin: 0;
    font-family: var(--font-mono);
    font-weight: 700;
    overflow-wrap: anywhere;
  }
  dd.on,
  .mode.download,
  .port.yes {
    color: var(--accent);
  }
  .mode.reset {
    color: var(--warning);
  }
  .port:not(.yes) {
    color: var(--danger);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin-bottom: var(--space-3);
  }
  .actions button {
    font: inherit;
    font-size: var(--fs-base);
    padding: 6px 14px;
    border-radius: var(--radius);
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  .actions button.primary {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .log {
    margin: 0;
    padding: var(--space-3) var(--space-3) var(--space-3) var(--space-6);
    border-radius: 6px;
    background: var(--bg-panel);
    border: var(--border-w) solid var(--border);
    font-size: var(--fs-base);
    min-height: 6em;
  }
  .log li {
    margin-bottom: var(--space-1);
  }
  .log li:last-child {
    font-weight: 600;
  }
</style>
