<script lang="ts">
  // The pass-along chain, step by step: the board sends one color per LED
  // down a single wire; each LED keeps the first value it sees and passes the
  // rest on; a moment of silence at the end tells every LED to show what it
  // is holding, all at once. The buttons replay the ways it fails quietly.
  import { onDestroy } from 'svelte';
  import { NAMED_COLORS, PHYSICAL_LEDS, displayCss, type Rgb } from './leds';

  type Mode = 'normal' | 'wrong-end' | 'dead' | 'short' | 'long';

  const MODES: { id: Mode; label: string }[] = [
    { id: 'normal', label: 'Send a frame' },
    { id: 'short', label: 'NUM_LEDS too low (5)' },
    { id: 'long', label: 'NUM_LEDS too high (11)' },
    { id: 'dead', label: 'Broken LED #3' },
    { id: 'wrong-end', label: 'Wire the wrong end' },
  ];

  const DEAD = 3;
  const STEP_MS = 650;

  let mode = $state<Mode>('normal');
  let queue = $state<{ rgb: Rgb; n: number }[]>([]);
  let held = $state<(Rgb | null)[]>(Array(PHYSICAL_LEDS).fill(null));
  let shown = $state<(Rgb | null)[]>(Array(PHYSICAL_LEDS).fill(null));
  let at = $state(-1);
  let spilled = $state(0);
  let message = $state('Press a button to send colors down the wire.');
  let running = $state(false);
  let timer: ReturnType<typeof setTimeout> | undefined;

  onDestroy(() => clearTimeout(timer));

  function colorsFor(m: Mode) {
    const count = m === 'short' ? 5 : m === 'long' ? 11 : PHYSICAL_LEDS;
    return Array.from({ length: count }, (_, n) => ({ rgb: NAMED_COLORS[n % NAMED_COLORS.length].rgb, n }));
  }

  function run(m: Mode) {
    clearTimeout(timer);
    mode = m;
    queue = colorsFor(m);
    held = Array(PHYSICAL_LEDS).fill(null);
    shown = Array(PHYSICAL_LEDS).fill(null);
    spilled = 0;
    at = -1;
    running = true;
    message = `The board sends ${queue.length} colors, one after another, down one wire.`;
    timer = setTimeout(m === 'wrong-end' ? wrongEnd : step, STEP_MS);
  }

  function wrongEnd() {
    at = PHYSICAL_LEDS;
    message =
      'Data only travels one way. Fed into the Dout end, every LED is still waiting at its Din for data that never arrives. Nothing lights, and nothing warns you.';
    running = false;
  }

  function step() {
    at += 1;
    if (mode === 'dead' && at === DEAD) {
      message = `LED ${DEAD} is broken, so it can't pass anything on. The LEDs after it never hear a thing.`;
      timer = setTimeout(latch, STEP_MS * 1.5);
      return;
    }
    if (at >= PHYSICAL_LEDS) {
      spilled = queue.length;
      queue = [];
      message =
        spilled > 0
          ? `The last LED passes ${spilled} leftover colors out of its Dout, off the end of the strip, into nothing.`
          : 'Every LED is holding one color. Now the board goes quiet…';
      timer = setTimeout(latch, STEP_MS * 1.5);
      return;
    }
    if (queue.length === 0) {
      message = `The colors ran out. LEDs ${at}–${PHYSICAL_LEDS - 1} never received one.`;
      timer = setTimeout(latch, STEP_MS * 1.5);
      return;
    }
    const [first, ...rest] = queue;
    held[at] = first.rgb;
    queue = rest;
    message = `LED ${at} keeps the first color for itself and passes the other ${rest.length} on.`;
    timer = setTimeout(step, STEP_MS);
  }

  function latch() {
    at = -1;
    shown = [...held];
    const lit = held.filter(Boolean).length;
    message =
      `Silence for 50 µs means "show it": all ${lit} LEDs that got a color light up at the same moment.` +
      (mode === 'normal' ? ' Position in the chain is the address.' : '');
    running = false;
  }
</script>

