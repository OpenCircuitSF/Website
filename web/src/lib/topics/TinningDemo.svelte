<script lang="ts">
  // Tin the wire, tin the pad (or skip either), then join them and compare
  // how long it takes and what comes out. Rules live in soldering.ts.
  import GuideArt from './GuideArt.svelte';
  import { TINNING_ART, tinningResult } from './soldering';

  let wireTinned = $state(false);
  let padTinned = $state(false);
  let joined = $state(false);

  const result = $derived(tinningResult(wireTinned, padTinned));

  function reset() {
    wireTinned = false;
    padTinned = false;
    joined = false;
  }
</script>

<div class="tin">
  <div class="actions">
    <button type="button" aria-disabled={joined || wireTinned} onclick={() => !joined && (wireTinned = true)}>Tin the wire</button>
    <button type="button" aria-disabled={joined || padTinned} onclick={() => !joined && (padTinned = true)}>Tin the pad</button>
    <button type="button" class="primary" aria-disabled={joined} onclick={() => (joined = true)}>Join them</button>
    <button type="button" onclick={reset}>Start over</button>
  </div>

  {#if !joined}
    <div class="parts">
      <figure>
        <GuideArt art={wireTinned ? TINNING_ART.wireTinned : TINNING_ART.wireBare} />
        <figcaption>Wire: <strong>{wireTinned ? 'tinned' : 'bare stranded'}</strong></figcaption>
      </figure>
      <figure>
        <GuideArt art={padTinned ? TINNING_ART.padTinned : TINNING_ART.padBare} />
        <figcaption>Pad: <strong>{padTinned ? 'tinned' : 'bare copper'}</strong></figcaption>
      </figure>
    </div>
  {:else}
    <div class="joined">
      <GuideArt art={result.art} />
      <div class="result {result.kind}">
        <p class="time">~{result.seconds} s <span>of heat</span></p>
        <h3>{result.title}</h3>
        <p>{result.body}</p>
      </div>
    </div>
  {/if}
  <p class="sr-only" aria-live="polite">{joined ? `${result.title}. ${result.body}` : ''}</p>
</div>

<style>
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin-bottom: var(--space-4);
  }
  .actions button {
    font: inherit;
    font-size: var(--fs-base);
    padding: 6px 14px;
    border-radius: var(--radius);
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  .actions button.primary {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .actions button[aria-disabled='true'] {
    opacity: 0.5;
    cursor: default;
  }
  .parts,
  .joined {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
    gap: var(--space-4);
    align-items: center;
  }
  figure {
    margin: 0;
  }
  figcaption {
    font-size: var(--fs-md);
    color: var(--text-muted);
  }
  .result {
    padding: var(--space-4);
    border: 2px solid var(--accent);
    border-radius: 6px;
    background: var(--bg-panel);
  }
  .result.slow {
    border-color: var(--warning);
  }
  .result.weak {
    border-color: var(--danger);
  }
  .time {
    margin: 0;
    font-family: var(--font-mono);
    font-size: clamp(28px, 6vw, 40px);
    font-weight: 700;
    line-height: 1.1;
  }
  .time span {
    font-size: 0.45em;
    color: var(--text-faint);
  }
  h3 {
    margin: var(--space-2) 0 var(--space-1);
  }
  .result p:last-child {
    margin: 0;
    font-size: var(--fs-md);
  }
</style>
