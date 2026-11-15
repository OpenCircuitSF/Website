<script lang="ts">
  // A numbered checklist of guide steps. Every step states the result the
  // student should see, so someone who is stuck knows which step failed
  // (Workshops repo README, "Writing a workshop guide").
  import type { Snippet } from 'svelte';
  import type { GuideStep } from './programmingLeds';
  import type { Progress } from './progress.svelte';

  interface Props {
    steps: GuideStep[];
    progress: Progress;
    /** Number of the first step, so a later list continues the count. */
    start?: number;
    /** Extra content rendered under a given step id (a demo, a diagram). */
    extras?: Record<string, Snippet>;
  }

  let { steps, progress, start = 1, extras = {} }: Props = $props();
</script>

<ol class="steps" {start}>
  {#each steps as step, i (step.id)}
    {@const done = !!progress.done[step.id]}
    <li class="step" class:done>
      <div class="marker" aria-hidden="true">{done ? '✓' : start + i}</div>
      <div class="content">
        <h3>{step.title}</h3>
        {#each step.body.split('\n\n') as para, p (p)}
          <p>{para}</p>
        {/each}
        {#if step.code}
          <pre class="code"><code>{step.code}</code></pre>
        {/if}
        {#if step.link}
          <p><a href={step.link.href} target="_blank" rel="noopener noreferrer">{step.link.label} ↗</a></p>
        {/if}
        <p class="expect"><span class="expect-tag">You should see</span> {step.expect}</p>
        {#if extras[step.id]}
          <div class="extra">{@render extras[step.id]()}</div>
        {/if}
        <label class="check">
          <input type="checkbox" checked={done} onchange={() => progress.toggle(step.id)} />
          {done ? 'Done' : 'Mark as done'}
        </label>
      </div>
    </li>
  {/each}
</ol>

<style>
  .steps {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: var(--space-4);
  }
  .step {
    display: grid;
    grid-template-columns: 36px 1fr;
    gap: var(--space-3);
    padding: var(--space-4);
    background: var(--bg-panel);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
    transition: border-color 0.2s;
  }
  .step.done {
    border-color: var(--accent-dim);
  }
  .marker {
    width: 32px;
    height: 32px;
    border-radius: 50%;
    display: grid;
    place-items: center;
    font-family: var(--font-mono);
    font-weight: 700;
    font-size: var(--fs-md);
    background: var(--bg-subtle);
    border: var(--border-w) solid var(--border-strong);
    color: var(--text);
  }
  .done .marker {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .content {
    min-width: 0;
  }
  h3 {
    margin: 4px 0 var(--space-2);
    font-size: var(--fs-lg);
  }
  p {
    margin: 0 0 var(--space-2);
    max-width: var(--measure);
    font-size: var(--fs-md);
    color: var(--text-muted);
  }
  .code {
    margin: 0 0 var(--space-2);
    padding: var(--space-2) var(--space-3);
    background: var(--bg-subtle);
    border-radius: var(--radius);
    font-family: var(--font-mono);
    font-size: var(--fs-base);
    overflow-x: auto;
  }
  .expect {
    color: var(--text);
    padding: var(--space-2) var(--space-3);
    background: var(--accent-subtle);
    border-left: 3px solid var(--accent);
    border-radius: 0 var(--radius) var(--radius) 0;
  }
  .expect-tag {
    display: block;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--accent);
  }
  .extra {
    margin: var(--space-3) 0;
  }
  .check {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    margin: var(--space-2) 0 0;
    font-size: var(--fs-base);
    font-weight: 600;
    color: var(--text-muted);
    cursor: pointer;
  }
  .check input {
    width: 18px;
    height: 18px;
    accent-color: var(--accent);
  }
</style>
