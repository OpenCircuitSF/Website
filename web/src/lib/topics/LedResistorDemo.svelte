<script lang="ts">
  // Pick a supply, an LED color and a current; get the resistor. Logic in
  // resistorsLeds.ts.
  import {
    BAND_FILL,
    LEDS,
    LED_MAX_A,
    PIN_MAX_A,
    PIN_VOLTS,
    QUARTER_WATT,
    bandsFor,
    formatAmps,
    formatOhms,
    formatVolts,
    formatWatts,
    ledById,
    ledResistor,
    type LedColor,
  } from './resistorsLeds';

  type Supply = 'pin' | 'usb' | 'custom';
  const SUPPLIES: { id: Supply; label: string }[] = [
    { id: 'pin', label: 'ESP32 pin (3.3 V)' },
    { id: 'usb', label: 'USB 5 V' },
    { id: 'custom', label: 'Custom' },
  ];
  let supply = $state<Supply>('pin');
  let custom = $state(9);
  let color = $state<LedColor>('red');
  let mA = $state(10);

  const volts = $derived(supply === 'pin' ? PIN_VOLTS : supply === 'usb' ? 5 : Math.max(0, Number(custom) || 0));
  const led = $derived(ledById(color));
  const r = $derived(ledResistor(volts, led.vf, mA / 1000, supply === 'pin'));
  const bands = $derived(r ? bandsFor(r.chosen, 4)?.bands : undefined);
</script>

