<script lang="ts">
  // Step through soldering header pins onto the SuperMini, seen from the
  // side: breadboard jig, board on, tack two opposite corners, check it's
  // flat, then the rest. Drawn inline so each step can redraw; the steps
  // themselves live in soldering.ts.
  import { HEADER_PINS, HEADER_STEPS } from './soldering';

  let step = $state(0);
  const s = $derived(HEADER_STEPS[step]);
  const pinX = (i: number) => 60 + i * 34;
  const pins = Array.from({ length: HEADER_PINS }, (_, i) => i);
</script>

<div class="headers">
  <figure>
    <svg viewBox="0 0 360 200" role="img" aria-label="Side view: {s.title}">
      <!-- header pins: the long ends go down into the breadboard -->
      {#each pins as i (i)}
        <rect x={pinX(i) - 3} y={s.boardOn ? 82 : 84} width="6" height="70" fill="#D4A63A" stroke="#7A5A14" stroke-width="1" />
      {/each}
      <rect x="40" y="118" width="280" height="14" rx="2" fill="#1A1A1A" stroke="#6B736C" stroke-width="1" />
      <!-- breadboard -->
      <rect x="16" y="140" width="328" height="52" rx="4" fill="#EDEAE0" stroke="#9A9688" stroke-width="2" />
      {#each pins as i (i)}
        <rect x={pinX(i) - 5} y="140" width="10" height="5" fill="#55524A" />
      {/each}
      {#if s.boardOn}
        <rect x="34" y="104" width="292" height="12" rx="2" fill="#1C201C" stroke="#6B736C" stroke-width="2" />
        {#each pins as i (i)}
          <rect x={pinX(i) - 10} y="100" width="20" height="4" fill="#C8783A" />
          {#if s.soldered.includes(i)}
            <path
              d="M{pinX(i) - 11},101 C{pinX(i) - 5},99 {pinX(i) - 4},94 {pinX(i) - 3},87 L{pinX(i) + 3},87 C{pinX(i) + 4},94 {pinX(i) + 5},99 {pinX(i) + 11},101 Z"
              fill="#C9D0D5"
              stroke="#4A5258"
              stroke-width="1.5"
            />
          {/if}
        {/each}
      {/if}
      {#if step === 4}
        <line x1="20" y1="104" x2="340" y2="104" stroke="var(--accent)" stroke-width="2" stroke-dasharray="6 5" />
      {/if}
      {#if s.iron !== null}
        {@const x = pinX(s.iron)}
        <!-- mirrored for pins on the right half so the iron stays in frame -->
        <g class="iron" transform={s.iron >= HEADER_PINS / 2 ? `translate(${2 * x} 0) scale(-1 1)` : undefined}>
          <path d="M{x + 6},90 L{x + 40},40 L{x + 48},46 Z" fill="#9AA29C" stroke="#6B736C" stroke-width="1.5" />
          <rect x={x + 36} y="6" width="16" height="44" rx="4" transform="rotate(35 {x + 44} 40)" fill="#2B302B" stroke="#6B736C" stroke-width="1.5" />
          <path d="M{x - 30},70 C{x - 20},76 {x - 12},84 {x - 6},88" fill="none" stroke="#C9D0D5" stroke-width="4" stroke-linecap="round" />
        </g>
      {/if}
    </svg>
  </figure>

  <div class="step" aria-live="polite">
    <p class="count">Step {step + 1} of {HEADER_STEPS.length}</p>
    <h3>{s.title}</h3>
    <p>{s.body}</p>
    <div class="nav">
      <button type="button" aria-disabled={step === 0} onclick={() => step > 0 && (step -= 1)}>← Back</button>
      <button type="button" class="primary" aria-disabled={step === HEADER_STEPS.length - 1} onclick={() => step < HEADER_STEPS.length - 1 && (step += 1)}>Next →</button>
    </div>
  </div>
</div>

<style>
  .headers {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: var(--space-5);
    align-items: center;
  }
  figure {
    margin: 0;
  }
  svg {
    display: block;
    width: 100%;
    height: auto;
  }
  .count {
    margin: 0;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    color: var(--text-faint);
  }
  h3 {
    margin: var(--space-1) 0 var(--space-2);
  }
  .step p:not(.count) {
    margin: 0 0 var(--space-3);
    font-size: var(--fs-md);
  }
  .nav {
    display: flex;
    gap: var(--space-2);
  }
  .nav button {
    font: inherit;
    font-size: var(--fs-base);
    padding: 6px 14px;
    border-radius: var(--radius);
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  .nav button.primary {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .nav button[aria-disabled='true'] {
    opacity: 0.5;
    cursor: default;
  }
</style>
