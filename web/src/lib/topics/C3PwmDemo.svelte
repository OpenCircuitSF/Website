<script lang="ts">
  // Drag the duty cycle and watch the square wave and the LED. Change the
  // frequency to see how much resolution is left. Model in esp32c3PinsPower.ts.
  import { dutyValue, gammaDuty, maxResolutionBits, perceived, periodLabel } from './esp32c3PinsPower';

  const FREQS = [50, 1000, 5000, 20000, 1000000];
  let level = $state(50);
  let freq = $state(5000);
  let gamma = $state(false);

  // With gamma correction on, the slider is "how bright it should look".
  const duty = $derived(gamma ? gammaDuty(level) : level);
  const bits = $derived(Math.min(8, maxResolutionBits(freq)));
  const maxBits = $derived(maxResolutionBits(freq));
  const value = $derived(dutyValue(duty, bits));
  const looks = $derived(perceived(duty));
  const freqLabel = (f: number) => (f >= 1000000 ? `${f / 1000000} MHz` : f >= 1000 ? `${f / 1000} kHz` : `${f} Hz`);

  // Three periods of the wave, drawn in a 300 × 60 box.
  const wave = $derived.by(() => {
    const w = 100;
    const on = (duty / 100) * w;
    let d = 'M0,50';
    for (let i = 0; i < 3; i++) {
      const x = i * w;
      if (on > 0) d += ` L${x},10 L${x + on},10`;
      d += ` L${x + on},50 L${x + w},50`;
    }
    return d;
  });
</script>

<div class="pwm">
  <label class="slider">
    <span>{gamma ? 'Brightness' : 'Duty cycle'}: <strong>{level}%</strong>{gamma ? ` (duty ${duty}%)` : ''}</span>
    <input type="range" min="0" max="100" step="1" bind:value={level} />
  </label>
  <label class="check">
    <input type="checkbox" bind:checked={gamma} />
    Gamma correction: the slider sets how bright it looks
  </label>

  <div class="layout">
    <figure>
      <svg viewBox="-4 0 308 64" role="img" aria-label="Square wave, on {duty}% of each cycle">
        <line x1="0" y1="50" x2="300" y2="50" class="axis" />
        <path d={wave} class="wave" />
        <text x="0" y="62" class="t">one cycle = {periodLabel(freq)}</text>
      </svg>
    </figure>
    <div class="led" aria-hidden="true">
      <span class="glow" style:opacity={looks / 100}></span>
      <span class="body"></span>
    </div>
  </div>
  <p class="looks" aria-live="polite">On {duty}% of the time. It looks about {looks}% bright.</p>

  <div class="group" role="radiogroup" aria-label="Frequency">
    {#each FREQS as f (f)}
      <button type="button" role="radio" aria-checked={freq === f} class:on={freq === f} onclick={() => (freq = f)}>{freqLabel(f)}</button>
    {/each}
  </div>
  <p class="res">
    At {freqLabel(freq)} the timer has room for up to <strong>{maxBits}-bit</strong> duty ({2 ** maxBits} steps).
    {#if freq < 100}Too slow for an LED: you’ll see it flicker.{:else if maxBits < 8}Too fast for smooth fading: only a few brightness steps.{:else}Fine for an LED.{/if}
  </p>
  <pre class="code"><code>ledcAttach(LED_PIN, {freq}, {bits});   // pin, frequency, bits
ledcWrite(LED_PIN, {value});          // 0–{2 ** bits - 1}</code></pre>
</div>

<style>
  .slider {
    display: grid;
    gap: var(--space-1);
    margin: 0 0 var(--space-2);
    font-size: var(--fs-md);
    font-weight: 400;
    color: var(--text);
  }
  input[type='range'] {
    accent-color: var(--accent);
  }
  .check {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    margin: 0 0 var(--space-3);
    font-size: var(--fs-base);
    font-weight: 400;
    color: var(--text);
  }
  .layout {
    display: grid;
    grid-template-columns: 1fr 72px;
    gap: var(--space-4);
    align-items: center;
  }
  figure {
    margin: 0;
    min-width: 0;
  }
  svg {
    display: block;
    width: 100%;
    height: auto;
  }
  .axis {
    stroke: var(--border);
    stroke-width: 1;
  }
  .wave {
    fill: none;
    stroke: var(--accent);
    stroke-width: 3;
    stroke-linejoin: round;
  }
  .t {
    font-family: var(--font-mono);
    font-size: 9px;
    fill: var(--text-muted);
  }
  .led {
    position: relative;
    width: 72px;
    height: 72px;
  }
  .glow {
    position: absolute;
    inset: 0;
    border-radius: 50%;
    background: radial-gradient(circle, #ff4a3d 0%, #ff4a3d 35%, transparent 70%);
  }
  .body {
    position: absolute;
    inset: 24px;
    border-radius: 50%;
    border: 2px solid var(--border-strong);
    background: color-mix(in srgb, #ff4a3d 30%, transparent);
  }
  .looks,
  .res {
    margin: var(--space-2) 0;
    font-size: var(--fs-md);
  }
  .group {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin-top: var(--space-3);
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
  .code {
    margin: 0;
    padding: var(--space-3);
    border-radius: 6px;
    background: var(--bg-panel);
    border: var(--border-w) solid var(--border);
    font-size: var(--fs-sm);
    overflow-x: auto;
  }
</style>
