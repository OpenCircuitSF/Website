<script lang="ts">
  // Capacitor types guide (#0691): type a marking and see the value in pF, nF
  // and µF on a drawn part; then put a polarised capacitor across a rail and
  // flip it until it is the right way round. Logic in capacitors.ts.
  import {
    MARK_MEANS,
    capUnits,
    decodeCapCode,
    polarityOk,
    ratingVerdict,
    toEiaCode,
    type CapKind,
    type Orientation,
  } from './capacitors';

  const PRESETS = ['104', '472', '22', '4R7', '105K', '0.1µF', '10µF 16V'];
  let code = $state('104');
  const result = $derived(decodeCapCode(code));
  const units = $derived(result.ok ? capUnits(result.value.pf) : null);
  const printed = $derived(result.ok ? toEiaCode(result.value.pf) : null);
  // Small values come as ceramic discs; 1 µF and up as electrolytic cans. A
  // three-digit code (105K is 1 µF) is printed on ceramics and film, never on a
  // can, so a code always draws a disc.
  const isCode = $derived(/^\d{3}[A-Za-z]?$/.test(code.trim()));
  const asCan = $derived(result.ok && result.value.pf >= 1e6 && !isCode);
  const label = $derived(code.trim().slice(0, 12));
  // Printed on the part: one line per word (value, then voltage), shrunk if long.
  const lines = $derived(label.split(/\s+/).filter(Boolean).slice(0, 2));
  // Monospace glyphs are about 0.6 em wide; fit the longest line in the body.
  const fontPx = $derived(Math.min(15, (asCan ? 44 : 76) / (0.62 * Math.max(...lines.map((l) => l.length), 1))));

  const KINDS: { id: CapKind; name: string }[] = [
    { id: 'electrolytic', name: 'Electrolytic' },
    { id: 'tantalum', name: 'Tantalum' },
    { id: 'ceramic', name: 'Ceramic' },
  ];
  const RAILS = [3.3, 5];
  const RATINGS = [4, 6.3, 10, 16, 25];
  let kind = $state<CapKind>('electrolytic');
  let orient = $state<Orientation>('mark-to-plus');
  let rail = $state(5);
  let rating = $state(6.3);

  const polOk = $derived(polarityOk(kind, orient));
  const verdict = $derived(ratingVerdict(rating, rail));
  // The marked lead is drawn on the left (to +) or the right (to GND).
  const markLeft = $derived(orient === 'mark-to-plus');
  const mark = $derived(MARK_MEANS[kind]);

  const polText = $derived(
    mark === 'none'
      ? 'Ceramic capacitors have no polarity. Either way round is fine.'
      : polOk
        ? mark === 'negative'
          ? 'Right way round: the striped (−) lead goes to GND, the longer lead to +.'
          : 'Right way round: on a tantalum the band marks the + lead.'
        : mark === 'negative'
          ? 'Backwards! The stripe marks the negative lead, and it is on the + rail. A reversed electrolytic can heat up, bulge or burst.'
          : 'Backwards! On a tantalum the band marks the positive lead, the opposite of an electrolytic. Reversed, it can short and burn.',
  );
  const ratingText = $derived(
    verdict === 'over'
      ? `A ${rating} V part on a ${rail} V rail is over its rating. Pick a higher voltage.`
      : verdict === 'tight'
        ? `${rating} V on a ${rail} V rail works, but with little margin. One step up is safer.`
        : `${rating} V on a ${rail} V rail leaves a good margin.`,
  );
</script>

