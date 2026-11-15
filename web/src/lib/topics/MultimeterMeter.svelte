<script lang="ts">
  // The shared meter for the multimeter guides (#0666): the meter image with
  // clickable jacks and dial settings over it, the probes plugged in, and the
  // display. The demos own all state and logic (multimeter.ts); this only
  // draws it and reports clicks.
  import type { Snippet } from 'svelte';
  import { METERS, dialById, dialPoint, type MeterId, type Reading, type Tone } from './multimeter';

  interface Props {
    meterId: MeterId;
    dial: string;
    black: string | null;
    red: string | null;
    reading: Reading | null;
    /** Shown beside the meter, under the controls. */
    note?: { tone: Tone; title: string; body: string } | null;
    onmeter?: (id: MeterId) => void;
    onjack?: (id: string) => void;
    ondial?: (id: string) => void;
    children?: Snippet;
  }
  let { meterId, dial, black, red, reading, note = null, onmeter, onjack, ondial, children }: Props = $props();

  const m = $derived(METERS[meterId]);
  const pos = $derived(dialById(m, dial));
  const lcd = $derived(m.lcd);
  const plugR = 15;

  function key(fn: () => void) {
    return (e: KeyboardEvent) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        fn();
      }
    };
  }
  function jackLabel(id: string, label: string) {
    const who = black === id ? ', black probe plugged in' : red === id ? ', red probe plugged in' : '';
    return `${label} jack${who}`;
  }
</script>

