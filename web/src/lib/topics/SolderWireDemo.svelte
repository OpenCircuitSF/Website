<script lang="ts">
  // Pick a solder wire: alloy and diameter. The card shows what each one
  // melts at, the iron setting it wants, how a good joint looks, and whether
  // it belongs anywhere near a circuit board. Rules live in soldering.ts.
  import GuideArt from './GuideArt.svelte';
  import { ALLOYS, DIAMETERS_MM, SPOOL_ART, alloyById, diameterAdvice, type AlloyId } from './soldering';

  let alloyId = $state<AlloyId>('sn63');
  let diameter = $state<number>(0.8);

  const alloy = $derived(alloyById(alloyId));
  const advice = $derived(diameterAdvice(diameter));
  const melt = $derived(
    alloy.meltC ? (alloy.meltC[0] === alloy.meltC[1] ? `${alloy.meltC[0]} °C` : `${alloy.meltC[0]}–${alloy.meltC[1]} °C`) : '—',
  );
  const iron = $derived(alloy.ironC ? `${alloy.ironC[0]}–${alloy.ironC[1]} °C` : '—');
</script>

<div class="wire">
  <div class="pickers">
    <div class="group" role="radiogroup" aria-label="Solder alloy">
      {#each ALLOYS as a (a.id)}
        <button type="button" role="radio" aria-checked={alloyId === a.id} class:on={alloyId === a.id} onclick={() => (alloyId = a.id)}>{a.name}</button>
      {/each}
    </div>
    <div class="group" role="radiogroup" aria-label="Wire diameter">
      {#each DIAMETERS_MM as d (d)}
        <button type="button" role="radio" aria-checked={diameter === d} class:on={diameter === d} onclick={() => (diameter = d)}>{d.toFixed(1)} mm</button>
      {/each}
    </div>
  </div>

  <div class="layout">
    <figure>
      <GuideArt art={SPOOL_ART} />
      <figcaption>Inside the wire: a core of flux (the gold dot) that cleans the metal as the solder melts. Look for “rosin” or “no-clean” flux core.</figcaption>
    </figure>

    <div class="card" class:bad={!alloy.forElectronics} aria-live="polite">
      <h3>{alloy.name}</h3>
      <p class="makeup">{alloy.makeup}</p>
      <dl>
        <dt>Melts at</dt>
        <dd>{melt}</dd>
        <dt>Set the iron to</dt>
        <dd>{iron}</dd>
        <dt>Good joint looks</dt>
        <dd>{alloy.finish}</dd>
      </dl>
      <p class="verdict">
        <strong>{alloy.forElectronics ? 'Verdict:' : 'Don’t:'}</strong>
        {alloy.verdict}
      </p>
      {#if alloy.forElectronics}
        <p class="dia {advice.fit}"><strong>{diameter.toFixed(1)} mm:</strong> {advice.text}</p>
      {/if}
    </div>
  </div>
</div>

<style>
  .pickers {
    display: grid;
    gap: var(--space-3);
    margin-bottom: var(--space-4);
  }
  .group {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
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
  .layout {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: var(--space-5);
    align-items: start;
  }
  figure {
    margin: 0;
  }
  figcaption {
    margin-top: var(--space-2);
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  .card {
    padding: var(--space-4);
    border: 2px solid var(--accent);
    border-radius: 6px;
    background: var(--bg-panel);
  }
  .card.bad {
    border-color: var(--danger);
  }
  h3 {
    margin: 0;
  }
  .makeup {
    margin: var(--space-1) 0 var(--space-3);
    color: var(--text-muted);
    font-size: var(--fs-md);
  }
  dl {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: var(--space-1) var(--space-3);
    margin: 0 0 var(--space-3);
    font-size: var(--fs-md);
  }
  dt {
    color: var(--text-faint);
  }
  dd {
    margin: 0;
    font-family: var(--font-mono);
  }
  .verdict,
  .dia {
    margin: var(--space-2) 0 0;
    font-size: var(--fs-md);
  }
  .bad .verdict strong {
    color: var(--danger);
  }
  .dia.poor strong {
    color: var(--danger);
  }
</style>
