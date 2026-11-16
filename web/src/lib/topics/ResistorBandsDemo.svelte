<script lang="ts">
  // Tap a band to change its color, or type a value and see the bands.
  // 4-band (two digits) or 5-band (three digits). Logic in resistorsLeds.ts.
  import {
    BAND_FILL,
    DIGIT_COLORS,
    MULTIPLIER_EXP,
    TOLERANCE,
    bandsFor,
    formatOhms,
    isEValue,
    nearestE,
    nextBand,
    parseOhms,
    readBands,
    type BandColor,
    type BandCount,
  } from './resistorsLeds';

  let count = $state<BandCount>(4);
  let bands = $state<BandColor[]>(['red', 'red', 'brown', 'gold']);
  let typed = $state('');
  let typedNote = $state('');
  const clipId = $props.id();

  const reading = $derived(readBands(bands));
  const sig = $derived(count - 2);
  const xs = $derived(count === 4 ? [92, 124, 156, 224] : [72, 104, 136, 168, 226]);
  const e12 = $derived(isEValue(reading.ohms, 'E12'));
  const e24 = $derived(isEValue(reading.ohms, 'E24'));

  function roleOf(i: number): string {
    if (i < sig) return `digit ${i + 1}`;
    if (i === sig) return 'multiplier';
    return 'tolerance';
  }
  function meaning(i: number, c: BandColor): string {
    if (i < sig) return String(DIGIT_COLORS.indexOf(c));
    if (i === sig) return `×${formatOhms(10 ** MULTIPLIER_EXP[c]!).replace('Ω', '').replace(' ', '')}`;
    return `± ${TOLERANCE[c]}%`;
  }
  function cycle(i: number, step = 1) {
    bands[i] = nextBand(count, i, bands[i], step);
    typed = '';
    typedNote = '';
  }
  function onkey(i: number, e: KeyboardEvent) {
    if (e.key === 'Enter' || e.key === ' ' || e.key === 'ArrowUp') {
      e.preventDefault();
      cycle(i, 1);
    } else if (e.key === 'ArrowDown') {
      e.preventDefault();
      cycle(i, -1);
    }
  }
  function setCount(n: BandCount) {
    if (n === count) return;
    const b = bandsFor(reading.ohms, n);
    count = n;
    bands = b ? b.bands : n === 4 ? ['red', 'red', 'brown', 'gold'] : ['red', 'red', 'black', 'black', 'brown'];
    typedNote = '';
  }
  function onType() {
    const v = parseOhms(typed);
    if (v === null) {
      typedNote = typed.trim() ? 'Type a value like 220, 4.7k or 4k7.' : '';
      return;
    }
    const b = bandsFor(v, count, bands[count - 1]);
    if (!b) {
      typedNote = `${formatOhms(v)} is out of range for ${count} bands.`;
      return;
    }
    bands = b.bands;
    typedNote = b.exact ? '' : `Rounded to ${formatOhms(b.ohms)}: ${count} bands only hold ${sig} digits.`;
  }
</script>

<div class="rb">
  <div class="group" role="radiogroup" aria-label="Number of bands">
    {#each [4, 5] as n (n)}
      <button type="button" role="radio" aria-checked={count === n} class:on={count === n} onclick={() => setCount(n as BandCount)}>
        {n} bands
      </button>
    {/each}
  </div>

  <figure>
    <svg viewBox="0 0 320 120" role="group" aria-label="Resistor, {formatOhms(reading.ohms)} ± {reading.tolerance}%">
      <line x1="4" y1="50" x2="316" y2="50" class="lead" />
      <defs>
        <clipPath id="{clipId}-body"><rect x="60" y="22" width="200" height="56" rx="24" /></clipPath>
      </defs>
      <rect x="60" y="22" width="200" height="56" rx="24" class="body" class:film={count === 5} />
      {#each bands as c, i (i)}
        <rect
          x={xs[i]}
          y="22"
          width="18"
          height="56"
          class="band"
          fill={BAND_FILL[c]}
          clip-path="url(#{clipId}-body)"
          role="button"
          tabindex="0"
          aria-label="Band {i + 1}, {roleOf(i)}: {c}, {meaning(i, c)}. Tap to change."
          onclick={() => cycle(i)}
          onkeydown={(e) => onkey(i, e)}
        />
        <text x={xs[i] + 9} y="96" class="t" text-anchor="middle">{meaning(i, c)}</text>
        <text x={xs[i] + 9} y="110" class="t muted" text-anchor="middle">{c}</text>
      {/each}
    </svg>
  </figure>

  <p class="value" aria-live="polite">
    <strong>{formatOhms(reading.ohms)}</strong> ± {reading.tolerance}%: really anywhere from {formatOhms(reading.min)} to {formatOhms(reading.max)}.
  </p>
  <p class="eseries">
    {#if e12}A standard E12 value, the kind every kit has.
    {:else if e24}A standard E24 value (not in E12; nearest E12 is {formatOhms(nearestE(reading.ohms, 'E12'))}).
    {:else}Not a standard value. Nearest E12: {formatOhms(nearestE(reading.ohms, 'E12'))}; nearest E24: {formatOhms(nearestE(reading.ohms, 'E24'))}.{/if}
  </p>

  <label class="type">
    <span>Or type a value</span>
    <input type="text" inputmode="decimal" placeholder="4.7k" bind:value={typed} oninput={onType} autocomplete="off" />
  </label>
  <p class="note" aria-live="polite">{typedNote}</p>

  <ul class="key" aria-label="Digit colors">
    {#each DIGIT_COLORS as c, d (c)}
      <li><span class="chip" style:background={BAND_FILL[c]}></span>{d} {c}</li>
    {/each}
  </ul>
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
  figure {
    margin: 0;
  }
  svg {
    display: block;
    width: 100%;
    max-width: 560px;
    height: auto;
  }
  .lead {
    stroke: #9aa0a6;
    stroke-width: 4;
    stroke-linecap: round;
  }
  .body {
    fill: #dcc49c;
    stroke: #a88b5c;
    stroke-width: 1.5;
  }
  .body.film {
    fill: #8fb3d9;
    stroke: #5d84ad;
  }
  .band {
    cursor: pointer;
    stroke: rgba(0, 0, 0, 0.25);
    stroke-width: 1;
  }
  .band:focus {
    outline: none;
  }
  /* The band is clipped to the body, so half the stroke shows: 5 draws a 2.5px ring. */
  .band:focus-visible {
    stroke: var(--accent);
    stroke-width: 5;
  }
  .t {
    font-family: var(--font-mono);
    font-size: 8px;
    fill: var(--text);
  }
  .t.muted {
    fill: var(--text-muted);
  }
  .value {
    margin: var(--space-2) 0 var(--space-1);
    font-size: var(--fs-md);
  }
  .eseries,
  .note {
    margin: 0 0 var(--space-2);
    font-size: var(--fs-base);
    color: var(--text-muted);
    min-height: 1.2em;
  }
  .type {
    display: grid;
    gap: var(--space-1);
    max-width: 16rem;
    font-size: var(--fs-base);
    font-weight: 400;
    color: var(--text);
  }
  .type input {
    font: inherit;
    padding: 4px 8px;
  }
  .key {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1) var(--space-3);
    list-style: none;
    padding: 0;
    margin: var(--space-3) 0 0;
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  .key li {
    display: flex;
    align-items: center;
    gap: 4px;
  }
  .chip {
    width: 12px;
    height: 12px;
    border-radius: 2px;
    border: 1px solid var(--border-strong);
  }
</style>
