<script lang="ts">
  // A source and one or two resistors, in series or in parallel. Drag the
  // sliders and watch the current and each resistor's power. Logic in
  // resistorsLeds.ts.
  import { SLIDER_OHMS, formatAmps, formatOhms, formatVolts, formatWatts, solve, type Hookup } from './resistorsLeds';

  const HOOKUPS: { id: Hookup; label: string }[] = [
    { id: 'one', label: 'One resistor' },
    { id: 'series', label: 'Series' },
    { id: 'parallel', label: 'Parallel' },
  ];
  let hookup = $state<Hookup>('one');
  let volts = $state(5);
  let i1 = $state(SLIDER_OHMS.indexOf(220));
  let i2 = $state(SLIDER_OHMS.indexOf(1000));

  const r1 = $derived(SLIDER_OHMS[i1]);
  const r2 = $derived(SLIDER_OHMS[i2]);
  const c = $derived(solve(volts, r1, r2, hookup));
  const hot = $derived(c.parts.filter((p) => p.overRated));

  /** A zigzag from (x1,y1) to (x2,y2), horizontal or vertical. */
  function zigzag(x1: number, y1: number, x2: number, y2: number): string {
    const n = 6;
    const vertical = x1 === x2;
    const len = vertical ? y2 - y1 : x2 - x1;
    const lead = len * 0.15;
    const step = (len - 2 * lead) / n;
    let d = `M${x1},${y1}`;
    const at = (t: number, off: number) => (vertical ? `${x1 + off},${y1 + t}` : `${x1 + t},${y1 + off}`);
    d += ` L${at(lead, 0)}`;
    for (let k = 0; k < n; k++) d += ` L${at(lead + step * (k + 0.5), k % 2 ? 8 : -8)}`;
    d += ` L${at(len - lead, 0)} L${at(len, 0)}`;
    return d;
  }
</script>

