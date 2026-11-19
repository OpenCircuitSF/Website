<script lang="ts">
  // Run-on-boot guide (#0725): a systemd service file built from seven
  // toggles; turn one off and see what systemd does. Logic in piSoftware.ts.
  import { ALL_ON, SCENARIOS, UNIT_TOGGLES, serviceOutcome, unitLines, type Scenario, type UnitOptions } from './piSoftware';

  let scenario = $state<Scenario>('boot');
  let opts = $state<UnitOptions>({ ...ALL_ON });

  const lines = $derived(unitLines(opts));
  const outcome = $derived(serviceOutcome(opts, scenario));
  const svgLabel = $derived(outcome.events.map((e) => e.label).join(', then '));
  const x = (at: number) => 20 + at * 3.6;
</script>

<!-- #0727: illustration slot -->
<div class="svc">
  <p class="group">What happens</p>
  <div class="opts" role="radiogroup" aria-label="What happens">
    {#each SCENARIOS as s (s.id)}
      <button type="button" role="radio" aria-checked={scenario === s.id} class:on={scenario === s.id} onclick={() => (scenario = s.id)}>
        {s.label}
      </button>
    {/each}
  </div>

  <fieldset>
    <legend>Lines in the service file</legend>
    {#each UNIT_TOGGLES as t (t.id)}
      <label>
        <input type="checkbox" bind:checked={opts[t.id]} />
        <span><code>{t.label}</code><small>{t.what}</small></span>
      </label>
    {/each}
  </fieldset>

  <p class="caption"><code>/etc/systemd/system/plantlog.service</code></p>
  <pre class="unit"><code
      >{#each lines as l, i (i)}<span class:off={!l.on}>{l.text}</span>{'\n'}{/each}</code
    ></pre>

  <svg viewBox="0 0 400 90" role="img" aria-label={svgLabel}>
    <line x1="20" y1="45" x2="380" y2="45" class="axis" />
    {#each outcome.events as e, i (i)}
      {#if e.tone === 'bad'}
        <path d={`M${x(e.at) - 5} 40 l10 10 m0 -10 l-10 10`} class="bad" />
      {:else if e.tone === 'good'}
        <circle cx={x(e.at)} cy="45" r="5" class="good" />
      {:else if e.tone === 'meh'}
        <circle cx={x(e.at)} cy="45" r="5" class="meh" />
      {:else}
        <line x1={x(e.at)} y1="40" x2={x(e.at)} y2="50" class="tick" />
      {/if}
      <text x={Math.min(Math.max(x(e.at), 40), 360)} y={i % 2 ? 72 : 26} class="lbl">{e.label}</text>
    {/each}
  </svg>

  <div class="result {outcome.tone}" aria-live="polite">
    <h3>{outcome.title}</h3>
    <p>{outcome.body}</p>
    <p class="status"><code>systemctl status plantlog</code> → <code>{outcome.status}</code></p>
    <div class="term" role="group" aria-label="journalctl -u plantlog">
      <div class="head">journalctl -u plantlog</div>
      {#each outcome.log as l, i (i)}
        <div class="line">{l}</div>
      {/each}
    </div>
  </div>

  <h4>Install it</h4>
  <pre><code>sudo nano /etc/systemd/system/plantlog.service
sudo systemctl daemon-reload
sudo systemctl enable --now plantlog
systemctl status plantlog
journalctl -u plantlog -f</code></pre>
  <p class="note">Replace maker with your user name and the paths with your program’s. The log lines are simplified.</p>
</div>

<style>
  .group {
    margin: 0 0 var(--space-1);
    font-weight: 600;
  }
  .opts {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin-bottom: var(--space-3);
  }
  .opts button {
    font: inherit;
    font-size: var(--fs-base);
    padding: 6px 14px;
    border-radius: 6px;
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  .opts button.on {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  fieldset {
    margin: 0 0 var(--space-3);
    padding: var(--space-3);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
    display: grid;
    gap: var(--space-2);
  }
  legend {
    font-weight: 600;
  }
  label {
    display: flex;
    gap: var(--space-2);
    align-items: flex-start;
    cursor: pointer;
  }
  label span {
    display: grid;
  }
  label small {
    color: var(--text-muted);
    font-size: var(--fs-sm);
  }
  .caption {
    margin: 0;
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  pre {
    margin: var(--space-1) 0 var(--space-3);
    padding: var(--space-3);
    overflow-x: auto;
    background: var(--bg-panel);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
  }
  .off {
    color: var(--text-faint);
  }
  svg {
    display: block;
    width: 100%;
    max-width: 480px;
    margin: 0 auto var(--space-3);
  }
  .axis {
    stroke: var(--border-strong);
    stroke-width: 2;
  }
  .tick {
    stroke: var(--text-muted);
    stroke-width: 1.5;
  }
  .good {
    fill: var(--accent);
  }
  .meh {
    fill: var(--bg-panel);
    stroke: var(--warning);
    stroke-width: 2;
  }
  .bad {
    stroke: var(--danger);
    stroke-width: 2.5;
  }
  .lbl {
    fill: var(--text-muted);
    font: 10px var(--font-mono);
    text-anchor: middle;
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
  h3 {
    margin: 0 0 var(--space-1);
  }
  .result p {
    margin: 0 0 var(--space-2);
    font-size: var(--fs-md);
  }
  .result .status {
    font-size: var(--fs-sm);
    overflow-wrap: anywhere;
  }
  .term {
    padding: var(--space-3);
    background: var(--guide-pcb);
    color: #d8e0d8;
    border-radius: 6px;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    overflow-wrap: anywhere;
  }
  .term .head {
    opacity: 0.7;
    margin-bottom: var(--space-1);
  }
  .line {
    white-space: pre-wrap;
  }
  h4 {
    margin: var(--space-4) 0 var(--space-1);
  }
  .note {
    font-size: var(--fs-sm);
    color: var(--text-faint);
  }
</style>
