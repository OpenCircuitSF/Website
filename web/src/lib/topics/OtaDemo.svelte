<script lang="ts">
  // The two app slots in the ESP32's flash during an over-the-air update.
  // The board runs from one slot while the new firmware is written into the
  // other. Rules live in codeOnBoard.ts.
  import { cableUpload, initialOta, otaUpload, type SlotName } from './codeOnBoard';

  let s = $state(initialOta());
  let sameNetwork = $state(true);
  let passwordMatches = $state(true);
  let dropout = $state(false);
  let newListens = $state(true);
  let last = $state<{ ok: boolean; message: string } | null>(null);

  const SLOTS: SlotName[] = ['app0', 'app1'];

  function cable() {
    const r = cableUpload(s);
    s = r.state;
    last = r;
  }
  function wifi() {
    const r = otaUpload(s, { sameNetwork, passwordMatches, dropout, newListens });
    s = r.state;
    last = r;
  }
  function restart() {
    s = initialOta();
    last = null;
  }
</script>

<div class="ota">
  <div class="flash" role="group" aria-label="Flash memory">
    {#each SLOTS as name (name)}
      {@const slot = s.slots[name]}
      <div class="slot" class:active={s.active === name && slot.version !== null}>
        <p class="name">{name}{s.active === name && slot.version !== null ? ' · running' : ''}</p>
        <p class="ver">{slot.version === null ? 'empty' : `v${slot.version}`}</p>
        <p class="sub">
          {slot.version === null ? '' : slot.listens ? 'listens for updates' : 'no ArduinoOTA.handle()'}
        </p>
      </div>
    {/each}
    <div class="slot data">
      <p class="name">spiffs</p>
      <p class="sub">files, not code</p>
    </div>
  </div>

  <fieldset class="opts">
    <legend>Before you send the update</legend>
    <label><input type="checkbox" bind:checked={sameNetwork} /> Laptop on the same WiFi as the board</label>
    <label><input type="checkbox" bind:checked={passwordMatches} /> Password matches</label>
    <label><input type="checkbox" bind:checked={dropout} /> WiFi drops halfway</label>
    <label><input type="checkbox" bind:checked={newListens} /> New code still calls ArduinoOTA.handle()</label>
  </fieldset>

  <div class="actions">
    <button type="button" onclick={cable}>Upload by cable</button>
    <button type="button" class="primary" onclick={wifi}>Upload over WiFi</button>
    <button type="button" onclick={restart}>Start over</button>
  </div>

  <p class="result" class:bad={last && !last.ok} class:good={last?.ok} aria-live="polite">
    {last ? last.message : 'The board is new and empty. Try WiFi first, then the cable.'}
  </p>
</div>

<style>
  .flash {
    display: grid;
    grid-template-columns: 1fr 1fr 0.8fr;
    gap: var(--space-2);
    margin-bottom: var(--space-4);
  }
  .slot {
    padding: var(--space-3) var(--space-2);
    border: 2px dashed var(--border-strong);
    border-radius: 6px;
    background: var(--bg-panel);
    text-align: center;
    min-width: 0;
  }
  .slot.active {
    border: 2px solid var(--accent);
    background: var(--accent-subtle);
  }
  .slot.data {
    border-style: solid;
    border-color: var(--border);
    opacity: 0.7;
  }
  .slot p {
    margin: 0;
  }
  .name {
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  .ver {
    font-family: var(--font-mono);
    font-size: var(--fs-xl);
    font-weight: 700;
  }
  .sub {
    font-size: var(--fs-sm);
    color: var(--text-muted);
    min-height: 1.3em;
  }
  .opts {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
    gap: var(--space-2);
    margin: 0 0 var(--space-4);
    padding: var(--space-3);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
    font-size: var(--fs-base);
  }
  .opts legend {
    padding: 0 var(--space-1);
    color: var(--text-muted);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin-bottom: var(--space-3);
  }
  .actions button {
    font: inherit;
    font-size: var(--fs-base);
    padding: 6px 14px;
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
  .result {
    margin: 0;
    padding: var(--space-3);
    border-radius: 6px;
    border: 2px solid var(--border);
    font-size: var(--fs-md);
  }
  .result.good {
    border-color: var(--accent);
  }
  .result.bad {
    border-color: var(--danger);
  }
</style>