<div class="ohm">
  <div class="group" role="radiogroup" aria-label="How the resistors connect">
    {#each HOOKUPS as h (h.id)}
      <button type="button" role="radio" aria-checked={hookup === h.id} class:on={hookup === h.id} onclick={() => (hookup = h.id)}>{h.label}</button>
    {/each}
  </div>

  <div class="sliders">
    <label>
      <span>Voltage: <strong>{formatVolts(volts)}</strong></span>
      <input type="range" min="0.5" max="12" step="0.1" bind:value={volts} />
    </label>
    <label>
      <span>R1: <strong>{formatOhms(r1)}</strong></span>
      <input type="range" min="0" max={SLIDER_OHMS.length - 1} step="1" bind:value={i1} aria-valuetext={formatOhms(r1)} />
    </label>
    {#if hookup !== 'one'}
      <label>
        <span>R2: <strong>{formatOhms(r2)}</strong></span>
        <input type="range" min="0" max={SLIDER_OHMS.length - 1} step="1" bind:value={i2} aria-valuetext={formatOhms(r2)} />
      </label>
    {/if}
  </div>

  <figure>
    <svg viewBox="0 0 340 165" role="img" aria-label="{formatVolts(volts)} source, {hookup === 'one' ? 'one resistor' : `two resistors in ${hookup}`}, {formatAmps(c.totalAmps)} flowing">
      <!-- the source -->
      <line x1="40" y1="40" x2="40" y2="84" class="wire" />
      <line x1="24" y1="84" x2="56" y2="84" class="plate" />
      <line x1="32" y1="96" x2="48" y2="96" class="plate thick" />
      <line x1="40" y1="96" x2="40" y2="150" class="wire" />
      <text x="62" y="88" class="t">+</text>
      <text x="6" y="128" class="t">{formatVolts(volts)}</text>
      <!-- the current arrow -->
      <path d="M78,34 L90,40 L78,46 Z" class="arrow" />
      <text x="70" y="26" class="t">I = {formatAmps(c.totalAmps)}</text>

      {#if hookup === 'one'}
        <path d="M40,40 L280,40 L280,60" class="wire" />
        <path d={zigzag(280, 60, 280, 130)} class="res" class:hot={c.parts[0].overRated} />
        <path d="M280,130 L280,150 L40,150" class="wire" />
        <text x="196" y="90" class="t">R1 {formatOhms(r1)}</text>
        <text x="196" y="104" class="t muted">{formatWatts(c.parts[0].watts)}</text>
      {:else if hookup === 'series'}
        <path d="M40,40 L130,40" class="wire" />
        <path d={zigzag(130, 40, 210, 40)} class="res" class:hot={c.parts[0].overRated} />
        <path d="M210,40 L280,40 L280,60" class="wire" />
        <path d={zigzag(280, 60, 280, 130)} class="res" class:hot={c.parts[1].overRated} />
        <path d="M280,130 L280,150 L40,150" class="wire" />
        <text x="136" y="66" class="t">R1 {formatOhms(r1)}</text>
        <text x="136" y="80" class="t muted">{formatVolts(c.parts[0].volts)} · {formatWatts(c.parts[0].watts)}</text>
        <text x="196" y="112" class="t" text-anchor="end">R2 {formatOhms(r2)}</text>
        <text x="196" y="126" class="t muted" text-anchor="end">{formatVolts(c.parts[1].volts)} · {formatWatts(c.parts[1].watts)}</text>
      {:else}
        <path d="M40,40 L310,40 L310,60" class="wire" />
        <path d="M170,40 L170,60" class="wire" />
        <path d={zigzag(170, 60, 170, 130)} class="res" class:hot={c.parts[0].overRated} />
        <path d={zigzag(310, 60, 310, 130)} class="res" class:hot={c.parts[1].overRated} />
        <path d="M170,130 L170,150 M310,130 L310,150 L40,150" class="wire" />
        <circle cx="170" cy="40" r="3" class="dot" />
        <circle cx="170" cy="150" r="3" class="dot" />
        <text x="162" y="116" class="t" text-anchor="end">R1 {formatOhms(r1)}</text>
        <text x="162" y="130" class="t muted" text-anchor="end">{formatAmps(c.parts[0].amps)} · {formatWatts(c.parts[0].watts)}</text>
        <text x="300" y="90" class="t" text-anchor="end">R2 {formatOhms(r2)}</text>
        <text x="300" y="104" class="t muted" text-anchor="end">{formatAmps(c.parts[1].amps)} · {formatWatts(c.parts[1].watts)}</text>
      {/if}
    </svg>
  </figure>

  <table>
    <thead>
      <tr><th scope="col"></th><th scope="col">Resistance</th><th scope="col">Voltage</th><th scope="col">Current</th><th scope="col">Power</th></tr>
    </thead>
    <tbody>
      {#each c.parts as p, i (i)}
        <tr class:warn={p.overRated}>
          <th scope="row">R{i + 1}</th>
          <td>{formatOhms(p.ohms)}</td>
          <td>{formatVolts(p.volts)}</td>
          <td>{formatAmps(p.amps)}</td>
          <td>{formatWatts(p.watts)}</td>
        </tr>
      {/each}
      {#if c.parts.length > 1}
        <tr>
          <th scope="row">Total</th>
          <td>{formatOhms(c.totalOhms)}</td>
          <td>{formatVolts(volts)}</td>
          <td>{formatAmps(c.totalAmps)}</td>
          <td>{formatWatts(c.parts.reduce((s, p) => s + p.watts, 0))}</td>
        </tr>
      {/if}
    </tbody>
  </table>

  <p class="status" class:bad={hot.length > 0} aria-live="polite">
    {#if hot.length > 0}
      Too hot: {c.parts
        .map((p, i) => (p.overRated ? `R${i + 1} is turning ${formatWatts(p.watts)} into heat` : ''))
        .filter(Boolean)
        .join(', and ')}. A 1/4 W resistor can’t take that. Use a bigger resistance or a higher-wattage part.
    {:else}
      Every resistor is under 1/4 W (250 mW), so ordinary through-hole parts are fine.
    {/if}
  </p>
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
  .sliders {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
    gap: var(--space-2) var(--space-4);
    margin-bottom: var(--space-3);
  }
  .sliders label {
    display: grid;
    gap: var(--space-1);
    font-size: var(--fs-base);
    font-weight: 400;
    color: var(--text);
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
  .plate,
  .res {
    fill: none;
    stroke: var(--text);
    stroke-width: 2;
    stroke-linejoin: round;
  }
  .plate.thick {
    stroke-width: 4;
  }
  .res {
    stroke: var(--accent);
    stroke-width: 2.5;
  }
  .res.hot {
    stroke: var(--danger);
    stroke-width: 3.5;
  }
  .arrow,
  .dot {
    fill: var(--text);
  }
  .t {
    font-family: var(--font-mono);
    font-size: 11px;
    fill: var(--text);
  }
  .t.muted {
    fill: var(--text-muted);
  }
  table {
    width: 100%;
    border-collapse: collapse;
    margin-top: var(--space-3);
    font-size: var(--fs-sm);
    font-variant-numeric: tabular-nums;
  }
  th,
  td {
    text-align: right;
    padding: 4px 6px;
    border-bottom: var(--border-w) solid var(--border);
  }
  th[scope='row'],
  thead th:first-child {
    text-align: left;
  }
  tr.warn td:last-child {
    color: var(--danger);
    font-weight: 600;
  }
  .status {
    margin: var(--space-3) 0 0;
    font-size: var(--fs-md);
  }
  .status.bad {
    color: var(--danger);
  }
</style>
