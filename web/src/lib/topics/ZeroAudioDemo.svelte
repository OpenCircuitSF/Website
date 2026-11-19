<script lang="ts">
  // Four ways to get sound out of a Pi Zero 2W. Fully static: nothing here moves or blinks (WCAG 2.3.1).
  // Logic in piProjects2.ts (#0726).
  import { AUDIO_AXES, ZERO_AUDIO, audioVerdict, zeroAudio, type AudioAxis, type Rating, type ZeroAudioId } from './piProjects2';

  const RATING_WORD: Record<Rating, string> = { good: 'Good', ok: 'OK', poor: 'Poor' };
  const BOX_W = 112;

  let id = $state<ZeroAudioId>('i2s');
  let axis = $state<AudioAxis>('quality');
  const o = $derived(zeroAudio(id));
  const v = $derived(audioVerdict(id, axis));
  const step = $derived((560 - 16) / o.nodes.length);
</script>

<!-- #0727: illustration slot -->
<div class="za">
  <div class="group" role="radiogroup" aria-label="Sound out through">
    {#each ZERO_AUDIO as x (x.id)}
      <button type="button" role="radio" aria-checked={id === x.id} class:on={id === x.id} onclick={() => (id = x.id)}>{x.label}</button>
    {/each}
  </div>
  <div class="group" role="radiogroup" aria-label="What matters most">
    {#each AUDIO_AXES as a (a.id)}
      <button type="button" role="radio" aria-checked={axis === a.id} class:on={axis === a.id} onclick={() => (axis = a.id)}>{a.label}</button>
    {/each}
  </div>

  <figure>
    <svg viewBox="0 0 560 120" role="img" aria-label="{o.title}. The sound goes {o.nodes.join(', then ')}.">
      {#each o.nodes as n, i (i)}
        {@const x = 8 + i * step + (step - Math.min(BOX_W, step - 8)) / 2}
        {@const w = Math.min(BOX_W, step - 8)}
        <rect class="node" class:mid={i > 0 && i < o.nodes.length - 1} {x} y="30" width={w} height="60" rx="8" />
        <foreignObject {x} y="30" width={w} height="60">
          <div class="nlabel">{n}</div>
        </foreignObject>
        {#if i < o.nodes.length - 1}
          {@const x1 = x + w}
          {@const x2 = 8 + (i + 1) * step + (step - w) / 2}
          <line class="arrow" class:dashed={id === 'bt' && i === 0} x1={x1 + 2} y1="60" x2={x2 - 6} y2="60" />
          <path class="head" d="M{x2 - 2} 60 l-7 -4 v8 z" />
          {#if id === 'bt' && i === 0}
            <text class="wl" x={(x1 + x2) / 2} y="50" text-anchor="middle">wireless</text>
          {/if}
        {/if}
      {/each}
    </svg>
  </figure>

  <div class="status {v.tone}" aria-live="polite">
    <h3>{o.title}</h3>
    <p class="verdict">{v.line}</p>
  </div>

  <table>
    <tbody>
      {#each AUDIO_AXES as a (a.id)}
        <tr aria-current={axis === a.id ? 'true' : undefined} class:cur={axis === a.id}>
          <th scope="row">{a.row}</th>
          <td class="r-{o.ratings[a.id].rating}">{RATING_WORD[o.ratings[a.id].rating]}</td>
          <td>{o.ratings[a.id].text}</td>
        </tr>
      {/each}
    </tbody>
  </table>

  <h4>Setup</h4>
  {#if o.setupFile}
    <p class="lbl">In {o.setupFile}:</p>
  {/if}
  <pre class="code"><code>{o.setup.join('\n')}</code></pre>
  <p class="lbl">{o.setupNote}</p>

  <p class="see-also">
    See also:
    {#if o.link}<a href={o.link.href}>{o.link.text}</a>,{/if}
    <a href="/guides/pi-volumio-streaming">A streaming speaker with Volumio</a>.
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
    max-width: 560px;
    height: auto;
  }
  .node {
    fill: var(--bg-panel);
    stroke: var(--border);
    stroke-width: 2;
  }
  .node.mid {
    stroke: var(--accent);
  }
  .nlabel {
    display: flex;
    align-items: center;
    justify-content: center;
    height: 100%;
    padding: 2px 4px;
    box-sizing: border-box;
    text-align: center;
    font-size: 11px;
    line-height: 1.2;
    color: var(--text);
  }
  .arrow {
    stroke: var(--text-faint);
    stroke-width: 2;
  }
  .arrow.dashed {
    stroke-dasharray: 4 4;
  }
  .head {
    fill: var(--text-faint);
  }
  .wl {
    font-size: 10px;
    fill: var(--text-faint);
  }
  .status h3 {
    margin: var(--space-3) 0 var(--space-1);
    font-size: var(--fs-lg);
  }
  .status p {
    margin: 0 0 var(--space-1);
    font-size: var(--fs-md);
  }
  .status.good .verdict {
    color: var(--accent);
  }
  .status.meh .verdict {
    color: var(--warning);
  }
  .status.bad .verdict {
    color: var(--danger);
  }
  table {
    border-collapse: collapse;
    margin: var(--space-2) 0 var(--space-3);
    font-size: var(--fs-sm);
  }
  th,
  td {
    text-align: left;
    padding: 4px 8px;
    border-bottom: var(--border-w) solid var(--border);
    font-weight: 400;
  }
  tr.cur th,
  tr.cur td {
    font-weight: 700;
  }
  .r-poor {
    color: var(--danger);
  }
  .lbl {
    color: var(--text-muted);
    font-size: var(--fs-sm);
  }
  h4 {
    margin: var(--space-3) 0 var(--space-1);
  }
  .code {
    margin: 0 0 var(--space-2);
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
