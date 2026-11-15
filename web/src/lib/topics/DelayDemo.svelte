<script lang="ts">
  // Two simulated boards running the same blink, written two ways, sharing
  // one button. The delay() board only reads the button once every two
  // seconds -- the instant between its two delay(1000) calls -- so most
  // presses are never seen. The millis() board reads it on every pass.
  import { Clock } from '../guide/clock.svelte';

  const clock = new Clock();
  const PERIOD = 2000;

  let down = $state(false);
  let presses = $state(0);
  let heardDelay = $state(0);
  let heardMillis = $state(0);
  let pressHeardDelay = false;

  // What the delay() board's button LED shows: whatever digitalRead saw at
  // the last check.
  let delaySaw = $state(false);
  let lastCheck = -1;

  $effect(() => {
    const t = clock.elapsed;
    const check = Math.floor(t / PERIOD);
    if (check !== lastCheck) {
      lastCheck = check;
      delaySaw = down;
      if (down && !pressHeardDelay) {
        pressHeardDelay = true;
        heardDelay += 1;
      }
    }
  });

  function press() {
    if (down) return;
    down = true;
    presses += 1;
    pressHeardDelay = false;
    heardMillis += 1;
  }
  function release() {
    down = false;
  }
  function reset() {
    presses = heardDelay = heardMillis = 0;
  }

  const phase = $derived(clock.elapsed % PERIOD);
  const blinkOn = $derived(phase < 1000);
  // 0: the read, 1: on + delay, 2: off + delay
  const delayLine = $derived(phase < 60 ? 0 : phase < 1000 ? 1 : 2);
  const remaining = $derived(Math.ceil((phase < 1000 ? 1000 - phase : PERIOD - phase) / 100) / 10);
</script>

<svelte:window
  onkeydown={(e) => {
    if (e.code === 'Space' && (e.target as HTMLElement)?.dataset?.press === 'yes') {
      e.preventDefault();
      press();
    }
  }}
  onkeyup={(e) => e.code === 'Space' && release()}
  onpointerup={release}
/>

<div class="delay-demo">
  <div class="boards">
    <section class="board">
      <h4>With <code>delay()</code></h4>
      <div class="leds">
        <div class="led-wrap"><span class="led blink" class:on={blinkOn}></span><small>blink</small></div>
        <div class="led-wrap"><span class="led btn" class:on={delaySaw}></span><small>button LED</small></div>
      </div>
      <pre class="code"><span class:hl={delayLine === 0}>digitalWrite(BTN_LED, digitalRead(BTN) == LOW);</span>
<span class:hl={delayLine === 1}>digitalWrite(LED, HIGH); delay(1000);</span>
<span class:hl={delayLine === 2}>digitalWrite(LED, LOW);  delay(1000);</span></pre>
      <p class="status" class:bad={delayLine !== 0}>
        {delayLine === 0 ? 'Reading the button now!' : `Stuck in delay() for ${remaining.toFixed(1)} s more. Not listening.`}
      </p>
      <p class="score">Heard <strong>{heardDelay}</strong> of {presses} presses</p>
    </section>

    <section class="board">
      <h4>With <code>millis()</code></h4>
      <div class="leds">
        <div class="led-wrap"><span class="led blink" class:on={blinkOn}></span><small>blink</small></div>
        <div class="led-wrap"><span class="led btn" class:on={down}></span><small>button LED</small></div>
      </div>
      <pre class="code"><span class="hl">digitalWrite(BTN_LED, digitalRead(BTN) == LOW);</span>
<span class="hl">if (millis() - lastBlink &gt;= 1000) &#123;</span>
<span class="hl">  lastBlink = millis(); toggle(LED); &#125;</span></pre>
      <p class="status good">Loops thousands of times a second. Always listening.</p>
      <p class="score">Heard <strong>{heardMillis}</strong> of {presses} presses</p>
    </section>
  </div>

  <div class="controls">
    <button
      type="button"
      class="big"
      class:down
      data-press="yes"
      onpointerdown={(e) => {
        e.preventDefault();
        press();
      }}
    >
      {down ? 'Pressed' : 'Press the button'}
    </button>
    <button type="button" class="ghost" onclick={reset}>Reset score</button>
  </div>
  <p class="hint">Tap it quickly a few times. Then try holding it down for 2 seconds.</p>
</div>

<style>
  .boards {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
    gap: var(--space-4);
  }
  .board {
    padding: var(--space-4);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
    background: var(--bg-panel);
  }
  h4 {
    margin: 0 0 var(--space-3);
    font-size: var(--fs-lg);
  }
  code {
    font-family: var(--font-mono);
  }
  .leds {
    display: flex;
    gap: var(--space-5);
    padding: var(--space-3);
    background: var(--guide-pcb);
    border-radius: 6px;
    margin-bottom: var(--space-3);
  }
  .led-wrap {
    display: grid;
    justify-items: center;
    gap: 6px;
  }
  .led-wrap small {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-faint);
  }
  .led {
    width: 28px;
    height: 28px;
    border-radius: 50%;
    background: var(--guide-led-off);
    border: 1px solid var(--guide-pcb-edge);
    transition: background 0.05s, box-shadow 0.05s;
  }
  .led.blink.on {
    background: var(--guide-wire-5v);
    box-shadow: 0 0 16px 4px var(--guide-wire-5v);
  }
  .led.btn.on {
    background: var(--guide-wire-data);
    box-shadow: 0 0 16px 4px var(--guide-wire-data);
  }
  .code {
    margin: 0 0 var(--space-3);
    padding: var(--space-2);
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    background: var(--bg-subtle);
    border-radius: var(--radius);
    overflow-x: auto;
  }
  .code span {
    display: inline-block;
    padding: 1px 4px;
    border-radius: 2px;
  }
  .code .hl {
    background: var(--accent-subtle);
    box-shadow: inset 3px 0 0 var(--accent);
  }
  .status {
    margin: 0 0 var(--space-2);
    min-height: 2.6em;
    font-size: var(--fs-base);
    font-weight: 600;
  }
  .status.bad {
    color: var(--danger);
  }
  .status.good,
  .status:not(.bad) {
    color: var(--accent);
  }
  .score {
    margin: 0;
    font-family: var(--font-mono);
    font-size: var(--fs-md);
  }
  .score strong {
    font-size: var(--fs-xl);
  }
  .controls {
    display: flex;
    gap: var(--space-3);
    align-items: center;
    margin-top: var(--space-4);
  }
  .big {
    font: inherit;
    font-size: var(--fs-lg);
    font-weight: 700;
    padding: var(--space-3) var(--space-5);
    border-radius: 999px;
    border: 0;
    background: var(--accent);
    color: var(--accent-text);
    cursor: pointer;
    user-select: none;
    touch-action: manipulation;
    box-shadow: 0 4px 0 var(--accent-hover);
    transform: translateY(0);
    transition: transform 0.05s, box-shadow 0.05s;
  }
  .big.down {
    transform: translateY(3px);
    box-shadow: 0 1px 0 var(--accent-hover);
  }
  .ghost {
    font: inherit;
    font-size: var(--fs-base);
    padding: var(--space-2) var(--space-3);
    background: transparent;
    border: var(--border-w) solid var(--border-strong);
    border-radius: var(--radius);
    color: var(--text);
    cursor: pointer;
  }
  .hint {
    margin: var(--space-2) 0 0;
    font-size: var(--fs-base);
    color: var(--text-faint);
  }
</style>
