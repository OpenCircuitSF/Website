<script lang="ts">
  // Where the three wires go on the ESP32-C3 SuperMini. The board photo is
  // overlaid with rings on the 5V, G and 4 pads; the legend highlights one
  // pin at a time. Underside view first: that's where the pin numbers are
  // printed.
  import { BOARD_VIEWS, PINS, type Pin } from './programmingLeds';

  type View = keyof typeof BOARD_VIEWS;

  let view = $state<View>('bottom');
  let active = $state<Pin['id'] | null>(null);

  const v = $derived(BOARD_VIEWS[view]);

  const wireVar: Record<Pin['id'], string> = {
    '5v': 'var(--guide-wire-5v)',
    gnd: 'var(--guide-wire-gnd)',
    gpio4: 'var(--guide-wire-data)',
  };
</script>

<div class="pinout">
  <div class="toolbar" role="group" aria-label="Board side">
    <button type="button" class:on={view === 'bottom'} aria-pressed={view === 'bottom'} onclick={() => (view = 'bottom')}>
      Underside (labels)
    </button>
    <button type="button" class:on={view === 'top'} aria-pressed={view === 'top'} onclick={() => (view = 'top')}>
      Top (buttons)
    </button>
  </div>

  <div class="layout">
    <figure class="board">
      <img src={v.src} width={v.width} height={v.height} alt={v.alt} />
      <svg viewBox="0 0 {v.width} {v.height}" aria-hidden="true">
        {#each PINS as pin (pin.id)}
          {@const [x, y] = v.pads[pin.id]}
          {@const dim = active !== null && active !== pin.id}
          {@const tagX = v.labelSide === 'left' ? x - v.padR - 150 : x + v.padR + 90}
          {@const leadX = v.labelSide === 'left' ? tagX + 84 : tagX}
          <g class="pad" class:dim class:active={active === pin.id} style:--wire={wireVar[pin.id]}>
            <circle class="pulse" cx={x} cy={y} r={v.padR} />
            <circle class="ring" cx={x} cy={y} r={v.padR} />
            <line class="lead" x1={v.labelSide === 'left' ? x - v.padR : x + v.padR} y1={y} x2={leadX} y2={y} />
            <rect class="tag" x={tagX} y={y - 20} width="84" height="40" rx="8" />
            <text class="tag-text" x={tagX + 42} y={y + 9}>{pin.id === 'gpio4' ? 'GPIO 4' : pin.id === 'gnd' ? 'GND' : '5V'}</text>
          </g>
        {/each}
      </svg>
      <figcaption>
        {view === 'bottom'
          ? 'Underside: the pins are numbered here. 5V, G and 4 are the first, second and fourth pads on the left.'
          : 'Top: the same pads are now on the right edge. BOOT and RST are the two buttons below the USB port.'}
      </figcaption>
    </figure>

    <ul class="legend">
      {#each PINS as pin (pin.id)}
        <li>
          <button
            type="button"
            class="legend-item"
            class:active={active === pin.id}
            style:--wire={wireVar[pin.id]}
            onmouseenter={() => (active = pin.id)}
            onmouseleave={() => (active = null)}
            onfocus={() => (active = pin.id)}
            onblur={() => (active = null)}
            onclick={() => (active = active === pin.id ? null : pin.id)}
          >
            <span class="swatch" aria-hidden="true"></span>
            <span class="legend-text">
              <strong>{pin.name}</strong>
              <span class="mono">board <b>{pin.board}</b> → strip <b>{pin.strip}</b></span>
              <span class="carries">{pin.carries}</span>
            </span>
          </button>
        </li>
      {/each}
      <li class="rule">Trust the printed labels, not the wire colors. Wire colors vary between kits.</li>
    </ul>
  </div>

  <svg class="wiring" viewBox="0 0 640 170" role="img" aria-label="Wiring: board 5V to strip +5V, board G to strip GND, board GPIO 4 to strip Din">
    <rect class="board-body" x="10" y="20" width="130" height="130" rx="8" />
    <text class="board-name" x="75" y="140">ESP32-C3</text>
    <rect class="usb" x="50" y="8" width="50" height="18" rx="4" />
    {#each [['5V', 50, '5v'], ['G', 85, 'gnd'], ['4', 120, 'gpio4']] as [label, y, id] (id)}
      <circle class="pin-dot" cx="128" cy={y} r="7" />
      <text class="pin-label" x="112" y={Number(y) + 5}>{label}</text>
      <path
        class="wire"
        class:data={id === 'gpio4'}
        class:dim={active !== null && active !== id}
        style:--wire={wireVar[id as Pin['id']]}
        d="M135 {y} C 220 {y}, 250 {Number(y) + 0}, 330 {y}"
      />
    {/each}
    <rect class="strip-body" x="330" y="30" width="300" height="110" rx="4" />
    {#each [['+5V', 50], ['GND', 85], ['Din', 120]] as [label, y] (label)}
      <rect class="strip-pad" x="333" y={Number(y) - 7} width="14" height="14" rx="2" />
      <text class="strip-label" x="352" y={Number(y) + 5}>{label}</text>
    {/each}
    {#each [0, 1, 2, 3] as i (i)}
      <rect class="strip-led" x={400 + i * 55} y="70" width="30" height="30" rx="4" />
      <text class="strip-arrow" x={436 + i * 55} y="91">›</text>
    {/each}
    <text class="strip-note" x="480" y="128">data flows this way →</text>
  </svg>
</div>

<style>
  .toolbar {
    display: inline-flex;
    border: var(--border-w) solid var(--border-strong);
    border-radius: var(--radius);
    overflow: hidden;
    margin-bottom: var(--space-3);
  }
  .toolbar button {
    font: inherit;
    font-size: var(--fs-base);
    padding: var(--space-2) var(--space-3);
    border: 0;
    background: var(--bg-panel);
    color: var(--text-muted);
    cursor: pointer;
  }
  .toolbar button.on {
    background: var(--accent);
    color: var(--accent-text);
  }
  .layout {
    display: grid;
    grid-template-columns: minmax(0, 340px) 1fr;
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
    height: auto;
    aspect-ratio: auto;
    pointer-events: none;
  }
  figcaption {
    margin-top: var(--space-2);
    font-size: var(--fs-base);
    color: var(--text-muted);
  }
  .pad {
    transition: opacity 0.2s;
  }
  .pad.dim {
    opacity: 0.2;
  }
  .ring {
    fill: none;
    stroke: var(--wire);
    stroke-width: 7;
  }
  .pulse {
    fill: none;
    stroke: var(--wire);
    stroke-width: 4;
    transform-box: fill-box;
    transform-origin: center;
    animation: pulse 1.6s ease-out infinite;
  }
  .tag {
    fill: var(--wire);
  }
  .lead {
    stroke: var(--wire);
    stroke-width: 4;
  }
  .tag-text {
    fill: var(--bg-panel);
    font-family: var(--font-mono);
    font-size: 22px;
    font-weight: 700;
    text-anchor: middle;
  }
  @keyframes pulse {
    from {
      transform: scale(1);
      opacity: 0.9;
    }
    to {
      transform: scale(1.8);
      opacity: 0;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .pulse {
      animation: none;
      opacity: 0;
    }
  }

  .legend {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: var(--space-2);
  }
  .legend-item {
    width: 100%;
    display: flex;
    gap: var(--space-3);
    align-items: flex-start;
    text-align: left;
    font: inherit;
    padding: var(--space-3);
    background: var(--bg-panel);
    border: var(--border-w) solid var(--border);
    border-left: 6px solid var(--wire);
    border-radius: var(--radius);
    color: var(--text);
    cursor: pointer;
  }
  .legend-item.active,
  .legend-item:hover {
    background: var(--bg-subtle);
  }
  .swatch {
    flex: none;
    width: 14px;
    height: 14px;
    margin-top: 3px;
    border-radius: 50%;
    background: var(--wire);
    border: 1px solid var(--border-strong);
  }
  .legend-text {
    display: grid;
    gap: 2px;
  }
  .legend-text strong {
    font-size: var(--fs-md);
  }
  .mono {
    font-family: var(--font-mono);
    font-size: var(--fs-base);
  }
  .carries {
    font-size: var(--fs-base);
    color: var(--text-muted);
  }
  .rule {
    font-size: var(--fs-base);
    color: var(--warning);
    font-weight: 600;
    padding: var(--space-2) 0;
  }

  .wiring {
    display: block;
    width: 100%;
    max-width: 640px;
    margin: var(--space-5) auto 0;
  }
  .board-body {
    fill: var(--guide-pcb);
    stroke: var(--guide-pcb-edge);
  }
  .usb {
    fill: var(--text-faint);
  }
  .board-name {
    fill: var(--text-faint);
    font-family: var(--font-mono);
    font-size: 12px;
    text-anchor: middle;
  }
  .pin-dot {
    fill: var(--guide-wood);
  }
  .pin-label,
  .strip-label {
    fill: var(--text-faint);
    font-family: var(--font-mono);
    font-size: 13px;
    text-anchor: end;
  }
  .strip-label {
    text-anchor: start;
  }
  .wire {
    fill: none;
    stroke: var(--wire);
    stroke-width: 5;
    stroke-linecap: round;
    transition: opacity 0.2s;
  }
  .wire.dim {
    opacity: 0.2;
  }
  .wire.data {
    stroke-dasharray: 10 8;
    animation: flow 0.8s linear infinite;
  }
  @keyframes flow {
    to {
      stroke-dashoffset: -18;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .wire.data {
      animation: none;
    }
  }
  .strip-body {
    fill: var(--guide-pcb);
    stroke: var(--guide-wood-edge);
    stroke-width: 6;
  }
  .strip-pad {
    fill: var(--guide-wood);
  }
  .strip-led {
    fill: var(--guide-led-off);
    stroke: var(--guide-pcb-edge);
  }
  .strip-arrow,
  .strip-note {
    fill: var(--text-faint);
    font-family: var(--font-mono);
    font-size: 14px;
  }
  .strip-note {
    font-size: 11px;
    text-anchor: middle;
  }
</style>
