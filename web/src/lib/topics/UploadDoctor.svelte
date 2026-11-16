<script lang="ts">
  // A yes/no troubleshooter for an ESP32-C3 that won't take an upload. The
  // tree lives in codeOnBoard.ts; this only walks it and remembers the path.
  import { tick } from 'svelte';
  import { DRIVER_NOTE, TROUBLE_FIXES, TROUBLE_STEPS, isFix, troubleNext } from './codeOnBoard';

  let at = $state('start');
  let path = $state<{ question: string; answer: 'yes' | 'no' }[]>([]);
  let heading = $state<HTMLElement | null>(null);

  const step = $derived(isFix(at) ? null : TROUBLE_STEPS[at]);
  const fix = $derived(isFix(at) ? TROUBLE_FIXES[at] : null);
  const announce = $derived(fix ? `Fix: ${fix.title}. ${fix.body}` : (step?.question ?? ''));

  async function answer(a: 'yes' | 'no') {
    if (!step) return;
    path = [...path, { question: step.question, answer: a }];
    at = troubleNext(step.id, a);
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

<div class="doc">
  {#if path.length}
    <ol class="path">
      {#each path as p, i (i)}
        <li><span>{p.question}</span> <strong class={p.answer}>{p.answer === 'yes' ? 'Yes' : 'No'}</strong></li>
      {/each}
    </ol>
  {/if}

  <section class="card" class:fix={!!fix}>
    {#if fix}
      <p class="kicker">Try this</p>
      <h3 tabindex="-1" bind:this={heading}>{fix.title}</h3>
      <p>{fix.body}</p>
      <div class="actions">
        <button type="button" class="primary" onclick={restart}>Start over</button>
      </div>
    {:else if step}
      <p class="kicker">Step {path.length + 1}</p>
      <h3 tabindex="-1" bind:this={heading}>{step.question}</h3>
      <p>{step.detail}</p>
      <div class="actions">
        <button type="button" class="primary" onclick={() => answer('yes')}>Yes</button>
        <button type="button" class="primary" onclick={() => answer('no')}>No</button>
        {#if path.length}<button type="button" onclick={restart}>Start over</button>{/if}
      </div>
    {/if}
  </section>
  <p class="sr-only" aria-live="polite">{announce}</p>

  <p class="note">{DRIVER_NOTE}</p>
</div>

<style>
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
