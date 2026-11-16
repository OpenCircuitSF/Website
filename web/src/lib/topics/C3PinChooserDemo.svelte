<script lang="ts">
  // Pick a job, and every GPIO on the SuperMini lights up green (good),
  // amber (works, with care) or red (avoid). Tap one to see why.
  import C3BoardPicker from './C3BoardPicker.svelte';
  import { GPIO_PINS, HIDDEN_PINS, JOBS, pinAdvice, type Job, type Verdict } from './esp32c3PinsPower';

  let job = $state<Job>('button');
  let pin = $state('gpio6');

  const ids = GPIO_PINS.map((p) => p.id);
  const advice = $derived(pinAdvice(job, pin));
  const good = $derived(ids.filter((id) => pinAdvice(job, id).verdict === 'good').map((id) => Number(id.replace('gpio', ''))).sort((x, y) => x - y));
  const VERDICT_TEXT: Record<Verdict, string> = { good: 'Good choice', care: 'Works, with care', avoid: 'Avoid' };
  const pinName = (id: string) => `GPIO ${id.replace('gpio', '')}`;
</script>

<div class="chooser">
  <div class="group" role="radiogroup" aria-label="Job">
    {#each JOBS as j (j.id)}
      <button type="button" role="radio" aria-checked={job === j.id} class:on={job === j.id} onclick={() => (job = j.id)}>{j.label}</button>
    {/each}
  </div>

  <div class="layout">
    <C3BoardPicker
      pickable={ids}
      selected={pin}
      onpick={(id) => (pin = id)}
      tone={(id) => pinAdvice(job, id).verdict}
      label={(p) => `${pinName(p.id)}: ${VERDICT_TEXT[pinAdvice(job, p.id).verdict].toLowerCase()}`}
    />
    <div class="side">
      <p class="best">Best pins for this: <strong>{good.join(', ')}</strong></p>
      <div class="advice {advice.verdict}" aria-live="polite">
        <p class="verdict">{pinName(pin)}: {VERDICT_TEXT[advice.verdict]}</p>
        <p>{advice.reason}</p>
      </div>
      <div class="legend" aria-hidden="true">
        <span><i class="good"></i>good</span>
        <span><i class="care"></i>with care</span>
        <span><i class="avoid"></i>avoid</span>
      </div>
      <h3>Not on the headers</h3>
      <ul class="hidden">
        {#each HIDDEN_PINS as h (h.pins)}
          <li><strong>{h.pins}:</strong> {h.why}</li>
        {/each}
      </ul>
    </div>
  </div>
</div>

<style>
  .group {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin-bottom: var(--space-4);
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
    grid-template-columns: minmax(0, 240px) 1fr;
    gap: var(--space-4);
    align-items: start;
  }
  @media (max-width: 600px) {
    .layout {
      grid-template-columns: 1fr;
    }
  }
  .side {
    min-width: 0;
  }
  .best {
    margin: 0 0 var(--space-3);
    font-size: var(--fs-md);
  }
  .advice {
    padding: var(--space-3) var(--space-4);
    border-left: 4px solid var(--border-strong);
    background: var(--bg-panel);
    font-size: var(--fs-md);
  }
  .advice.good {
    border-left-color: var(--accent);
  }
  .advice.care {
    border-left-color: var(--warning);
  }
  .advice.avoid {
    border-left-color: var(--danger);
  }
  .advice p {
    margin: 0;
  }
  .advice p.verdict {
    font-weight: 700;
    margin-bottom: var(--space-1);
  }
  .legend {
    display: flex;
    gap: var(--space-4);
    margin: var(--space-3) 0;
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  .legend i {
    display: inline-block;
    width: 10px;
    height: 10px;
    border-radius: 50%;
    margin-right: 4px;
    border: 3px solid;
  }
  .legend .good {
    border-color: #2e8b3e;
  }
  .legend .care {
    border-color: #e0a800;
  }
  .legend .avoid {
    border-color: #d23b2f;
  }
  h3 {
    margin: var(--space-3) 0 var(--space-1);
    font-size: var(--fs-lg);
  }
  .hidden {
    margin: 0;
    padding-left: var(--space-5);
    font-size: var(--fs-base);
  }
</style>
