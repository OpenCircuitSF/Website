<script lang="ts">
  // WiFi dropouts guide (#0725): a yes/no troubleshooter for a Pi that joins
  // the WiFi and then drops off. The tree lives in piSoftware.ts.
  import { tick } from 'svelte';
  import { SPOT_LABELS, WIFI_FIXES, WIFI_STEPS, isWifiFix, wifiNext, wifiSpot, type WifiSpot } from './piSoftware';

  let at = $state('start');
  let path = $state<{ question: string; answer: 'yes' | 'no' }[]>([]);
  let heading = $state<HTMLElement | null>(null);

  const step = $derived(isWifiFix(at) ? null : WIFI_STEPS[at]);
  const fix = $derived(isWifiFix(at) ? WIFI_FIXES[at] : null);
  const spot = $derived(wifiSpot(at));
  const mark = $derived(fix ? 'Likely cause' : 'Checking');
  const announce = $derived(fix ? `Fix: ${fix.title}. ${fix.body}` : (step?.question ?? ''));

  // Centre x of each box in the drawing.
  const SPOT_X: Record<WifiSpot, number> = { supply: 50, pi: 150, air: 250, router: 350 };

  async function answer(a: 'yes' | 'no') {
    if (!step) return;
    path = [...path, { question: step.question, answer: a }];
    at = wifiNext(step.id, a);
    await tick();
    heading?.focus();
  }

  async function restart() {
    at = 'start';
    path = [];
    await tick();
    heading?.focus();
  }
</script>

<!-- #0727: illustration slot -->
<div class="doc">
  <svg viewBox="0 0 400 120" role="img" aria-label={`${mark}: ${SPOT_LABELS[spot]}`}>
    <line x1="90" y1="50" x2="110" y2="50" class="wire" />
    <line x1="190" y1="50" x2="215" y2="50" class="wire" />
    <line x1="285" y1="50" x2="310" y2="50" class="wire" />
    <rect x="10" y="30" width="80" height="40" rx="4" class="box" class:hot={spot === 'supply'} />
    <text x="50" y="54" class="txt">Power supply</text>
    <rect x="110" y="30" width="80" height="40" rx="4" class="box" class:hot={spot === 'pi'} />
    <text x="150" y="54" class="txt">Pi</text>
    <rect x="215" y="25" width="70" height="50" rx="4" class="zone" class:hot={spot === 'air'} />
    <path d="M232 40 A18 18 0 0 1 232 60" class="arc" />
    <path d="M244 34 A28 28 0 0 1 244 66" class="arc" />
    <path d="M256 28 A38 38 0 0 1 256 72" class="arc" />
    <text x="250" y="88" class="txt small">Signal</text>
    <rect x="310" y="30" width="80" height="40" rx="4" class="box" class:hot={spot === 'router'} />
    <text x="350" y="54" class="txt">Router</text>
    <text x={SPOT_X[spot]} y="108" class="mark">{mark}</text>
  </svg>

  {#if path.length}
    <ol class="path">
      {#each path as p, i (i)}
        <li><span>{p.question}</span> <strong class={p.answer}>{p.answer === 'yes' ? 'Yes' : 'No'}</strong></li>
      {/each}
    </ol>
  {/if}

  <section class="card" class:fix={!!fix}>
    {#if fix}
      <p class="kicker">Likely cause: {SPOT_LABELS[fix.spot]}</p>
      <h3 tabindex="-1" bind:this={heading}>{fix.title}</h3>
      <p>{fix.body}</p>
      <pre><code>{fix.commands.join('\n')}</code></pre>
      {#if fix.links.length}
        <ul class="links">
          {#each fix.links as l (l.href)}
            <li><a href={l.href}>{l.label}</a></li>
          {/each}
        </ul>
      {/if}
      <div class="actions">
        <button type="button" class="primary" onclick={restart}>Start over</button>
      </div>
    {:else if step}
      <p class="kicker">Step {path.length + 1}</p>
      <h3 tabindex="-1" bind:this={heading}>{step.question}</h3>
      <p>{step.detail}</p>
      {#if step.id === 'start'}
        <p><a href="/guides/pi-undervoltage">Decode get_throttled in the under-voltage guide</a></p>
      {/if}
      <div class="actions">
        <button type="button" class="primary" onclick={() => answer('yes')}>Yes</button>
        <button type="button" class="primary" onclick={() => answer('no')}>No</button>
        {#if path.length}<button type="button" onclick={restart}>Start over</button>{/if}
      </div>
    {/if}
  </section>
  <p class="sr-only" aria-live="polite">{announce}</p>

  <p class="note">
    Never joins the WiFi at all? That’s a different problem: see the <a href="/guides/pi-wifi-2-4ghz">2.4 GHz guide</a>.
  </p>
</div>

<style>
  svg {
    display: block;
    width: 100%;
    max-width: 480px;
    margin: 0 auto var(--space-3);
  }
  .box,
  .zone {
    fill: var(--bg-panel);
    stroke: var(--border-strong);
    stroke-width: 1.5;
  }
  .zone {
    stroke-dasharray: 3 3;
  }
  .hot {
    stroke: var(--warning);
    stroke-width: 2.5;
    stroke-dasharray: none;
  }
  .wire {
    stroke: var(--text-faint);
    stroke-width: 1.5;
  }
  .arc {
    fill: none;
    stroke: var(--accent);
    stroke-width: 2;
  }
  .txt {
    fill: var(--text);
    font: 11px var(--font-mono);
    text-anchor: middle;
  }
  .txt.small {
    font-size: 10px;
  }
  .mark {
    fill: var(--warning);
    font: 700 11px var(--font-mono);
    text-anchor: middle;
  }
  .path {
    margin: 0 0 var(--space-3);
    padding-left: var(--space-5);
    font-size: var(--fs-base);
    color: var(--text-muted);
  }
  .path li {
    margin-bottom: var(--space-1);
  }
  .path strong {
    font-family: var(--font-mono);
  }
  .path .yes {
    color: var(--accent);
  }
  .path .no {
    color: var(--warning);
  }
  .card {
    padding: var(--space-4);
    border: var(--border-w) solid var(--border-strong);
    border-radius: 6px;
    background: var(--bg-panel);
  }
  .card.fix {
    border: 2px solid var(--accent);
  }
  .kicker {
    margin: 0;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-muted);
  }
  h3 {
    margin: var(--space-1) 0 var(--space-2);
    font-size: var(--fs-lg);
  }
  .card p:not(.kicker) {
    font-size: var(--fs-md);
  }
  pre {
    margin: var(--space-2) 0;
    padding: var(--space-3);
    overflow-x: auto;
    background: var(--bg-panel);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
  }
  .links {
    margin: 0 0 var(--space-3);
    padding-left: var(--space-5);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }
  .actions button {
    font: inherit;
    font-size: var(--fs-base);
    padding: 6px 18px;
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
  .note {
    margin: var(--space-3) 0 0;
    font-size: var(--fs-base);
    color: var(--text-muted);
  }
</style>
