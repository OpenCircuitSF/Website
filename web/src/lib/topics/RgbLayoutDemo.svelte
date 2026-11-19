<script lang="ts">
  // HUB75 panels on a Pi 4: size, chain and parallel chains. Fully static: nothing here moves or blinks (WCAG 2.3.1).
  // Logic in piProjects2.ts (#0726).
  import { ADAPTERS, MAX_CHAIN, PANEL_SIZES, matrixLayout, panelSize, type AdapterId, type PanelSizeId } from './piProjects2';

  const FLICKER_LINE = {
    visible: 'Under 100 Hz the eye can see flicker. Split the panels into parallel chains or use fewer PWM bits.',
    camera: 'Steady to the eye; a phone camera may catch bands.',
    steady: 'Comfortably fast.',
  } as const;

  let size = $state<PanelSizeId>('64x32');
  let adapter = $state<AdapterId>('single');
  let parallel = $state(1);
  let chain = $state(1);

  const s = $derived(panelSize(size));
  const l = $derived(matrixLayout(size, chain, parallel, adapter));
  const vbW = $derived(90 + chain * (s.cols + 16));
  const vbH = $derived(24 + parallel * (s.rows + 24));
  const px = (c: number) => 90 + c * (s.cols + 16);
  const py = (p: number) => 24 + p * (s.rows + 24);
</script>

<!-- #0727: illustration slot -->
<div class="rl">
  <div class="group" role="radiogroup" aria-label="Panel size (columns × rows)">
    {#each PANEL_SIZES as p (p.id)}
      <button type="button" role="radio" aria-checked={size === p.id} class:on={size === p.id} onclick={() => (size = p.id)}>{p.label}</button>
    {/each}
  </div>
  <div class="group" role="radiogroup" aria-label="Adapter">
    {#each ADAPTERS as a (a.id)}
      <button type="button" role="radio" aria-checked={adapter === a.id} class:on={adapter === a.id} onclick={() => (adapter = a.id)}>{a.label}</button>
    {/each}
  </div>
  <div class="group" role="radiogroup" aria-label="Parallel chains (--led-parallel)">
    {#each [1, 2, 3] as n (n)}
      <button type="button" role="radio" aria-checked={parallel === n} class:on={parallel === n} onclick={() => (parallel = n)}>{n}</button>
    {/each}
  </div>
  <label class="range">
    Panels in each chain (--led-chain)
    <input type="range" min="1" max={MAX_CHAIN} step="1" bind:value={chain} />
    <output>{chain}</output>
  </label>

  <figure>
    <svg
      viewBox="0 0 {vbW} {vbH}"
      width="100%"
      role="img"
      aria-label="{parallel} chain{parallel > 1 ? 's' : ''} of {chain} {s.label} panel{chain > 1 ? 's' : ''}: {l.width}×{l.height} pixels. {l.title}."
    >
      <rect class="pi" x="4" y="24" width="64" height={vbH - 48 > 16 ? vbH - 48 : 16} rx="4" />
      <text class="lbl-s" x="36" y="16" text-anchor="middle">Pi 4 + adapter</text>
      {#each Array.from({ length: parallel }, (_, p) => p) as p (p)}
        {@const bad = l.problem !== null && p > 0}
        {@const cy = py(p) + s.rows / 2}
        <line class="wire" class:bad x1="68" y1={cy} x2={px(0) - 3} y2={cy} />
        {#each Array.from({ length: chain }, (_, c) => c) as c (c)}
          {#if c > 0}
            <line class="wire" class:bad x1={px(c) - 16} y1={cy} x2={px(c) - 3} y2={cy} />
            {#if c === 1 && p === 0}
              <text class="lbl-s" x={px(c) - 8} y={py(p) - 6} text-anchor="middle">OUT → IN</text>
            {/if}
          {/if}
          <rect class="panel" class:bad x={px(c)} y={py(p)} width={s.cols} height={s.rows} />
          <text class="num" x={px(c) + s.cols / 2} y={cy + 4} text-anchor="middle">{p * chain + c + 1}</text>
        {/each}
      {/each}
    </svg>
  </figure>

  <p>{l.width}×{l.height} pixels ({l.pixels.toLocaleString('en-US')} LEDs) · 1:{s.rows / 2} scan</p>

  <div class="status {l.tone}" aria-live="polite">
    <h3>{l.title}</h3>
    {#if l.problem}<p>{l.problem}</p>{/if}
    <p>{FLICKER_LINE[l.flicker]}</p>
  </div>

  <pre class="code"><code>{l.command}</code></pre>
  <p class="lbl">Add the --led-gpio-mapping your adapter needs.</p>
  <p class="lbl">Illustrative numbers for a Pi 4 at --led-slowdown-gpio=2 and 11 PWM bits; measure yours with --led-show-refresh.</p>

  <p class="see-also">
    See also: <a href="/guides/pi-rgb-matrix-flicker">Fixing flicker and garbage</a>,
    <a href="/guides/pi-rgb-matrix-power">How much power the panels need</a>.
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
  .range {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    font-size: var(--fs-base);
    color: var(--text);
    margin-bottom: var(--space-3);
  }
  figure {
    margin: 0;
  }
  svg {
    display: block;
    height: auto;
  }
  .pi {
    fill: var(--bg-panel);
    stroke: var(--text-faint);
  }
  .panel {
    fill: var(--bg-panel);
    stroke: var(--accent);
    stroke-width: 1.5;
  }
  .wire {
    stroke: var(--text-faint);
    stroke-width: 1.5;
  }
  .panel.bad,
  .wire.bad {
    stroke: var(--danger);
    stroke-dasharray: 3 3;
  }
  .lbl-s {
    font-size: 8px;
    fill: var(--text-faint);
  }
  .num {
    font-size: 11px;
    fill: var(--text);
  }
  .status h3 {
    margin: var(--space-3) 0 var(--space-1);
    font-size: var(--fs-lg);
  }
  .status p {
    margin: 0 0 var(--space-1);
    font-size: var(--fs-md);
  }
  .status.good h3 {
    color: var(--accent);
  }
  .status.meh h3 {
    color: var(--warning);
  }
  .status.bad h3 {
    color: var(--danger);
  }
  .lbl {
    color: var(--text-muted);
    font-size: var(--fs-sm);
  }
  .code {
    margin: var(--space-2) 0;
    padding: var(--space-3);
    border-radius: 6px;
    background: var(--bg-panel);
    border: var(--border-w) solid var(--border);
    font-size: var(--fs-sm);
    overflow-x: auto;
  }
  .see-also {
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  .see-also a {
    color: var(--accent);
  }
</style>
