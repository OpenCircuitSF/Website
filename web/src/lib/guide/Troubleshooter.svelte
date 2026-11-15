<script lang="ts">
  // "What do you see?" -> likely cause and fix. Ordered by how often each
  // one actually happens in the room.
  import type { Problem } from './programmingLeds';

  interface Props {
    problems: Problem[];
  }

  let { problems }: Props = $props();
  let picked = $state<string | null>(null);
  const current = $derived(problems.find((p) => p.id === picked) ?? null);
</script>

<div class="trouble">
  <fieldset>
    <legend>What do you see?</legend>
    <div class="options">
      {#each problems as p (p.id)}
        <label class="opt" class:on={picked === p.id}>
          <input type="radio" name="symptom" value={p.id} bind:group={picked} />
          {p.symptom}
        </label>
      {/each}
    </div>
  </fieldset>

  <div class="answer" aria-live="polite">
    {#if current}
      <p class="k">Probably</p>
      <p>{current.cause}</p>
      <p class="k">Try</p>
      <p class="fix">{current.fix}</p>
    {:else}
      <p class="text-muted">Pick a symptom. The top of the list is the answer most of the time.</p>
    {/if}
    <p class="safe">
      Nothing here can hurt you. Unplug USB before moving wires, and don't let the bare 5V and GND wires touch.
      Ask early: a stuck laptop is the most normal thing in the room.
    </p>
  </div>
</div>

<style>
  .trouble {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
    gap: var(--space-5);
    align-items: start;
  }
  @media (max-width: 720px) {
    .trouble {
      grid-template-columns: 1fr;
    }
  }
  fieldset {
    margin: 0;
    padding: 0;
    border: 0;
  }
  legend {
    font-weight: 700;
    margin-bottom: var(--space-2);
  }
  .options {
    display: grid;
    gap: var(--space-1);
  }
  .opt {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    margin: 0;
    padding: var(--space-2) var(--space-3);
    font-size: var(--fs-md);
    font-weight: 400;
    color: var(--text);
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
    background: var(--bg-panel);
    cursor: pointer;
  }
  .opt.on {
    border-color: var(--accent);
    background: var(--accent-subtle);
  }
  .opt input {
    accent-color: var(--accent);
  }
  .answer {
    position: sticky;
    top: var(--space-4);
    padding: var(--space-4);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
    background: var(--bg-panel);
  }
  .answer p {
    margin: 0 0 var(--space-2);
    font-size: var(--fs-md);
  }
  .k {
    font-family: var(--font-mono);
    font-size: var(--fs-sm) !important;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--accent);
  }
  .fix {
    font-weight: 600;
  }
  .safe {
    margin-top: var(--space-4) !important;
    padding-top: var(--space-3);
    border-top: var(--border-w) solid var(--border);
    font-size: var(--fs-base) !important;
    color: var(--text-muted);
  }
</style>