<div class="chain">
  <div class="buttons" role="group" aria-label="Chain scenarios">
    {#each MODES as m (m.id)}
      <button type="button" class="btn" class:primary={m.id === 'normal'} disabled={running} onclick={() => run(m.id)}>
        {m.label}
      </button>
    {/each}
  </div>

  <div class="stage" class:reversed={mode === 'wrong-end'}>
    <div class="board" aria-hidden="true">
      <span>ESP32</span>
      <span class="pin">GPIO 4</span>
    </div>
    <div class="track">
      <div class="queue" aria-hidden="true" style:--at={Math.max(0, Math.min(at, PHYSICAL_LEDS))}>
        {#each queue as q (q.n)}
          <span class="packet" style:--led={displayCss(q.rgb, 255)}></span>
        {/each}
      </div>
      <div class="leds">
        {#each held as h, i (i)}
          <div class="cell" class:here={at === i}>
            <span class="held" class:has={!!h} style:--led={displayCss(h, 255)} title="holding"></span>
            <span
              class="led"
              class:on={!!shown[i]}
              class:dead={mode === 'dead' && i === DEAD}
              style:--led={displayCss(shown[i], 255)}
            ></span>
            <span class="idx">{i}</span>
          </div>
        {/each}
        <div class="spill" aria-hidden="true">
          {#each Array(spilled) as _, s (s)}
            <span class="packet falling" style:--led={displayCss(NAMED_COLORS[(PHYSICAL_LEDS + s) % NAMED_COLORS.length].rgb, 255)} style:--d="{s * 80}ms"></span>
          {/each}
        </div>
      </div>
    </div>
  </div>

  <p class="message" aria-live="polite">{message}</p>
  <p class="legend text-faint">
    Small squares: the color each LED is holding. Big squares: what it shows.
  </p>
</div>

<style>
  .buttons {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin-bottom: var(--space-4);
  }
  .btn {
    font: inherit;
    font-size: var(--fs-base);
    padding: var(--space-2) var(--space-3);
    border: var(--border-w) solid var(--border-strong);
    border-radius: var(--radius);
    background: var(--bg-subtle);
    color: var(--text);
    cursor: pointer;
  }
  .btn:hover:not(:disabled) {
    background: var(--bg-header);
  }
  .btn.primary {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .btn:disabled {
    opacity: 0.5;
    cursor: default;
  }
  .stage {
    display: flex;
    align-items: stretch;
    gap: var(--space-2);
    padding: var(--space-3);
    background: var(--guide-wood);
    border: var(--border-w) solid var(--guide-wood-edge);
    border-radius: 6px;
    overflow-x: auto;
  }
  .board {
    flex: none;
    display: grid;
    place-content: center;
    gap: 4px;
    width: 72px;
    background: var(--guide-pcb);
    border-radius: 6px;
    color: var(--text-faint);
    font-family: var(--font-mono);
    font-size: 11px;
    text-align: center;
  }
  .reversed .board {
    order: 2;
  }
  .pin {
    color: var(--guide-wire-data);
  }
  .track {
    flex: 1;
    min-width: max-content;
    background: var(--guide-pcb);
    border-radius: 4px;
    padding: var(--space-2);
  }
  .queue {
    --cell: 56px;
    display: flex;
    gap: 3px;
    min-height: 14px;
    margin-left: calc(var(--at) * var(--cell) + 6px);
    transition: margin-left 0.4s ease;
  }
  .packet {
    width: 12px;
    height: 12px;
    border-radius: 2px;
    background: var(--led);
    box-shadow: 0 0 6px var(--led);
  }
  .leds {
    display: flex;
    align-items: flex-start;
    margin-top: var(--space-2);
  }
  .cell {
    width: 56px;
    display: grid;
    justify-items: center;
    gap: 6px;
    padding: 6px 0;
    border-radius: 4px;
    outline: 2px solid transparent;
    transition: outline-color 0.2s;
  }
  .cell.here {
    outline-color: var(--accent);
  }
  .held {
    width: 12px;
    height: 12px;
    border-radius: 2px;
    border: 1px dashed var(--guide-pcb-edge);
  }
  .held.has {
    background: var(--led);
    border: 0;
  }
  .led {
    width: 30px;
    height: 30px;
    border-radius: 4px;
    background: var(--guide-led-off);
    border: 1px solid var(--guide-pcb-edge);
    transition: background 0.15s, box-shadow 0.15s;
  }
  .led.on {
    background: var(--led);
    box-shadow:
      0 0 12px 3px var(--led),
      0 0 34px 6px color-mix(in srgb, var(--led) 40%, transparent);
  }
  .led.dead {
    background:
      linear-gradient(45deg, transparent 45%, var(--danger) 45% 55%, transparent 55%),
      linear-gradient(-45deg, transparent 45%, var(--danger) 45% 55%, transparent 55%),
      var(--guide-led-off);
    box-shadow: none;
  }
  .idx {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-faint);
  }
  .spill {
    display: flex;
    flex-direction: column;
    gap: 3px;
    padding: 6px 0 0 var(--space-2);
  }
  .falling {
    animation: fall 0.9s ease-in var(--d) both;
  }
  @keyframes fall {
    from {
      transform: translate(0, 0);
      opacity: 1;
    }
    to {
      transform: translate(30px, 40px);
      opacity: 0;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .queue {
      transition: none;
    }
    .falling {
      animation: none;
      opacity: 0.4;
    }
  }
  .message {
    min-height: 3em;
    margin: var(--space-3) 0 var(--space-1);
    font-size: var(--fs-md);
    max-width: var(--measure);
  }
  .legend {
    margin: 0;
    font-size: var(--fs-sm);
  }
</style>
