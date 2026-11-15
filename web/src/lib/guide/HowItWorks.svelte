<script lang="ts">
  // time -> position -> color, live, with a scrubber. Shows the one line of
  // the firmware that makes the animation (`position + i * LED_SPREAD`) and
  // the delay() vs millis() comparison from the class plan.
  import LedStrip from './LedStrip.svelte';
  import { Clock } from './clock.svelte';
  import {
    DEFAULT_CONFIG,
    describePosition,
    displayCss,
    namedColor,
    physicalStrip,
    positionAt,
    renderFrame,
  } from './leds';

  const clock = new Clock();
  const cfg = DEFAULT_CONFIG;
  const cycle = cfg.stepMs * cfg.palette.length;

  const t = $derived(Math.floor(clock.elapsed % cycle));
  const position = $derived(positionAt(t, cfg.stepMs, cfg.palette.length));
  const where = $derived(describePosition(position, cfg.palette));
  const frame = $derived(renderFrame(t, cfg));
</script>

<div class="how">
  <div class="flow">
    <div class="box">
      <span class="k">time</span>
      <span class="v">millis() = {t}</span>
      <span class="note">ms since power-on, wrapped every {cycle} ms</span>
    </div>
    <span class="arrow" aria-hidden="true">→</span>
    <div class="box">
      <span class="k">position</span>
      <span class="v">{position}</span>
      <span class="note">256 units per color step</span>
    </div>
    <span class="arrow" aria-hidden="true">→</span>
    <div class="box">
      <span class="k">color</span>
      <span class="v">
        <span class="sw" style:--sw={displayCss(namedColor(where.from).rgb, 255)}></span>
        {where.from.toLowerCase()}
        <span class="text-faint">+{where.amount}/256</span>
        <span class="sw" style:--sw={displayCss(namedColor(where.to).rgb, 255)}></span>
        {where.to.toLowerCase()}
      </span>
      <span class="note">blend() of two palette entries</span>
    </div>
  </div>

  <label class="scrub">
    <span>Scrub time</span>
    <input
      type="range"
      min="0"
      max={cycle - 1}
      value={t}
      oninput={(e) => clock.seek(+e.currentTarget.value)}
    />
    <button type="button" class="ghost" onclick={() => clock.toggle()}>{clock.playing ? '❚❚' : '▶'}</button>
  </label>

  <LedStrip leds={physicalStrip(frame, cfg)} brightness={cfg.brightness} label="LED 0 to 7 at this moment" />

  <p>
    Every LED asks for its own position: <code>position + i * LED_SPREAD</code>. LED 3 is always
    {3 * cfg.ledSpread} units ahead of LED 0. That one <code>+ i *</code> is the entire animation.
  </p>

  <div class="compare">
    <div>
      <h4>The tempting way</h4>
      <pre><code>digitalWrite(pin, HIGH);
delay(1000);
digitalWrite(pin, LOW);
delay(1000);</code></pre>
      <p class="bad">delay() stops the whole program. No button, sensor or serial command can be noticed while it waits.</p>
    </div>
    <div>
      <h4>What this project does</h4>
      <pre><code>if (millis() - lastRun &gt;= interval) &#123;
    lastRun = millis();
    doTheThing();
&#125;</code></pre>
      <p class="good">The loop keeps running. Anything you add later (buttons, sensors, WiFi) still works.</p>
    </div>
  </div>
</div>

<style>
  .how {
    display: grid;
    gap: var(--space-4);
  }
  .flow {
    display: flex;
    flex-wrap: wrap;
    align-items: stretch;
    gap: var(--space-2);
  }
  .box {
    flex: 1 1 180px;
    display: grid;
    gap: 4px;
    padding: var(--space-3);
    background: var(--bg-panel);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
  }
  .k {
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    text-transform: uppercase;
    color: var(--accent);
    letter-spacing: 0.04em;
  }
  .v {
    font-family: var(--font-mono);
    font-size: var(--fs-lg);
    font-variant-numeric: tabular-nums;
  }
  .note {
    font-size: var(--fs-sm);
    color: var(--text-faint);
  }
  .arrow {
    align-self: center;
    font-size: var(--fs-xl);
    color: var(--accent-dim);
  }
  .sw {
    display: inline-block;
    width: 12px;
    height: 12px;
    border-radius: 2px;
    background: var(--sw);
    vertical-align: -1px;
  }
  .scrub {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    margin: 0;
    font-size: var(--fs-base);
  }
  .scrub input {
    flex: 1;
    accent-color: var(--accent);
  }
  .ghost {
    font: inherit;
    padding: var(--space-1) var(--space-3);
    background: transparent;
    border: var(--border-w) solid var(--border-strong);
    border-radius: var(--radius);
    color: var(--text);
    cursor: pointer;
  }
  p {
    margin: 0;
    font-size: var(--fs-md);
    max-width: var(--measure);
  }
  code {
    font-family: var(--font-mono);
    background: var(--bg-subtle);
    padding: 1px 4px;
    border-radius: 3px;
  }
  .compare {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: var(--space-4);
  }
  h4 {
    margin: 0 0 var(--space-2);
  }
  pre {
    margin: 0 0 var(--space-2);
    padding: var(--space-3);
    background: var(--bg-subtle);
    border-radius: var(--radius);
    overflow-x: auto;
  }
  pre code {
    background: none;
    padding: 0;
  }
  .bad {
    color: var(--danger);
    font-size: var(--fs-base);
  }
  .good {
    color: var(--accent);
    font-size: var(--fs-base);
  }
</style>