<div class="mm">
  {#if onmeter}
    <div class="seg" role="group" aria-label="Meter">
      {#each Object.values(METERS) as mm (mm.id)}
        <button type="button" class:on={meterId === mm.id} aria-pressed={meterId === mm.id} onclick={() => onmeter(mm.id)}>
          {mm.short}{mm.autoRanging ? ', auto-ranging' : ', manual range'}
        </button>
      {/each}
    </div>
  {/if}

  <div class="layout">
    <figure class="meter">
      <img
        src={m.image.src}
        srcset="{m.image.src} 1x, {m.image.src2x} 2x"
        width={m.image.width}
        height={m.image.height}
        alt={m.image.alt}
      />
      <svg viewBox="0 0 {m.image.width} {m.image.height}">
        <!-- Display -->
        <g class="lcd" aria-hidden="true">
          {#if reading && reading.display !== ''}
            <text
              class="digits"
              x={reading.overRange && m.overRange === '1' ? lcd.x + 18 : lcd.x + lcd.w - 14}
              y={lcd.y + lcd.h * 0.74}
              font-size={lcd.h * 0.5}
              text-anchor={reading.overRange && m.overRange === '1' ? 'start' : 'end'}>{reading.display}</text
            >
            <text class="ann" x={lcd.x + lcd.w - 14} y={lcd.y + 16} text-anchor="end">{reading.unit}</text>
            <text class="ann" x={lcd.x + 10} y={lcd.y + 16}>
              {pos.fn === 'acv' ? 'AC' : pos.fn === 'dcv' || pos.fn === 'dca' ? 'DC' : ''}{m.autoRanging ? '  AUTO' : ''}
            </text>
            {#if reading.beep}
              <text class="ann beep" x={lcd.x + 10} y={lcd.y + lcd.h - 8}>•)))</text>
            {/if}
          {/if}
        </g>

        <!-- Knob pointer -->
        <g class="knob" style="transform: rotate({pos.angle}deg); transform-origin: {m.dial.cx}px {m.dial.cy}px" aria-hidden="true">
          <rect x={m.dial.cx - 11} y={m.dial.cy - m.dial.knobR + 8} width="22" height={m.dial.knobR * 2 - 16} rx="10" class="bar" />
          <path
            d="M{m.dial.cx} {m.dial.cy - m.dial.knobR + 12} l-6 12 h12 z"
            class="tip"
          />
        </g>

        <!-- Dial settings -->
        {#each m.dial.positions as p (p.id)}
          {@const [x, y] = dialPoint(m, p)}
          {#if ondial}
            <g
              class="spot dial"
              class:sel={p.id === dial}
              role="button"
              tabindex="0"
              aria-label={p.name}
              aria-pressed={p.id === dial}
              onclick={() => ondial(p.id)}
              onkeydown={key(() => ondial(p.id))}
            >
              <circle class="hit" cx={x} cy={y} r="15" />
              <circle class="ring" cx={x} cy={y} r="15" />
            </g>
          {:else if p.id === dial}
            <circle class="ring fixed" cx={x} cy={y} r="15" />
          {/if}
        {/each}

        <!-- Jacks -->
        {#each m.jacks as j (j.id)}
          {#if onjack}
            <g
              class="spot jack"
              role="button"
              tabindex="0"
              aria-label={jackLabel(j.id, j.label)}
              onclick={() => onjack(j.id)}
              onkeydown={key(() => onjack(j.id))}
            >
              <circle class="hit" cx={j.x} cy={j.y} r="28" />
              <circle class="ring" cx={j.x} cy={j.y} r="27" />
            </g>
          {/if}
        {/each}

        <!-- Probes -->
        {#each [['black', black], ['red', red]] as [color, id] (color)}
          {@const j = m.jacks.find((x) => x.id === id)}
          {#if j}
            <g class="plug {color}" aria-hidden="true">
              <path
                d="M{j.x} {j.y} C {j.x} {j.y + 60}, {j.x + (color === 'black' ? -20 : 20)} {m.image.height - 20}, {j.x + (color === 'black' ? -10 : 10)} {m.image.height}"
                class="lead"
              />
              <circle cx={j.x} cy={j.y} r={plugR} class="body" />
              <circle cx={j.x - 4} cy={j.y - 4} r="4" class="shine" />
            </g>
          {/if}
        {/each}
      </svg>
    </figure>

    <div class="side">
      {@render children?.()}
      {#if reading?.beep}
        <p class="beep-badge" aria-hidden="true">BEEP</p>
      {/if}
      <div class="note {note?.tone ?? 'empty'}" role="status" aria-live="polite">
        {#if note}
          <strong>{note.title}</strong>
          <p>{note.body}</p>
        {/if}
      </div>
    </div>
  </div>
</div>

<style>
  .seg {
    display: inline-flex;
    flex-wrap: wrap;
    border: var(--border-w) solid var(--border-strong);
    border-radius: var(--radius);
    overflow: hidden;
    margin-bottom: var(--space-4);
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
    .meter {
      max-width: 340px;
      margin: 0 auto;
    }
  }
  .meter {
    position: relative;
    margin: 0;
  }
  .meter img {
    display: block;
    width: 100%;
    height: auto;
  }
  .meter svg {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
  }
  .digits {
    font-family: var(--font-mono);
    font-weight: 700;
    fill: #1d2318;
    letter-spacing: 0.04em;
  }
  .ann {
    font-family: var(--font-mono);
    font-size: 11px;
    font-weight: 700;
    fill: #2c3326;
  }
  .ann.beep {
    font-size: 14px;
  }
  .knob {
    transition: transform 0.25s ease;
  }
  @media (prefers-reduced-motion: reduce) {
    .knob {
      transition: none;
    }
  }
  .bar {
    fill: #3a3e44;
    stroke: #0b0c0d;
    stroke-width: 1.5;
  }
  .tip {
    fill: #f2f4f5;
  }
  .spot {
    cursor: pointer;
    outline: none;
  }
  .hit {
    fill: transparent;
  }
  .ring {
    fill: none;
    stroke: transparent;
    stroke-width: 3;
    transition: stroke 0.15s;
  }
  .spot:hover .ring,
  .spot:focus-visible .ring {
    stroke: var(--accent);
  }
  .spot.sel .ring,
  .ring.fixed {
    stroke: var(--accent);
    stroke-width: 3;
  }
  .spot:focus-visible .ring {
    stroke-width: 4;
  }
  .lead {
    fill: none;
    stroke-width: 7;
    stroke-linecap: round;
  }
  .plug.black .lead,
  .plug.black .body {
    fill: #161616;
    stroke: #6c7076;
  }
  .plug.black .lead {
    fill: none;
    stroke: #161616;
  }
  .plug.red .lead {
    stroke: #c62828;
  }
  .plug.red .body {
    fill: #d32f2f;
    stroke: #7f1414;
  }
  .body {
    stroke-width: 2;
  }
  .shine {
    fill: #fff;
    opacity: 0.25;
  }
  .side {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }
  .beep-badge {
    align-self: flex-start;
    margin: 0;
    padding: 2px 10px;
    border-radius: 999px;
    font-family: var(--font-mono);
    font-weight: 700;
    background: var(--success);
    color: var(--bg-page);
    animation: pulse 0.5s ease-in-out 3;
  }
  @keyframes pulse {
    50% {
      transform: scale(1.12);
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .beep-badge {
      animation: none;
    }
  }
  .note {
    padding: var(--space-3) var(--space-4);
    border: var(--border-w) solid var(--border);
    border-left-width: 4px;
    border-radius: 6px;
    background: var(--bg-panel);
  }
  .note.empty {
    padding: 0;
    border: 0;
    background: none;
  }
  .note p {
    margin: var(--space-1) 0 0;
    font-size: var(--fs-md);
  }
  .note.ok {
    border-left-color: var(--success);
  }
  .note.info {
    border-left-color: var(--accent);
  }
  .note.warn {
    border-left-color: var(--warning);
  }
  .note.bad {
    border-left-color: var(--danger);
  }
  .note.bad strong {
    color: var(--danger);
  }
</style>
