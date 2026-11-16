<script lang="ts">
  // An LED on an ESP32 pin. Flip it round, take the resistor out, change the
  // color, and see whether it lights and how much current flows. Logic in
  // resistorsLeds.ts.
  import { LEDS, PIN_VOLTS, formatAmps, ledById, ledState, type LedColor } from './resistorsLeds';

  const R = 220;
  const uid = $props.id();
  const glowId = `${uid}-glow`;
  let reversed = $state(false);
  let withResistor = $state(true);
  let color = $state<LedColor>('red');

  const led = $derived(ledById(color));
  const s = $derived(ledState(PIN_VOLTS, led.vf, withResistor ? R : null, reversed));
  const lit = $derived(s.kind === 'lit');
  const glow = $derived(s.kind === 'lit' ? 0.25 + 0.75 * s.brightness : 0);
  const flowing = $derived(s.kind === 'lit' || s.kind === 'no-resistor');

  // The LED's legs: the long one is the anode (+), the short one the cathode (−).
  const LONG_END = 168;
  const SHORT_END = 150;
  const leftEnd = $derived(reversed ? SHORT_END : LONG_END);
  const rightEnd = $derived(reversed ? LONG_END : SHORT_END);

  const heading = $derived(
    s.kind === 'reversed'
      ? 'Dark: the LED is backwards'
      : s.kind === 'too-low'
        ? 'Dark: 3.3 V isn’t enough'
        : s.kind === 'no-resistor'
          ? 'No resistor: too much current'
          : `Lit: ${formatAmps(s.amps)}`,
  );
</script>

