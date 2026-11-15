<script lang="ts">
  // Tap any pad on the SuperMini photo to see what it does. Pads are color
  // coded by kind; a filter highlights every pin with a capability.
  import { BOARD_VIEWS } from '../guide/programmingLeds';
  import { ALL_GPIO_NOTE, PAD_GEOMETRY, SUPERMINI_PINS, type SuperMiniPin } from './supermini';

  type View = 'bottom' | 'top';
  let view = $state<View>('bottom');
  let selected = $state<SuperMiniPin>(SUPERMINI_PINS[3]);
  let filter = $state<string | null>(null);

  const photo = $derived(BOARD_VIEWS[view]);
  const geo = $derived(PAD_GEOMETRY[view]);
  const FILTERS = ['ADC', 'strapping', 'onboard LED', 'BOOT button', 'UART RX', 'UART TX', 'power out'];

  function pos(p: SuperMiniPin): [number, number] {
    return [geo[p.col], geo.rows[p.row]];
  }
  function matches(p: SuperMiniPin) {
    return filter === null || p.tags.includes(filter) || (filter === 'UART RX' && p.tags.includes('UART TX'));
  }
</script>

<div class="explorer">
  <div class="controls">
    <div class="seg" role="group" aria-label="Board side">
      <button type="button" class:on={view === 'bottom'} aria-pressed={view === 'bottom'} onclick={() => (view = 'bottom')}>Underside</button>
      <button type="button" class:on={view === 'top'} aria-pressed={view === 'top'} onclick={() => (view = 'top')}>Top</button>
    </div>
    <div class="filters" role="group" aria-label="Highlight pins">
      {#each FILTERS.filter((f) => f !== 'UART TX') as f (f)}
        <button type="button" class="chip" class:on={filter === f} aria-pressed={filter === f} onclick={() => (filter = filter === f ? null : f)}>
          {f === 'UART RX' ? 'serial (UART)' : f}
        </button>
      {/each}
    </div>
  </div>

  <div class="layout">
    <figure class="board">
      <img src={photo.src} width={photo.width} height={photo.height} alt={photo.alt} />
      <svg viewBox="0 0 {photo.width} {photo.height}">
        {#each SUPERMINI_PINS as p (p.id)}
          {@const [x, y] = pos(p)}
          <g
            class="pad {p.kind}"
            class:sel={selected.id === p.id}
            class:dim={!matches(p)}
            role="button"
            tabindex="0"
            aria-label={p.title}
            aria-pressed={selected.id === p.id}
            onclick={() => (selected = p)}
            onkeydown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), (selected = p))}
          >
            <circle class="hit" cx={x} cy={y} r={geo.r + 8} />
            <circle class="ring" cx={x} cy={y} r={geo.r} />
          </g>
        {/each}
      </svg>
    </figure>

    <div class="info" aria-live="polite">
      <p class="kind {selected.kind}">{selected.kind === 'gpio' ? 'GPIO' : selected.kind}</p>
      <h3>{selected.title}</h3>
      <ul class="tags">
        {#each selected.tags as t (t)}<li>{t}</li>{/each}
      </ul>
      <ul class="facts">
        {#each selected.facts as f, i (i)}<li>{f}</li>{/each}
      </ul>
      <p class="note">{ALL_GPIO_NOTE}</p>
      <div class="legend" aria-hidden="true">
        <span><i class="power"></i>power</span>
        <span><i class="ground"></i>ground</span>
        <span><i class="gpio"></i>GPIO</span>
      </div>
    </div>
  </div>
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
    grid-template-columns: minmax(0, 360px) 1fr;
    gap: var(--space-5);
    align-items: start;
  }
  @media (max-width: 720px) {
    .layout {
      grid-template-columns: 1fr;
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
  .board svg {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
  }
  .pad {
    cursor: pointer;
    outline: none;
    transition: opacity 0.2s;
  }
  .pad.dim {
    opacity: 0.15;
  }
  .hit {
    fill: transparent;
  }
  .ring {
    fill: none;
    stroke-width: 6;
    transition: stroke-width 0.15s;
  }
  .power .ring {
    stroke: var(--guide-wire-5v);
  }
  .ground .ring {
    stroke: var(--guide-wire-gnd);
  }
  .gpio .ring {
    stroke: var(--guide-wire-data);
  }
  .pad:hover .ring,
  .pad:focus-visible .ring {
    stroke-width: 10;
  }
  .pad.sel .ring {
    stroke-width: 12;
    fill: color-mix(in srgb, var(--accent) 35%, transparent);
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
  }
  .kind.power {
    color: var(--guide-wire-5v);
  }
  .kind.ground {
    color: var(--text-muted);
  }
  .kind.gpio {
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
  .note {
    margin: 0 0 var(--space-3);
    font-size: var(--fs-base);
    color: var(--warning);
    font-weight: 600;
  }
  .legend {
    display: flex;
    gap: var(--space-4);
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  .legend i {
    display: inline-block;
    width: 10px;
    height: 10px;
    border-radius: 50%;
    margin-right: 4px;
    border: 3px solid;
  }
  .legend .power {
    border-color: var(--guide-wire-5v);
  }
  .legend .ground {
    border-color: var(--guide-wire-gnd);
  }
  .legend .gpio {
    border-color: var(--guide-wire-data);
  }
</style>