<div class="cap">
  <section aria-labelledby="cap-read">
    <h3 id="cap-read">Read the marking</h3>
    <label class="field">
      <span>Marking on the capacitor</span>
      <input type="text" bind:value={code} autocomplete="off" spellcheck="false" />
    </label>
    <div class="chips" role="group" aria-label="Try a marking">
      {#each PRESETS as p (p)}
        <button type="button" class:on={code === p} aria-pressed={code === p} onclick={() => (code = p)}>{p}</button>
      {/each}
    </div>

    <div class="layout">
      <svg viewBox="0 0 120 130" role="img" aria-label={asCan ? `Electrolytic capacitor marked ${label}` : `Ceramic capacitor marked ${label}`}>
        {#if asCan}
          <line class="lead" x1="48" y1="100" x2="48" y2="128" />
          <line class="lead" x1="72" y1="100" x2="72" y2="120" />
          <rect class="can" x="28" y="10" width="64" height="92" rx="8" />
          <rect class="stripe" x="74" y="10" width="18" height="92" rx="4" />
          <text class="minus" x="83" y="40" text-anchor="middle">−</text>
          <text class="minus" x="83" y="70" text-anchor="middle">−</text>
          {#each lines as l, i (i)}
            <text class="mark-text" x="51" y={60 + (i - (lines.length - 1) / 2) * (fontPx + 3)} style:font-size="{fontPx.toFixed(1)}px" text-anchor="middle">{l}</text>
          {/each}
        {:else}
          <line class="lead" x1="48" y1="80" x2="48" y2="128" />
          <line class="lead" x1="72" y1="80" x2="72" y2="128" />
          <ellipse class="disc" cx="60" cy="52" rx="42" ry="40" />
          {#each lines as l, i (i)}
            <text class="mark-text" x="60" y={57 + (i - (lines.length - 1) / 2) * (fontPx + 3)} style:font-size="{fontPx.toFixed(1)}px" text-anchor="middle">{l}</text>
          {/each}
        {/if}
      </svg>

      <div class="out" aria-live="polite">
        {#if result.ok && units}
          <p class="big">{units.best}</p>
          <ul class="units">
            <li>{units.pF}</li>
            <li>{units.nF}</li>
            <li>{units.uF}</li>
          </ul>
          <p class="how">{result.value.how}.</p>
          {#if result.value.tolerance}<p>Tolerance: <strong>{result.value.tolerance}</strong></p>{/if}
          {#if result.value.volts !== undefined}<p>Rated for <strong>{result.value.volts} V</strong>: keep the rail below that.</p>{/if}
          {#if printed && printed !== code.trim().slice(0, 3)}<p>As a three-digit code: <code>{printed}</code></p>{/if}
        {:else if !result.ok}
          <p class="err">{result.error}</p>
        {/if}
      </div>
    </div>
  </section>

  <section aria-labelledby="cap-pol">
    <h3 id="cap-pol">Which way round?</h3>
    <div class="group" role="radiogroup" aria-label="Capacitor type">
      {#each KINDS as k (k.id)}
        <button type="button" role="radio" aria-checked={kind === k.id} class:on={kind === k.id} onclick={() => (kind = k.id)}>{k.name}</button>
      {/each}
    </div>

    <svg class="rail" viewBox="0 0 320 150" role="img" aria-label={mark === 'none' ? `ceramic capacitor between the ${rail} volt rail and GND, no polarity` : `${kind} capacitor between the ${rail} volt rail and GND, marked lead on the ${markLeft ? 'plus' : 'ground'} side`}>
      <line class="wire plus" x1="10" y1="20" x2="310" y2="20" />
      <text class="t" x="12" y="14">+{rail} V</text>
      <line class="wire" x1="10" y1="135" x2="310" y2="135" />
      <text class="t" x="12" y="149">GND</text>
      <!-- Left lead up to +, right lead down to GND. -->
      <line class="lead" x1="148" y1="72" x2="148" y2="20" />
      <line class="lead" x1="172" y1="72" x2="172" y2="135" />
      <circle class="dot" cx="148" cy="20" r="3" />
      <circle class="dot" cx="172" cy="135" r="3" />
      {#if kind === 'electrolytic'}
        <rect class="can" x="130" y="40" width="60" height="60" rx="6" />
        <rect class="stripe" x={markLeft ? 130 : 172} y="40" width="18" height="60" rx="3" />
        <text class="minus" x={markLeft ? 139 : 181} y="66" text-anchor="middle">−</text>
        <text class="minus" x={markLeft ? 139 : 181} y="88" text-anchor="middle">−</text>
      {:else if kind === 'tantalum'}
        <ellipse class="tant" cx="160" cy="72" rx="30" ry="34" />
        <text class="plus-mark" x={markLeft ? 144 : 176} y="78" text-anchor="middle">+</text>
      {:else}
        <ellipse class="disc" cx="160" cy="72" rx="30" ry="30" />
        <text class="mark-text small" x="160" y="77" text-anchor="middle">104</text>
      {/if}
      <text class="t" x="200" y="62">{polOk ? '✓' : '✗'} {mark === 'none' ? 'no polarity' : polOk ? 'correct' : 'reversed'}</text>
    </svg>

    <button type="button" class="flip" onclick={() => (orient = orient === 'mark-to-plus' ? 'mark-to-ground' : 'mark-to-plus')}>
      Flip the capacitor
    </button>

    <div class="row">
      <div class="group" role="radiogroup" aria-label="Rail voltage">
        {#each RAILS as r (r)}
          <button type="button" role="radio" aria-checked={rail === r} class:on={rail === r} onclick={() => (rail = r)}>{r} V rail</button>
        {/each}
      </div>
      <div class="group" role="radiogroup" aria-label="Capacitor voltage rating">
        {#each RATINGS as v (v)}
          <button type="button" role="radio" aria-checked={rating === v} class:on={rating === v} onclick={() => (rating = v)}>{v} V</button>
        {/each}
      </div>
    </div>

    <div class="verdict" class:bad={!polOk || verdict === 'over'} aria-live="polite">
      <p><strong>{polOk ? 'Polarity OK' : 'Reversed'}</strong>: {polText}</p>
      <p><strong>{verdict === 'over' ? 'Over its rating' : verdict === 'tight' ? 'Tight' : 'Rating OK'}</strong>: {ratingText}</p>
    </div>
  </section>
</div>

<style>
  .cap {
    display: grid;
    gap: var(--space-5);
  }
  h3 {
    margin: 0 0 var(--space-2);
    font-size: var(--fs-md);
  }
  .field {
    display: grid;
    gap: var(--space-1);
    max-width: 18rem;
    font-size: var(--fs-base);
    color: var(--text);
  }
  .field input {
    font: inherit;
    font-family: var(--font-mono);
    padding: 6px 10px;
    border-radius: 6px;
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
  }
  .field input:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
  .chips,
  .group {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin: var(--space-2) 0;
  }
  .chips button,
  .group button,
  .flip {
    font: inherit;
    font-size: var(--fs-base);
    padding: 4px 12px;
    border-radius: 999px;
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  .chips button {
    font-family: var(--font-mono);
  }
  .chips button.on,
  .group button.on {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .layout {
    display: grid;
    grid-template-columns: 120px 1fr;
    gap: var(--space-4);
    align-items: start;
    margin-top: var(--space-3);
  }
  svg {
    display: block;
    width: 100%;
    height: auto;
  }
  .rail {
    max-width: 420px;
    margin: var(--space-2) 0;
  }
  .lead,
  .wire {
    fill: none;
    stroke: var(--text-muted);
    stroke-width: 3;
    stroke-linecap: round;
  }
  .wire.plus {
    stroke: #d9534f;
  }
  .dot {
    fill: var(--text);
  }
  .disc {
    fill: #d98a2b;
    stroke: #8a5214;
    stroke-width: 2;
  }
  .can {
    fill: #2f5f9e;
    stroke: #1d3b63;
    stroke-width: 2;
  }
  .stripe {
    fill: #c9d6e8;
  }
  .tant {
    fill: #e2b33c;
    stroke: #8f6c12;
    stroke-width: 2;
  }
  .minus,
  .plus-mark {
    font-family: var(--font-mono);
    font-size: 18px;
    font-weight: 700;
    fill: #1d3b63;
  }
  .plus-mark {
    fill: #4a3500;
    font-size: 22px;
  }
  .mark-text {
    font-family: var(--font-mono);
    font-size: 15px;
    font-weight: 700;
    fill: #fff;
  }
  .mark-text.small {
    font-size: 13px;
    fill: #2b1700;
  }
  .t {
    font-family: var(--font-mono);
    font-size: 12px;
    fill: var(--text-muted);
  }
  .out p {
    margin: 0 0 var(--space-1);
  }
  .big {
    font-size: var(--fs-xl);
    font-weight: 700;
    color: var(--text);
  }
  .units {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin: 0 0 var(--space-2);
    padding: 0;
    list-style: none;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
  }
  .units li {
    padding: 2px 8px;
    border: var(--border-w) solid var(--border);
    border-radius: 4px;
  }
  .how {
    color: var(--text-muted);
  }
  .err {
    color: var(--text);
  }
  .row {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-4);
  }
  .verdict {
    margin-top: var(--space-2);
    padding: var(--space-3);
    border-radius: 6px;
    border: var(--border-w) solid var(--accent);
  }
  .verdict.bad {
    border-color: var(--danger);
  }
  .verdict p {
    margin: 0 0 var(--space-1);
  }
  @media (max-width: 480px) {
    .layout {
      grid-template-columns: 1fr;
    }
    .layout svg {
      max-width: 120px;
    }
  }
</style>
