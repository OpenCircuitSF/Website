<script lang="ts">
  // A HUB75 panel on a Pi 4 with the five settings that fix it. Nothing here moves or blinks (WCAG 2.3.1): flicker, shimmer and jitter are static renderings plus a label.
  // Logic in piProjects.ts.
  import {
    MATRIX_START,
    PANEL_H,
    PANEL_W,
    colorLevels,
    matrixCommand,
    matrixState,
    panelPixels,
    rowOpacity,
    type GpioMapping,
    type MatrixConfig,
    type Problem,
  } from './piProjects';

  const CELL = 10;
  const MAPPINGS: GpioMapping[] = ['regular', 'adafruit-hat', 'adafruit-hat-pwm'];

  let cfg = $state<MatrixConfig>({ ...MATRIX_START });
  const s = $derived(matrixState(cfg));
  const pixels = $derived(panelPixels(s, cfg.pwmBits));

  const heading = $derived.by(() => {
    const p = s.problems[0];
    if (p === 'sound') return 'Dark: the library won’t start with onboard sound on';
    if (p === 'mapping') return 'Garbage: the GPIO mapping doesn’t match the HAT';
    if (p === 'slowdown') return s.look === 'garbage' ? 'Garbage: the Pi 4 is too fast for the panel' : 'Shimmer: the Pi 4 is still a little too fast';
    if (p === 'core') return 'Uneven: refresh dips when Linux uses the panel’s core';
    if (p === 'refresh') return 'Visible flicker: refresh is too low';
    return 'Steady';
  });

  const refreshText = $derived(
    !s.runs ? 'Refresh: none (program not running)' : s.jitter ? `Refresh: ${s.refreshLowHz}–${s.refreshHz} Hz` : `Refresh: ${s.refreshHz} Hz`,
  );

  const FIXES: Record<Problem, string> = {
    sound: 'Turn the onboard sound off and reboot.',
    mapping: 'Set --led-gpio-mapping to match the adapter: adafruit-hat, or adafruit-hat-pwm only with the GPIO 4 to GPIO 18 wire.',
    slowdown: 'Raise --led-slowdown-gpio to 2 or more.',
    core: 'Reserve core 3 for the panel with isolcpus.',
    refresh: 'Use fewer PWM bits or fewer chained panels to raise the refresh rate.',
  };
</script>

<div class="rm">
  <div class="group" role="radiogroup" aria-label="GPIO slowdown (--led-slowdown-gpio)">
    {#each [0, 1, 2, 3, 4] as n (n)}
      <button type="button" role="radio" aria-checked={cfg.slowdown === n} class:on={cfg.slowdown === n} onclick={() => (cfg.slowdown = n)}>{n}</button>
    {/each}
  </div>
  <div class="checks">
    <label class="check"><input type="checkbox" bind:checked={cfg.soundOff} /> Onboard sound off (dtparam=audio=off)</label>
    <label class="check"><input type="checkbox" bind:checked={cfg.isolatedCore} /> Core 3 reserved for the panel (isolcpus)</label>
  </div>
  <div class="group" role="radiogroup" aria-label="GPIO mapping (--led-gpio-mapping)">
    {#each MAPPINGS as m (m)}
      <button type="button" role="radio" aria-checked={cfg.mapping === m} class:on={cfg.mapping === m} onclick={() => (cfg.mapping = m)}>{m}</button>
    {/each}
  </div>
  <div class="checks">
    <label class="check"><input type="checkbox" bind:checked={cfg.pwmMod} /> PWM mod soldered (GPIO 4 to GPIO 18)</label>
  </div>
  <label class="range">
    PWM bits (--led-pwm-bits)
    <input type="range" min="1" max="11" step="1" bind:value={cfg.pwmBits} />
    <output>{cfg.pwmBits}</output>
  </label>
  <div class="group" role="radiogroup" aria-label="Panels in the chain">
    {#each [1, 2, 3] as n (n)}
      <button type="button" role="radio" aria-checked={cfg.chain === n} class:on={cfg.chain === n} onclick={() => (cfg.chain = n)}>{n} panel{n > 1 ? 's' : ''}</button>
    {/each}
  </div>

  <figure>
    <svg viewBox="0 0 {PANEL_W * CELL} {PANEL_H * CELL}" role="img" aria-label="{heading}. {refreshText}.">
      <rect x="0" y="0" width={PANEL_W * CELL} height={PANEL_H * CELL} fill="#111111" />
      {#each pixels as fill, i (i)}
        {@const row = Math.floor(i / PANEL_W)}
        <rect
          class="cell"
          x={(i % PANEL_W) * CELL + 1}
          y={row * CELL + 1}
          width={CELL - 2}
          height={CELL - 2}
          rx="2"
          {fill}
          opacity={rowOpacity(row, s)}
        />
      {/each}
      {#if !s.runs}
        <text x={(PANEL_W * CELL) / 2} y={(PANEL_H * CELL) / 2 + 5} class="exit" text-anchor="middle">Program exited</text>
      {/if}
    </svg>
  </figure>

  <div class="status" class:bad={s.problems.length > 0} aria-live="polite">
    <h3>{heading}</h3>
    <p>{refreshText} · {colorLevels(cfg.pwmBits)} brightness levels per color</p>
    {#each s.problems.slice(1) as p (p)}
      <p>{FIXES[p]}</p>
    {/each}
    {#if s.flicker === 'camera'}
      <p>Steady to the eye; a phone camera may catch bands.</p>
    {/if}
    <p class="muted">Illustrative numbers; measure yours with --led-show-refresh.</p>
  </div>

  <h4>Your settings</h4>
  {#if cfg.soundOff}
    <p class="lbl">In /boot/firmware/config.txt:</p>
    <pre class="code"><code>dtparam=audio=off</code></pre>
  {/if}
  {#if cfg.isolatedCore}
    <p class="lbl">At the end of the one line in /boot/firmware/cmdline.txt:</p>
    <pre class="code"><code>isolcpus=domain,managed_irq,3 nohz_full=3 rcu_nocbs=3 irqaffinity=0,1,2</code></pre>
  {/if}
  <p class="lbl">Run the library’s demo:</p>
  <pre class="code"><code>{matrixCommand(cfg)}</code></pre>

  <p class="see-also">See also: <a href="/guides/led-power-budget">How much power a panel needs</a>.</p>
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
  .checks {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
    margin-bottom: var(--space-3);
  }
  .check,
  .range {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    font-size: var(--fs-base);
    font-weight: 400;
    color: var(--text);
  }
  .range {
    margin-bottom: var(--space-3);
  }
  figure {
    margin: 0;
  }
  svg {
    display: block;
    width: 100%;
    max-width: 480px;
    height: auto;
  }
  .exit {
    font-family: var(--font-mono);
    font-size: 16px;
    fill: #bbbbbb;
  }
  .status h3 {
    margin: var(--space-3) 0 var(--space-1);
    font-size: var(--fs-lg);
  }
  .status p {
    margin: 0 0 var(--space-1);
    font-size: var(--fs-md);
  }
  .status.bad h3 {
    color: var(--danger);
  }
  .muted,
  .lbl {
    color: var(--text-muted);
    font-size: var(--fs-sm);
  }
  h4 {
    margin: var(--space-3) 0 var(--space-1);
  }
  .code {
    margin: 0 0 var(--space-2);
    padding: var(--space-3);
    border-radius: 6px;
    background: var(--bg-panel);
    border: var(--border-w) solid var(--border);
    font-size: var(--fs-sm);
    overflow-x: auto;
  }
  .see-also {
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  .see-also a {
    color: var(--accent);
  }
</style>
