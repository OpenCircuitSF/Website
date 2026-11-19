<script lang="ts">
  // Bookworm guide (#0719): pick a step from an old tutorial and see what it
  // does now, and what to type instead. The steps are in piSetup.ts.
  import { BOOKWORM_STEPS, stepById, type StepId } from './piSetup';

  let id = $state<StepId>('boot-files');
  const step = $derived(stepById(id));
</script>

<div class="bookworm">
  <div class="nets" role="radiogroup" aria-label="Old tutorial step">
    {#each BOOKWORM_STEPS as s (s.id)}
      <button type="button" role="radio" aria-checked={id === s.id} class:on={id === s.id} onclick={() => (id = s.id)}>
        <strong>{s.label}</strong>
      </button>
    {/each}
  </div>

  <svg viewBox="0 0 360 90" role="img" aria-label="Before: {step.oldBox}, crossed out. On Bookworm: {step.newBox}.">
    <rect x="6" y="30" width="148" height="34" rx="4" class="old" />
    <text x="80" y="52" class="t">{step.oldBox}</text>
    <line x1="10" y1="47" x2="150" y2="47" class="strike" />
    <text x="80" y="20" class="lbl">before</text>
    <line x1="160" y1="47" x2="196" y2="47" class="arrow" />
    <path d="M190 41 L198 47 L190 53" class="arrow" />
    <rect x="204" y="30" width="150" height="34" rx="4" class="new" />
    <text x="279" y="52" class="t">{step.newBox}</text>
    <text x="279" y="20" class="lbl">Bookworm</text>
  </svg>

  <div aria-live="polite">
    <h3>{step.label}</h3>
    <h4>The old way</h4>
    <pre><code>{step.oldCmd.join('\n')}</code></pre>
    <h4>What you get now</h4>
    <div class="monitor" role="group" aria-label="What Bookworm shows">
      {#each step.now as line, i (i)}
        <div>{line || ' '}</div>
      {/each}
    </div>
    <h4>Do this instead</h4>
    <pre><code>{step.fix.join('\n')}</code></pre>
    <p>{step.why}</p>
  </div>
</div>

<style>
  .nets {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
    gap: var(--space-2);
    margin-bottom: var(--space-3);
  }
  .nets button {
    text-align: left;
    font: inherit;
    font-size: var(--fs-base);
    padding: var(--space-2) var(--space-3);
    border-radius: 6px;
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  .nets button.on {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  svg {
    display: block;
    width: 100%;
    max-width: 480px;
    margin: 0 auto var(--space-3);
  }
  .old {
    fill: var(--bg-panel);
    stroke: var(--danger);
    stroke-width: 1.5;
    stroke-dasharray: 5 3;
  }
  .new {
    fill: var(--bg-panel);
    stroke: var(--accent);
    stroke-width: 2;
  }
  .t {
    fill: var(--text);
    font: 10px var(--font-mono);
    text-anchor: middle;
  }
  .lbl {
    fill: var(--text-faint);
    font: 11px var(--font-mono);
    text-anchor: middle;
  }
  .strike {
    stroke: var(--danger);
    stroke-width: 2;
  }
  .arrow {
    fill: none;
    stroke: var(--text);
    stroke-width: 2;
  }
  h3 {
    margin: 0 0 var(--space-2);
  }
  h4 {
    margin: var(--space-3) 0 var(--space-1);
  }
  .monitor {
    padding: var(--space-3);
    background: var(--guide-pcb);
    color: #d8e0d8;
    border-radius: 6px;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    overflow-wrap: anywhere;
  }
  p {
    font-size: var(--fs-md);
  }
  pre {
    margin: var(--space-2) 0 0;
    padding: var(--space-3);
    overflow-x: auto;
    background: var(--bg-panel);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
  }
</style>
