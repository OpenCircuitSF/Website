<script lang="ts">
  // ESP32-C3 WiFi guide (#0672): pick a network and see what the serial
  // monitor prints, what WiFi.status() ends on, and how to fix it. The
  // outcomes are in wireless.ts.
  import { NETWORKS, connectOutcome, wifiSketch, type NetworkId } from './wireless';

  let network = $state<NetworkId>('home');
  let badAntenna = $state(false);
  let lowTx = $state(false);

  const outcome = $derived(connectOutcome(network, badAntenna, lowTx));
  const sketch = $derived(wifiSketch(lowTx));
</script>

<div class="wifi">
  <div class="nets" role="radiogroup" aria-label="Network">
    {#each NETWORKS as n (n.id)}
      <button type="button" role="radio" aria-checked={network === n.id} class:on={network === n.id} onclick={() => (network = n.id)}>
        <strong>{n.label}</strong>
        <span>{n.detail}</span>
      </button>
    {/each}
  </div>

  <div class="opts">
    <label><input type="checkbox" bind:checked={badAntenna} /> My SuperMini has the antenna problem</label>
    <label><input type="checkbox" bind:checked={lowTx} /> Add <code>WiFi.setTxPower(WIFI_POWER_8_5dBm)</code></label>
  </div>

  <div class="layout">
    <div class="monitor" role="group" aria-label="Serial monitor">
      {#each outcome.log as line, i (i)}
        <div>{line}</div>
      {/each}
    </div>
    <div class="result {outcome.tone}" aria-live="polite">
      <p class="status"><code>{outcome.status}</code></p>
      <h3>{outcome.title}</h3>
      <p>{outcome.fix}</p>
    </div>
  </div>

  <details>
    <summary>The sketch</summary>
    <pre><code>{sketch}</code></pre>
  </details>
</div>

<style>
  .nets {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
    gap: var(--space-2);
    margin-bottom: var(--space-3);
  }
  .nets button {
    display: grid;
    gap: 2px;
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
  .nets button span {
    font-size: var(--fs-sm);
    color: var(--text-faint);
  }
  .nets button.on {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .nets button.on span {
    color: inherit;
  }
  .opts {
    display: grid;
    gap: var(--space-2);
    margin-bottom: var(--space-4);
  }
  .opts label {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin: 0;
    font-size: var(--fs-md);
    font-weight: 400;
    color: var(--text);
  }
  .opts code {
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    overflow-wrap: anywhere;
  }
  .layout {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: var(--space-4);
    align-items: start;
  }
  .monitor {
    padding: var(--space-3);
    min-height: 9em;
    background: var(--guide-pcb);
    color: #d8e0d8;
    border-radius: 6px;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    overflow-wrap: anywhere;
  }
  .result {
    padding: var(--space-4);
    border: 2px solid var(--accent);
    border-radius: 6px;
    background: var(--bg-panel);
  }
  .result.meh {
    border-color: var(--warning);
  }
  .result.bad {
    border-color: var(--danger);
  }
  .status {
    margin: 0;
    font-family: var(--font-mono);
    font-weight: 700;
    overflow-wrap: anywhere;
  }
  h3 {
    margin: var(--space-2) 0 var(--space-1);
  }
  .result p:last-child {
    margin: 0;
    font-size: var(--fs-md);
  }
  details {
    margin-top: var(--space-4);
  }
  summary {
    cursor: pointer;
    font-weight: 600;
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
