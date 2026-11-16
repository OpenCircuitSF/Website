<script lang="ts">
  // Deep sleep guide (#0672): a battery-life calculator. Wake for a few
  // seconds, sleep for minutes, and see the average current and how long a
  // battery lasts. The chart is one wake/sleep cycle on a log scale, because
  // awake and asleep differ by thousands of times. Numbers: wireless.ts.
  import {
    awakeCurrentMa,
    averageCurrentMa,
    batteryHours,
    formatCurrent,
    formatDuration,
    sleepCurrentMa,
    type SleepPlan,
  } from './wireless';

  let p = $state<SleepPlan>({ awakeS: 3, sleepMin: 10, wifi: true, boardParts: true, batteryMah: 1000 });

  const avg = $derived(averageCurrentMa(p));
  const life = $derived(formatDuration(batteryHours(p)));
  const awake = $derived(awakeCurrentMa(p.wifi));
  const asleep = $derived(sleepCurrentMa(p.boardParts));
  const neverSleeps = $derived(formatDuration(p.batteryMah / awake));

  // Chart: log10 current from 1 µA to 1 A. The awake pulse is drawn at
  // least a few pixels wide so it stays visible next to a long sleep.
  const W = 400;
  const H = 170;
  const L = 50;
  const B = 22;
  const y = (ma: number) => 6 + ((3 - Math.log10(ma)) / 6) * (H - B - 6);
  const cycle = $derived(p.awakeS + p.sleepMin * 60);
  const pulseW = $derived(Math.max(6, ((W - L - 6) * p.awakeS) / cycle));
  const path = $derived(
    `M${L},${y(awake)} h${pulseW} V${y(asleep)} H${W - 6}`,
  );
</script>

<div class="sleep">
  <div class="controls">
    <label>
      <span>Awake for <strong>{p.awakeS} s</strong> each time</span>
      <input type="range" min="1" max="30" bind:value={p.awakeS} />
    </label>
    <label>
      <span>Then sleep for <strong>{p.sleepMin} min</strong></span>
      <input type="range" min="1" max="120" bind:value={p.sleepMin} />
    </label>
    <label>
      <span>Battery <strong>{p.batteryMah} mAh</strong></span>
      <input type="range" min="200" max="3000" step="100" bind:value={p.batteryMah} />
    </label>
    <label class="check"><input type="checkbox" bind:checked={p.wifi} /> Use WiFi while awake</label>
    <label class="check"><input type="checkbox" bind:checked={p.boardParts} /> Count the board’s power LED and regulator</label>
  </div>

  <div class="layout">
    <svg viewBox="0 0 {W} {H}" role="img" aria-label="Current over one cycle: {formatCurrent(awake)} awake for {p.awakeS} seconds, then {formatCurrent(asleep)} asleep for {p.sleepMin} minutes.">
      {#each [[1000, '1 A'], [1, '1 mA'], [0.001, '1 µA']] as [ma, lbl] (lbl)}
        <line class="grid" x1={L} x2={W - 6} y1={y(ma as number)} y2={y(ma as number)} />
        <text class="tick" x={L - 4} y={y(ma as number) + 4} text-anchor="end">{lbl}</text>
      {/each}
      <path class="trace" d={path} />
      <text class="tick" x={L} y={H - 6}>awake</text>
      <text class="tick" x={W - 6} y={H - 6} text-anchor="end">asleep {p.sleepMin} min →</text>
    </svg>

    <div class="result" aria-live="polite">
      <p class="big">{life}</p>
      <p>on a {p.batteryMah} mAh battery. Average current <strong>{formatCurrent(avg)}</strong>: {formatCurrent(awake)} awake, {formatCurrent(asleep)} asleep.</p>
      <p class="muted">Never sleeping, it would last about {neverSleeps}. A rough estimate: real batteries give less.</p>
    </div>
  </div>
</div>

<style>
  .controls {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
    gap: var(--space-3);
    margin-bottom: var(--space-4);
  }
  label {
    display: grid;
    gap: var(--space-1);
    margin: 0;
    font-size: var(--fs-md);
    font-weight: 400;
    color: var(--text);
  }
  label.check {
    display: flex;
    align-items: flex-start;
    gap: var(--space-2);
  }
  input {
    accent-color: var(--accent);
  }
  label.check input {
    margin-top: 4px;
  }
  .layout {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: var(--space-4);
    align-items: center;
  }
  svg {
    width: 100%;
    height: auto;
    background: var(--bg-panel);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
  }
  .grid {
    stroke: var(--border);
  }
  .tick {
    font-family: var(--font-mono);
    font-size: 11px;
    fill: var(--text-faint);
  }
  .trace {
    fill: none;
    stroke: var(--accent);
    stroke-width: 2.5;
    stroke-linejoin: round;
  }
  .result {
    padding: var(--space-4);
    border: 2px solid var(--accent);
    border-radius: 6px;
    background: var(--bg-panel);
  }
  .big {
    margin: 0 0 var(--space-2);
    font-family: var(--font-mono);
    font-size: clamp(28px, 6vw, 40px);
    font-weight: 700;
    line-height: 1.1;
  }
  .result p {
    font-size: var(--fs-md);
  }
  .result p:last-child {
    margin-bottom: 0;
  }
  .muted {
    color: var(--text-faint);
  }
</style>