<div class="lb">
  <div class="controls">
    <button type="button" class="flip" onclick={() => (reversed = !reversed)}>Flip the LED</button>
    <label class="check">
      <input type="checkbox" bind:checked={withResistor} />
      {R} Ω resistor in the circuit
    </label>
  </div>
  <div class="group" role="radiogroup" aria-label="LED color">
    {#each LEDS as l (l.id)}
      <button type="button" role="radio" aria-checked={color === l.id} class:on={color === l.id} onclick={() => (color = l.id)}>
        <span class="sw" style:background={l.light}></span>{l.name}
      </button>
    {/each}
  </div>

  <figure>
    <svg
      viewBox="0 0 340 200"
      role="img"
      aria-label="{led.name} LED, long leg on the {reversed ? 'ground' : 'pin'} side, {withResistor ? `through a ${R} Ω resistor` : 'with no resistor'}. {heading}."
    >
      <defs>
        <radialGradient id={glowId}>
          <stop offset="0%" stop-color={led.light} stop-opacity="0.9" />
          <stop offset="100%" stop-color={led.light} stop-opacity="0" />
        </radialGradient>
      </defs>

      <!-- the pin and ground -->
      <rect x="4" y={leftEnd - 14} width="70" height="28" rx="4" class="box" />
      <text x="39" y={leftEnd + 4} class="t" text-anchor="middle">GPIO 3.3 V</text>
      <rect x="282" y={rightEnd - 14} width="54" height="28" rx="4" class="box" />
      <text x="309" y={rightEnd + 4} class="t" text-anchor="middle">GND</text>

      <!-- left wire: pin → resistor → left leg -->
      <line x1="74" y1={leftEnd} x2="96" y2={leftEnd} class="wire" class:flow={flowing} />
      {#if withResistor}
        <line x1="96" y1={leftEnd} x2="150" y2={leftEnd} class="wire" class:flow={flowing} />
        <rect x="104" y={leftEnd - 8} width="38" height="16" rx="6" class="rbody" />
        <rect x="111" y={leftEnd - 8} width="4" height="16" fill="#d7261e" />
        <rect x="118" y={leftEnd - 8} width="4" height="16" fill="#d7261e" />
        <rect x="125" y={leftEnd - 8} width="4" height="16" fill="#7a4a24" />
        <rect x="134" y={leftEnd - 8} width="3" height="16" fill="#c9a227" />
        <text x="123" y={leftEnd - 14} class="t" text-anchor="middle">{R} Ω</text>
      {:else}
        <line x1="96" y1={leftEnd} x2="150" y2={leftEnd} class="wire" class:flow={flowing} />
      {/if}
      <line x1="150" y1={leftEnd} x2="186" y2={leftEnd} class="wire" class:flow={flowing} />
      <!-- right wire: right leg → ground -->
      <line x1="214" y1={rightEnd} x2="282" y2={rightEnd} class="wire" class:flow={flowing} />

      <!-- the LED -->
      {#if lit}
        <circle cx="200" cy="62" r={30 + 30 * glow} fill="url(#{glowId})" opacity={glow} />
      {/if}
      <line x1="194" y1="104" x2="194" y2={leftEnd} class="leg" />
      <line x1="206" y1="104" x2="206" y2={rightEnd} class="leg" />
      <line x1="186" y1={leftEnd} x2="194" y2={leftEnd} class="leg" />
      <line x1="206" y1={rightEnd} x2="214" y2={rightEnd} class="leg" />
      <path
        d="M182,98 L182,62 A18,18 0 0 1 218,62 L218,98 Z"
        class="dome"
        class:burnt={s.kind === 'no-resistor'}
        style:fill={s.kind === 'no-resistor' ? undefined : led.light}
        style:fill-opacity={s.kind === 'no-resistor' ? undefined : lit ? 0.55 + 0.45 * glow : 0.3}
      />
      <!-- the rim, with its flat edge on the cathode side -->
      {#if reversed}
        <path d="M182,98 L222,98 L222,104 L182,104 Z" class="rim" />
      {:else}
        <path d="M178,98 L218,98 L218,104 L178,104 Z" class="rim" />
      {/if}
      <text x={reversed ? 160 : 240} y="80" class="t muted" text-anchor="middle">flat</text>
      <text x="160" y="132" class="t" text-anchor="middle">{reversed ? '−' : '+'}</text>
      <text x="240" y="132" class="t" text-anchor="middle">{reversed ? '+' : '−'}</text>
      <text x="200" y="196" class="t muted" text-anchor="middle">long leg = + (anode)</text>
    </svg>
  </figure>

  <div class="status" class:bad={s.kind === 'no-resistor'} aria-live="polite">
    <h3>{heading}</h3>
    <p>
      {#if s.kind === 'reversed'}
        An LED is a diode: current only flows from the long leg (+) to the short leg (−). Backwards, nothing flows and nothing breaks. Flip it back.
      {:else if s.kind === 'too-low'}
        This LED needs about {led.vf} V before it conducts, and the pin only gives {PIN_VOLTS} V.
      {:else if s.kind === 'no-resistor'}
        Nothing limits the current. The LED and the pin both try to pass far more than the 20 mA they are good for, and something gets hot. Put the resistor back.
      {:else}
        The {led.name.toLowerCase()} LED drops about {led.vf} V, leaving {(PIN_VOLTS - led.vf).toFixed(1)} V across the {R} Ω resistor:
        {(PIN_VOLTS - led.vf).toFixed(1)} V ÷ {R} Ω = {formatAmps(s.amps)}.
        {#if led.vf >= 3}
          Blue and white need about 3 V or more, so a 3.3 V pin has little left over and they glow dimly. How dimly varies from LED to LED.
        {/if}
      {/if}
    </p>
  </div>
</div>

<style>
  .controls {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
    align-items: center;
    margin-bottom: var(--space-2);
  }
  .flip {
    font: inherit;
    font-size: var(--fs-base);
    padding: 6px 14px;
    border-radius: var(--radius);
    border: var(--border-w) solid var(--accent);
    background: var(--accent);
    color: var(--accent-text);
    cursor: pointer;
  }
  .check {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    font-size: var(--fs-base);
    font-weight: 400;
    color: var(--text);
  }
  .group {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin-bottom: var(--space-3);
  }
  .group button {
    display: inline-flex;
    align-items: center;
    gap: 6px;
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
  .sw {
    width: 10px;
    height: 10px;
    border-radius: 50%;
    border: 1px solid var(--border-strong);
  }
  figure {
    margin: 0;
  }
  svg {
    display: block;
    width: 100%;
    max-width: 520px;
    height: auto;
  }
  .box {
    fill: var(--bg-panel);
    stroke: var(--border-strong);
  }
  .wire {
    stroke: var(--text-muted);
    stroke-width: 2.5;
    stroke-linecap: round;
  }
  .wire.flow {
    stroke: var(--accent);
    stroke-dasharray: 6 5;
    animation: flow 0.8s linear infinite;
  }
  @keyframes flow {
    to {
      stroke-dashoffset: -11;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .wire.flow {
      animation: none;
    }
  }
  .rbody {
    fill: #dcc49c;
    stroke: #a88b5c;
  }
  .leg {
    stroke: #9aa0a6;
    stroke-width: 2.5;
  }
  .dome {
    stroke: var(--border-strong);
    stroke-width: 1.5;
  }
  .dome.burnt {
    fill: #3a2a20;
    fill-opacity: 1;
  }
  .rim {
    fill: var(--border-strong);
  }
  .t {
    font-family: var(--font-mono);
    font-size: 11px;
    fill: var(--text);
  }
  .t.muted {
    fill: var(--text-muted);
  }
  .status h3 {
    margin: var(--space-3) 0 var(--space-1);
    font-size: var(--fs-lg);
  }
  .status p {
    margin: 0;
    font-size: var(--fs-md);
  }
  .status.bad h3 {
    color: var(--danger);
  }
</style>
