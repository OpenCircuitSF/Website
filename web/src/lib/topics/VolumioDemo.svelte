<script lang="ts">
  // Pick how people will play, the board and the audio output; see which
  // Volumio feature handles it, whether it's free, and the signal chain.
  // Logic in piProjects.ts. Static drawing, nothing moves.
  import { AUDIO_OUTS, PLAY_SOURCES, VOLUMIO_PLANS_CHECKED, sourceById, volumioChain, type AudioOut, type PiBoard, type PlaySource } from './piProjects';

  const BOARDS: { id: PiBoard; label: string }[] = [
    { id: 'zero2w', label: 'Pi Zero 2W' },
    { id: 'pi4', label: 'Pi 4' },
  ];

  let source = $state<PlaySource>('spotify');
  let board = $state<PiBoard>('zero2w');
  let out = $state<AudioOut>('dac-hat');

  const chain = $derived(volumioChain(source, board, out));
  const src = $derived(sourceById(source));
  const heading = $derived(
    !chain.ok
      ? 'No headphone jack on a Zero 2W'
      : chain.tier === 'free'
        ? `${chain.feature}: Free in Volumio OS`
        : `${chain.feature}: Needs Volumio Premium`,
  );

  const BOX_W = 96;
  const GAP = 20;
  const BOX_Y = 46;
  const BOX_H = 44;
  const boxX = (i: number) => 4 + i * (BOX_W + GAP);
</script>

<div class="vd">
  <div class="group" role="radiogroup" aria-label="How people will play">
    {#each PLAY_SOURCES as s (s.id)}
      <button type="button" role="radio" aria-checked={source === s.id} class:on={source === s.id} onclick={() => (source = s.id)}>{s.label}</button>
    {/each}
  </div>
  <div class="group" role="radiogroup" aria-label="Board">
    {#each BOARDS as b (b.id)}
      <button type="button" role="radio" aria-checked={board === b.id} class:on={board === b.id} onclick={() => (board = b.id)}>{b.label}</button>
    {/each}
  </div>
  <div class="group" role="radiogroup" aria-label="Audio out">
    {#each AUDIO_OUTS as o (o.id)}
      <button type="button" role="radio" aria-checked={out === o.id} class:on={out === o.id} onclick={() => (out = o.id)}>{o.label}</button>
    {/each}
  </div>

  <figure>
    <svg
      viewBox="0 0 584 140"
      role="img"
      aria-label="Signal chain: {chain.nodes.map((n) => n.label).join(', then ')}. {chain.ok ? 'Working' : chain.problem}"
    >
      {#each chain.nodes as n, i (n.id)}
        {#if i > 0}
          {@const ok = n.ok}
          <line x1={boxX(i) - GAP + 2} y1={BOX_Y + BOX_H / 2} x2={boxX(i) - 4} y2={BOX_Y + BOX_H / 2} class="arrow" class:ok class:bad={!ok} />
          <path
            d="M{boxX(i) - 8},{BOX_Y + BOX_H / 2 - 4} L{boxX(i) - 2},{BOX_Y + BOX_H / 2} L{boxX(i) - 8},{BOX_Y + BOX_H / 2 + 4} Z"
            class="head"
            class:ok
            class:bad={!ok}
          />
        {/if}
        <rect x={boxX(i)} y={BOX_Y} width={BOX_W} height={BOX_H} rx="6" class="box" class:ok={n.ok} class:bad={!n.ok} />
        <text x={boxX(i) + BOX_W / 2} y={BOX_Y + BOX_H / 2 + 4} class="t" text-anchor="middle">
          {n.label.length > 15 ? n.label.slice(0, 14) + '…' : n.label}
        </text>
        {#if !n.ok}
          <path
            d="M{boxX(i) + BOX_W - 14},{BOX_Y + 4} l10,10 m0,-10 l-10,10"
            class="cross"
          />
        {/if}
      {/each}
      <text x={boxX(0) + BOX_W / 2} y={BOX_Y - 10} class="tag" class:premium={chain.tier === 'premium'} text-anchor="middle">
        {chain.tier === 'free' ? 'Free' : 'Premium'}
      </text>
      <text x={boxX(1) - GAP / 2} y={BOX_Y + BOX_H + 22} class="t muted" text-anchor="middle">via {src.via}</text>
    </svg>
  </figure>

  <div class="status" class:bad={!chain.ok} aria-live="polite">
    <h3>{heading}</h3>
    {#if chain.problem}<p>{chain.problem}</p>{/if}
    <p>{src.note}</p>
    {#if out === 'hdmi'}
      <p>The receiver or HDMI audio extractor does the digital-to-analog step.</p>
    {/if}
  </div>

  <p class="foot">
    Free/Premium split checked on volumio.com on {VOLUMIO_PLANS_CHECKED}; Volumio moves features between plans, so check the current plans.
  </p>
  <p class="see-also">
    See also:
    <a href="/guides/pi-wifi-2-4ghz">A Zero 2W only sees 2.4 GHz WiFi</a>,
    <a href="/guides/pi-undervoltage">Power supplies and the lightning bolt</a>,
    <a href="/guides/pi-sd-card-corruption">Keeping the SD card healthy</a>.
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
  figure {
    margin: 0;
  }
  svg {
    display: block;
    width: 100%;
    max-width: 584px;
    height: auto;
  }
  .box {
    fill: var(--bg-panel);
    stroke: var(--border-strong);
    stroke-width: 1.5;
  }
  .box.ok,
  .arrow.ok {
    stroke: var(--accent);
  }
  .box.bad,
  .arrow.bad,
  .cross {
    stroke: var(--danger);
  }
  .arrow {
    stroke-width: 2.5;
  }
  .cross {
    stroke-width: 2.5;
    fill: none;
  }
  .head.ok {
    fill: var(--accent);
  }
  .head.bad {
    fill: var(--danger);
  }
  .t {
    font-family: var(--font-mono);
    font-size: 11px;
    fill: var(--text);
  }
  .t.muted {
    fill: var(--text-muted);
  }
  .tag {
    font-family: var(--font-mono);
    font-size: 12px;
    font-weight: 700;
    fill: var(--accent);
  }
  .tag.premium {
    fill: var(--text-muted);
  }
  .status h3 {
    margin: var(--space-3) 0 var(--space-1);
    font-size: var(--fs-lg);
  }
  .status p {
    margin: 0 0 var(--space-1);
    font-size: var(--fs-md);
  }
  .status.bad h3 {
    color: var(--danger);
  }
  .foot,
  .see-also {
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  .see-also a {
    color: var(--accent);
  }
</style>
