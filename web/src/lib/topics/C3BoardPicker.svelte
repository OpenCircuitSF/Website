<script lang="ts">
  // The SuperMini underside photo with tappable pads, shared by the #0671
  // pin demos. Pad positions and pin facts come from supermini.ts (the same
  // source PinoutExplorer uses); the parent decides which pads can be
  // picked and how each one is colored.
  import { BOARD_VIEWS } from '../guide/programmingLeds';
  import { PAD_GEOMETRY, SUPERMINI_PINS, type SuperMiniPin } from './supermini';

  let {
    pickable,
    selected,
    onpick,
    tone = () => '',
    label = (p: SuperMiniPin) => p.title,
  }: {
    pickable: readonly string[];
    selected: string;
    onpick: (id: string) => void;
    tone?: (id: string) => string;
    label?: (p: SuperMiniPin) => string;
  } = $props();

  const photo = BOARD_VIEWS.bottom;
  const geo = PAD_GEOMETRY.bottom;
  const pins = $derived(SUPERMINI_PINS.filter((p) => pickable.includes(p.id)));
</script>

<figure class="board">
  <img src={photo.src} width={photo.width} height={photo.height} alt={photo.alt} />
  <svg viewBox="0 0 {photo.width} {photo.height}">
    {#each pins as p (p.id)}
      {@const x = geo[p.col]}
      {@const y = geo.rows[p.row]}
      <g
        class="pad {tone(p.id)}"
        class:sel={selected === p.id}
        role="button"
        tabindex="0"
        aria-label={label(p)}
        aria-pressed={selected === p.id}
        onclick={() => onpick(p.id)}
        onkeydown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onpick(p.id))}
      >
        <circle class="hit" cx={x} cy={y} r={geo.r + 8} />
        <circle class="ring" cx={x} cy={y} r={geo.r} />
      </g>
    {/each}
  </svg>
</figure>

<style>
  .board {
    position: relative;
    margin: 0;
    max-width: 300px;
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
  }
  .hit {
    fill: transparent;
  }
  .ring {
    fill: none;
    stroke: var(--guide-wire-data);
    stroke-width: 6;
  }
  .pad.good .ring {
    stroke: #2e8b3e;
  }
  .pad.care .ring {
    stroke: #e0a800;
  }
  .pad.avoid .ring {
    stroke: #d23b2f;
  }
  .pad:hover .ring,
  .pad:focus-visible .ring {
    stroke-width: 10;
  }
  .pad:focus-visible .hit {
    stroke: var(--accent);
    stroke-width: 4;
  }
  .pad.sel .ring {
    stroke-width: 12;
    fill: color-mix(in srgb, var(--accent) 35%, transparent);
  }
</style>
