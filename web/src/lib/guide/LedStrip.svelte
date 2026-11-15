<script lang="ts">
  // A drawing of the kit's WS2812B strip on its wood backing. Purely a view:
  // the parent decides what each LED shows (null = dark) and drives the
  // animation. The input end (Din) is on the left, as in the kit photo, with
  // the direction arrows printed along the strip like the real one.
  import { displayCss, isDark, type Rgb } from './leds';

  interface Props {
    leds: (Rgb | null)[];
    brightness?: number;
    /** Which LED to outline, e.g. while a chain demo hands it a color. */
    highlight?: number | null;
    /** Index of an LED drawn as broken. */
    dead?: number | null;
    size?: 'md' | 'lg';
    label?: string;
  }

  let { leds, brightness = 255, highlight = null, dead = null, size = 'md', label = 'LED strip' }: Props = $props();

  const description = $derived(
    `${label}: ${leds.filter((c) => !isDark(c)).length} of ${leds.length} LEDs lit`,
  );
</script>

<div class="wood {size}" role="img" aria-label={description}>
  <div class="pcb">
    <span class="end-label in" aria-hidden="true">Din</span>
    {#each leds as c, i (i)}
      <div class="cell" class:highlight={highlight === i}>
        <div
          class="led"
          class:off={isDark(c)}
          class:dead={dead === i}
          style:--led={displayCss(c, brightness)}
        ></div>
        <span class="arrow" aria-hidden="true">›</span>
        <span class="idx" aria-hidden="true">{i}</span>
      </div>
    {/each}
    <span class="end-label out" aria-hidden="true">Dout</span>
  </div>
</div>

<style>
  .wood {
    background:
      repeating-linear-gradient(
        92deg,
        transparent 0 14px,
        color-mix(in srgb, var(--guide-wood-edge) 25%, transparent) 14px 15px,
        transparent 15px 31px
      ),
      var(--guide-wood);
    border: var(--border-w) solid var(--guide-wood-edge);
    border-radius: 6px;
    padding: var(--space-3) var(--space-3);
    overflow-x: auto;
  }
  .pcb {
    display: flex;
    align-items: center;
    gap: 0;
    width: max-content;
    min-width: 100%;
    background: var(--guide-pcb);
    border: var(--border-w) solid var(--guide-pcb-edge);
    border-radius: 3px;
    padding: var(--space-2) var(--space-2) var(--space-4);
  }
  .end-label {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-faint);
    writing-mode: vertical-rl;
    transform: rotate(180deg);
    padding: 0 var(--space-1);
  }
  .cell {
    position: relative;
    flex: 1 0 auto;
    display: flex;
    justify-content: center;
    padding: var(--space-2) var(--space-3);
    border-radius: 4px;
    outline: 2px solid transparent;
    transition: outline-color 0.2s;
  }
  .cell.highlight {
    outline-color: var(--accent);
  }
  .led {
    --size: 26px;
    width: var(--size);
    height: var(--size);
    border-radius: 4px;
    background: radial-gradient(circle at 50% 50%, color-mix(in srgb, var(--led) 55%, white) 0 18%, var(--led) 45%);
    box-shadow:
      0 0 calc(var(--size) * 0.5) calc(var(--size) * 0.15) var(--led),
      0 0 calc(var(--size) * 1.4) calc(var(--size) * 0.3) color-mix(in srgb, var(--led) 45%, transparent);
    border: 1px solid color-mix(in srgb, var(--led) 60%, var(--guide-pcb-edge));
  }
  .lg .led {
    --size: 34px;
  }
  .led.off {
    background: var(--guide-led-off);
    box-shadow: inset 0 0 0 4px color-mix(in srgb, var(--guide-pcb-edge) 60%, transparent);
    border-color: var(--guide-pcb-edge);
  }
  .led.dead {
    background:
      linear-gradient(45deg, transparent 45%, var(--danger) 45% 55%, transparent 55%),
      linear-gradient(-45deg, transparent 45%, var(--danger) 45% 55%, transparent 55%),
      var(--guide-led-off);
    box-shadow: none;
  }
  .arrow {
    position: absolute;
    right: -3px;
    top: 50%;
    transform: translateY(-50%);
    color: var(--text-faint);
    font-size: 14px;
  }
  .idx {
    position: absolute;
    bottom: -14px;
    left: 0;
    right: 0;
    text-align: center;
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-faint);
  }
</style>
