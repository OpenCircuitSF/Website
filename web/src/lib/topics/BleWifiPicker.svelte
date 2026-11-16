<script lang="ts">
  // BLE or WiFi guide (#0672): tick what the project needs and get a
  // recommendation with reasons. The rules are in wireless.ts.
  import { NEEDS, recommend, type NeedId } from './wireless';

  let needs = $state<NeedId[]>([]);
  const rec = $derived(recommend(needs));
  const tone = $derived(rec.pick === 'classic' ? 'bad' : rec.pick === 'none' ? 'idle' : 'good');

  function toggle(id: NeedId, on: boolean) {
    needs = on ? [...needs, id] : needs.filter((n) => n !== id);
  }
</script>

<div class="picker">
  <fieldset>
    <legend>My project needs to…</legend>
    {#each NEEDS as n (n.id)}
      <label>
        <input type="checkbox" checked={needs.includes(n.id)} onchange={(e) => toggle(n.id, e.currentTarget.checked)} />
        {n.label}
      </label>
    {/each}
  </fieldset>

  <div class="result {tone}" aria-live="polite">
    <p class="kicker">Use</p>
    <h3>{rec.title}</h3>
    {#if rec.reasons.length}
      <ul>
        {#each rec.reasons as r (r)}
          <li>{r}</li>
        {/each}
      </ul>
    {:else}
      <p>Pick one or more needs and the answer appears here.</p>
    {/if}
  </div>
</div>

<style>
  .picker {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: var(--space-4);
    align-items: start;
  }
  fieldset {
    display: grid;
    gap: var(--space-2);
    margin: 0;
    padding: var(--space-3) var(--space-4);
    border: var(--border-w) solid var(--border-strong);
    border-radius: 6px;
  }
  legend {
    font-weight: 600;
    padding: 0 var(--space-1);
  }
  label {
    display: flex;
    gap: var(--space-2);
    align-items: flex-start;
    margin: 0;
    font-size: var(--fs-md);
    font-weight: 400;
    color: var(--text);
  }
  input {
    accent-color: var(--accent);
    margin-top: 4px;
  }
  .result {
    padding: var(--space-4);
    border: 2px solid var(--border-strong);
    border-radius: 6px;
    background: var(--bg-panel);
  }
  .result.good {
    border-color: var(--accent);
  }
  .result.bad {
    border-color: var(--danger);
  }
  .kicker {
    margin: 0;
    font-size: var(--fs-sm);
    color: var(--text-faint);
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }
  h3 {
    margin: var(--space-1) 0 var(--space-2);
  }
  ul {
    margin: 0;
    padding-left: 1.2em;
    font-size: var(--fs-md);
  }
  li + li {
    margin-top: var(--space-1);
  }
  .result p:last-child {
    margin: 0;
    font-size: var(--fs-md);
  }
</style>
