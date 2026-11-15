<script lang="ts">
  // How much current an LED strip draws, against what a USB port can give.
  // Uses the datasheet rule of thumb the class teaches: up to ~20 mA per
  // color channel at full value, plus ~1 mA idle per LED, so ~61 mA for full
  // white. (FastLED's own estimate is a little lower.) Go over 500 mA with
  // the limiter off and the board browns out and resets.
  import { onDestroy } from 'svelte';
  import {
    POWER_LIMIT_MA,
    displayCss,
    namedColor,
    type Rgb,
  } from '../guide/leds';

  function frameMilliamps(f: readonly Rgb[], b: number): number {
    let full = 0;
    for (const c of f) full += ((c.r + c.g + c.b) / 255) * 20;
    return (full * b) / 255 + f.length;
  }
  function limitedBrightness(f: readonly Rgb[], requested: number): number {
    if (frameMilliamps(f, requested) <= POWER_LIMIT_MA) return requested;
    const full = frameMilliamps(f, 255) - f.length;
    return Math.max(0, Math.floor(((POWER_LIMIT_MA - f.length) * 255) / full));
  }

  type Fill = 'White' | 'Red' | 'Rainbow' | 'Blue';
  let count = $state(10);
  let brightness = $state(120);
  let fill = $state<Fill>('White');
  let limiter = $state(false);

  const RAINBOW = ['Red', 'Orange', 'Yellow', 'Lime', 'Cyan', 'Blue', 'Purple', 'Magenta'];

  const frame = $derived.by<Rgb[]>(() =>
    Array.from({ length: count }, (_, i) =>
      fill === 'Rainbow' ? namedColor(RAINBOW[i % RAINBOW.length]).rgb : namedColor(fill).rgb,
    ),
  );
  const effective = $derived(limiter ? limitedBrightness(frame, brightness) : brightness);
  const ma = $derived(Math.round(frameMilliamps(frame, effective)));
  const over = $derived(ma > POWER_LIMIT_MA);

  // Brownout: the board resets and the strip blinks off, over and over.
  let resetting = $state(false);
  let resets = $state(0);
  let timer: ReturnType<typeof setInterval> | undefined;
  $effect(() => {
    clearInterval(timer);
    resetting = false;
    if (over) {
      timer = setInterval(() => {
        resetting = true;
        resets += 1;
        setTimeout(() => (resetting = false), 450);
      }, 1400);
    }
  });
  onDestroy(() => clearInterval(timer));

  const scaleMax = $derived(Math.max(1000, Math.ceil(ma / 500) * 500));
</script>

<div class="power">
  <div class="grid">
    <div class="sliders">
      <label>
        <span>LEDs <strong>{count}</strong></span>
        <input type="range" min="1" max="60" bind:value={count} />
      </label>
      <label>
        <span>Brightness <strong>{brightness}</strong>/255</span>
        <input type="range" min="0" max="255" bind:value={brightness} />
      </label>
      <div class="fills" role="radiogroup" aria-label="Color">
        {#each ['White', 'Red', 'Blue', 'Rainbow'] as f (f)}
          <button type="button" role="radio" aria-checked={fill === f} class:on={fill === f} onclick={() => (fill = f as Fill)}>{f}</button>
        {/each}
      </div>
      <label class="toggle">
        <input type="checkbox" bind:checked={limiter} />
        <code>FastLED.setMaxPowerInVoltsAndMilliamps(5, 500);</code>
      </label>
    </div>

    <div class="readout" class:over={over}>
      <div class="big">{ma}<span> mA</span></div>
      <div class="meter" style:--pct="{(ma / scaleMax) * 100}%" style:--limit="{(POWER_LIMIT_MA / scaleMax) * 100}%">
        <span class="fill"></span><span class="limit"></span>
      </div>
      <div class="scale"><span>0</span><span>USB: 500 mA</span><span>{scaleMax}</span></div>
      <p class="msg" aria-live="polite">
        {#if over}
          Over budget. The voltage sags, the chip browns out and resets. <strong>Resets: {resets}</strong>
        {:else if limiter && effective < brightness}
          The limiter scaled brightness from {brightness} to <strong>{effective}</strong> to stay under 500 mA.
        {:else}
          Within what a USB port can supply.
        {/if}
      </p>
    </div>
  </div>

  <div class="strip" class:reset={resetting} role="img" aria-label="{count} LEDs, {resetting ? 'off while the board resets' : 'lit'}">
    {#each frame as c, i (i)}
      <span class="led" style:--led={resetting ? 'transparent' : displayCss(c, effective)}></span>
    {/each}
  </div>
  {#if resetting}
    <p class="serial"><code>rst:0xf (BROWNOUT_RST)</code> Brownout detector was triggered</p>
  {/if}
</div>

<style>
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: var(--space-5);
    margin-bottom: var(--space-4);
  }
  .sliders {
    display: grid;
    gap: var(--space-3);
  }
  .sliders label {
    display: grid;
    gap: var(--space-1);
    margin: 0;
    font-size: var(--fs-md);
    font-weight: 400;
    color: var(--text);
  }
  input[type='range'] {
    accent-color: var(--accent);
  }
  .fills {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }
  .fills button {
    font: inherit;
    font-size: var(--fs-base);
    padding: 4px 12px;
    border-radius: 999px;
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  .fills button.on {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .toggle {
    display: flex !important;
    grid-template-columns: none !important;
    align-items: center;
    gap: var(--space-2) !important;
  }
  .toggle code {
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    overflow-wrap: anywhere;
  }
  .readout {
    padding: var(--space-4);
    border: 2px solid var(--accent);
    border-radius: 6px;
    background: var(--bg-panel);
  }
  .readout.over {
    border-color: var(--danger);
  }
  .big {
    font-family: var(--font-mono);
    font-size: clamp(32px, 7vw, 48px);
    font-weight: 700;
    line-height: 1;
  }
  .big span {
    font-size: 0.45em;
    color: var(--text-faint);
  }
  .over .big {
    color: var(--danger);
  }
  .meter {
    position: relative;
    height: 12px;
    margin-top: var(--space-3);
    background: var(--bg-header);
    border-radius: 6px;
    overflow: hidden;
  }
  .fill {
    position: absolute;
    inset: 0 auto 0 0;
    width: var(--pct);
    background: var(--accent);
    transition: width 0.15s;
  }
  .over .fill {
    background: var(--danger);
  }
  .limit {
    position: absolute;
    top: 0;
    bottom: 0;
    left: var(--limit);
    width: 3px;
    background: var(--text);
  }
  .scale {
    display: flex;
    justify-content: space-between;
    margin-top: 4px;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    color: var(--text-faint);
  }
  .msg {
    margin: var(--space-3) 0 0;
    font-size: var(--fs-md);
  }
  .strip {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    padding: var(--space-3);
    background: var(--guide-pcb);
    border: 4px solid var(--guide-wood);
    border-radius: 6px;
  }
  .led {
    width: 18px;
    height: 18px;
    border-radius: 3px;
    background: var(--led);
    box-shadow: 0 0 10px 2px var(--led);
    border: 1px solid var(--guide-pcb-edge);
  }
  .strip.reset .led {
    background: var(--guide-led-off);
    box-shadow: none;
  }
  .serial {
    margin: var(--space-2) 0 0;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    color: var(--danger);
  }
</style>