<div class="lr">
  <div class="group" role="radiogroup" aria-label="Supply">
    {#each SUPPLIES as s (s.id)}
      <button type="button" role="radio" aria-checked={supply === s.id} class:on={supply === s.id} onclick={() => (supply = s.id)}>{s.label}</button>
    {/each}
  </div>
  {#if supply === 'custom'}
    <label class="num">
      <span>Supply voltage (V)</span>
      <input type="number" min="1" max="24" step="0.1" bind:value={custom} />
    </label>
  {/if}
  <div class="group" role="radiogroup" aria-label="LED color">
    {#each LEDS as l (l.id)}
      <button type="button" role="radio" aria-checked={color === l.id} class:on={color === l.id} onclick={() => (color = l.id)}>
        <span class="sw" style:background={l.light}></span>{l.name} ({l.vf} V)
      </button>
    {/each}
  </div>
  <label class="slider">
    <span>Target current: <strong>{mA} mA</strong></span>
    <input type="range" min="1" max="30" step="1" bind:value={mA} aria-valuetext="{mA} milliamps" />
  </label>

  <figure>
    <svg viewBox="0 0 340 170" role="img" aria-label="{formatVolts(volts)} supply, {r ? `${formatOhms(r.chosen)} resistor` : 'resistor'}, {led.name} LED">
      <!-- source -->
      <line x1="30" y1="30" x2="30" y2="74" class="wire" />
      <line x1="14" y1="74" x2="46" y2="74" class="plate" />
      <line x1="22" y1="86" x2="38" y2="86" class="plate thick" />
      <line x1="30" y1="86" x2="30" y2="140" class="wire" />
      <text x="52" y="84" class="t">{formatVolts(volts)}</text>
      <!-- top wire, resistor, LED -->
      <line x1="30" y1="30" x2="90" y2="30" class="wire" />
      <path d="M90,30 L98,30 L103,22 L113,38 L123,22 L133,38 L143,22 L153,38 L158,30 L166,30" class="res" />
      <line x1="166" y1="30" x2="300" y2="30" class="wire" />
      <line x1="300" y1="30" x2="300" y2="60" class="wire" />
      <!-- LED symbol pointing down, cathode bar at the bottom -->
      {#if r}
        <circle cx="300" cy="78" r={14 + 10 * Math.min(1, r.amps / LED_MAX_A)} fill={led.light} opacity="0.35" />
      {/if}
      <path d="M288,66 L312,66 L300,86 Z" class="led" style:fill={led.light} />
      <line x1="288" y1="88" x2="312" y2="88" class="plate" />
      <path d="M316,70 L326,62 M318,80 L328,72" class="ray" />
      <line x1="300" y1="88" x2="300" y2="140" class="wire" />
      <line x1="300" y1="140" x2="30" y2="140" class="wire" />
      <text x="128" y="56" class="t" text-anchor="middle">{r ? formatOhms(r.chosen) : '—'}</text>
      <text x="128" y="70" class="t muted" text-anchor="middle">{r ? `${formatVolts(volts - led.vf)} across it` : ''}</text>
      <text x="276" y="82" class="t" text-anchor="end">{led.vf} V</text>
      <text x="165" y="134" class="t" text-anchor="middle">I = {r ? formatAmps(r.amps) : '0 mA'}</text>
      <!-- the resistor you'd pick, with its bands -->
      {#if bands}
        <line x1="40" y1="158" x2="140" y2="158" class="leg" />
        <rect x="60" y="150" width="60" height="16" rx="6" class="rbody" />
        {#each bands as b, i (i)}
          <rect x={68 + i * 8 + (i === 3 ? 12 : 0)} y="150" width="5" height="16" fill={BAND_FILL[b]} />
        {/each}
        <text x="150" y="162" class="t muted">{bands.join(' ')}</text>
      {/if}
    </svg>
  </figure>

  <div class="out" aria-live="polite">
    {#if r}
      <dl>
        <div><dt>Exact</dt><dd>({formatVolts(volts)} − {led.vf} V) ÷ {mA} mA = {formatOhms(r.exact)}</dd></div>
        <div><dt>Use (next E12 up)</dt><dd><strong>{formatOhms(r.chosen)}</strong></dd></div>
        <div><dt>Current you get</dt><dd>{formatAmps(r.amps)}</dd></div>
        <div><dt>Resistor power</dt><dd>{formatWatts(r.watts)} {r.watts <= QUARTER_WATT ? '(a 1/4 W part is fine)' : ''}</dd></div>
      </dl>
      {#if r.warnings.length === 0}
        <p class="ok">Good to go.{supply === 'pin' && r.amps > 0.01 ? ' For an indicator, 5–10 mA is plenty and easier on the pin.' : ''}</p>
      {:else}
        <ul class="warn">
          {#if r.warnings.includes('led-max')}<li>Over {formatAmps(LED_MAX_A)}: more than a plain 5 mm LED should carry.</li>{/if}
          {#if r.warnings.includes('pin')}<li>Over {formatAmps(PIN_MAX_A)} from an ESP32 pin. The pin’s voltage sags and you are near its limit. Aim for 5–10 mA.</li>{/if}
          {#if r.warnings.includes('power')}<li>{formatWatts(r.watts)} is more than a 1/4 W resistor can take. Use a 1/2 W part.</li>{/if}
        </ul>
      {/if}
      {#if supply !== 'pin'}
        <p class="note">An ESP32 pin can’t switch {formatVolts(volts)} directly: power the LED from the supply and switch it with a transistor.</p>
      {/if}
    {:else}
      <h3 class="bad">Can’t light it</h3>
      <p>A {formatVolts(volts)} supply is not above this LED’s {led.vf} V forward voltage, so no resistor will help. Use a higher supply.</p>
    {/if}
  </div>
</div>

<style>
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
  .num,
  .slider {
    display: grid;
    gap: var(--space-1);
    max-width: 20rem;
    margin-bottom: var(--space-3);
    font-size: var(--fs-base);
    font-weight: 400;
    color: var(--text);
  }
  .num input {
    font: inherit;
    padding: 4px 8px;
    max-width: 8rem;
  }
  input[type='range'] {
    accent-color: var(--accent);
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
  .wire,
  .plate {
    stroke: var(--text);
    stroke-width: 2;
  }
  .plate.thick {
    stroke-width: 4;
  }
  .res {
    fill: none;
    stroke: var(--accent);
    stroke-width: 2.5;
    stroke-linejoin: round;
  }
  .led {
    stroke: var(--text);
    stroke-width: 1.5;
  }
  .ray {
    stroke: var(--text-muted);
    stroke-width: 1.5;
  }
  .leg {
    stroke: #9aa0a6;
    stroke-width: 2.5;
  }
  .rbody {
    fill: #dcc49c;
    stroke: #a88b5c;
  }
  .t {
    font-family: var(--font-mono);
    font-size: 11px;
    fill: var(--text);
  }
  .t.muted {
    fill: var(--text-muted);
  }
  dl {
    display: grid;
    gap: var(--space-1);
    margin: var(--space-3) 0 var(--space-2);
    font-size: var(--fs-md);
  }
  dl div {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }
  dt {
    color: var(--text-muted);
    min-width: 10rem;
  }
  dd {
    margin: 0;
    font-variant-numeric: tabular-nums;
  }
  .ok,
  .note {
    margin: var(--space-2) 0 0;
    font-size: var(--fs-base);
  }
  .note {
    color: var(--text-muted);
  }
  .warn {
    margin: var(--space-2) 0 0;
    padding-left: 1.2em;
    color: var(--danger);
    font-size: var(--fs-md);
  }
  h3.bad {
    margin: var(--space-3) 0 var(--space-1);
    color: var(--danger);
    font-size: var(--fs-lg);
  }
</style>
