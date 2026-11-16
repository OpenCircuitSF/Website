<script lang="ts">
  // Set the voltage on an analog pin and compare one analogRead(), the
  // average of 16, and analogReadMilliVolts(). Then size a voltage divider
  // for something above the ADC's range. Model in esp32c3PinsPower.ts.
  import { ADC_MAX_RAW, analogZone, averageReading, divider, milliVolts, rawReading } from './esp32c3PinsPower';

  let volts = $state(1.2);
  let seed = $state(1);

  const raw = $derived(rawReading(volts, seed));
  const avg = $derived(averageReading(volts, seed));
  const mv = $derived(milliVolts(volts, seed));
  const zone = $derived(analogZone(volts));
  const ZONE_TEXT = {
    ok: 'In range: readings track the voltage.',
    top: 'Above about 2.5 V: readings flatten out and stop being accurate.',
    over: 'At the very top: the reading is pinned near its maximum.',
    danger: 'More than 3.3 V on a GPIO can damage the chip.',
  } as const;

  const VINS = [
    { v: 4.2, label: 'LiPo battery (4.2 V full)' },
    { v: 5, label: 'USB 5 V' },
    { v: 9, label: '9 V battery' },
  ];
  const RS = [10, 22, 47, 100];
  let vin = $state(4.2);
  let r1 = $state(100);
  let r2 = $state(100);
  const div = $derived(divider(vin, r1, r2));
</script>

<div class="analog">
  <label class="slider">
    <span>Voltage on GPIO 3: <strong>{volts.toFixed(2)} V</strong></span>
    <input type="range" min="0" max="4" step="0.05" bind:value={volts} />
  </label>

  <div class="readings" class:danger={zone === 'danger'}>
    <div class="cell">
      <span class="lab">analogRead()</span>
      <span class="num">{raw}</span>
      <span class="sub">one noisy reading, 0–{ADC_MAX_RAW}</span>
    </div>
    <div class="cell">
      <span class="lab">average of 16</span>
      <span class="num">{avg}</span>
      <span class="sub">steadier</span>
    </div>
    <div class="cell">
      <span class="lab">analogReadMilliVolts()</span>
      <span class="num">{mv} mV</span>
      <span class="sub">calibrated</span>
    </div>
  </div>
  <div class="bar" aria-hidden="true"><span style:width="{(raw / ADC_MAX_RAW) * 100}%"></span></div>
  <p class="zone {zone}" aria-live="polite">{ZONE_TEXT[zone]}</p>
  <button type="button" class="again" onclick={() => (seed += 1)}>Read again</button>

  <h3>Measuring more than 2.5 V: a voltage divider</h3>
  <p class="how">Two resistors in series from the voltage to ground. The pin reads the point between them: R1 on top, R2 to ground.</p>
  <div class="divider">
    <label>Measuring
      <select bind:value={vin}>
        {#each VINS as o (o.v)}<option value={o.v}>{o.label}</option>{/each}
      </select>
    </label>
    <label>R1 (top)
      <select bind:value={r1}>
        {#each RS as r (r)}<option value={r}>{r} kΩ</option>{/each}
      </select>
    </label>
    <label>R2 (to ground)
      <select bind:value={r2}>
        {#each RS as r (r)}<option value={r}>{r} kΩ</option>{/each}
      </select>
    </label>
  </div>
  <p class="divout" class:bad={!div.ok} aria-live="polite">
    The pin sees <strong>{div.vout.toFixed(2)} V</strong>. {div.message}
  </p>
</div>

<style>
  .slider,
  .divider label {
    display: grid;
    gap: var(--space-1);
    margin: 0 0 var(--space-3);
    font-size: var(--fs-md);
    font-weight: 400;
    color: var(--text);
  }
  input[type='range'] {
    accent-color: var(--accent);
  }
  .readings {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
    gap: var(--space-2);
  }
  .cell {
    display: grid;
    padding: var(--space-2) var(--space-3);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
    background: var(--bg-panel);
    min-width: 0;
  }
  .lab {
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    color: var(--text-muted);
    overflow-wrap: anywhere;
  }
  .num {
    font-family: var(--font-mono);
    font-size: var(--fs-xl);
    font-weight: 700;
  }
  .sub {
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  .readings.danger .num {
    color: var(--danger);
  }
  .bar {
    height: 8px;
    margin: var(--space-3) 0 var(--space-2);
    border-radius: 4px;
    background: var(--bg-header);
    overflow: hidden;
  }
  .bar span {
    display: block;
    height: 100%;
    background: var(--accent);
  }
  .zone {
    margin: 0 0 var(--space-2);
    font-size: var(--fs-md);
  }
  .zone.top,
  .zone.over {
    color: var(--warning);
    font-weight: 600;
  }
  .zone.danger {
    color: var(--danger);
    font-weight: 700;
  }
  .again {
    font: inherit;
    font-size: var(--fs-base);
    padding: 4px 12px;
    border-radius: 999px;
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  h3 {
    margin: var(--space-5) 0 var(--space-1);
    font-size: var(--fs-lg);
  }
  .how {
    margin: 0 0 var(--space-3);
    font-size: var(--fs-base);
    color: var(--text-muted);
  }
  .divider {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
    gap: var(--space-2);
  }
  select {
    font: inherit;
    font-size: var(--fs-base);
    max-width: 100%;
  }
  .divout {
    margin: 0;
    padding: var(--space-3) var(--space-4);
    border-left: 4px solid var(--accent);
    background: var(--bg-panel);
    font-size: var(--fs-md);
  }
  .divout.bad {
    border-left-color: var(--danger);
  }
</style>
