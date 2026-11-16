<script lang="ts">
  // BLE basics guide (#0672): a phone app (like nRF Connect or LightBlue)
  // scans, connects, reads and writes the board's LED characteristic, and
  // subscribes to notifications. The conversation is bleStep() in wireless.ts.
  import { onDestroy } from 'svelte';
  import { BLE_START, DEVICE_NAME, LED_CHAR_UUID, LED_SERVICE_UUID, bleStep, type BleAction } from './wireless';

  let s = $state(BLE_START);
  let timer: ReturnType<typeof setTimeout> | undefined;

  function act(a: BleAction) {
    s = bleStep(s, a);
  }
  function scan() {
    act({ type: 'scan' });
    clearTimeout(timer);
    timer = setTimeout(() => act({ type: 'scanDone' }), 700);
  }
  onDestroy(() => clearTimeout(timer));

  const connected = $derived(s.stage === 'connected');
  const shortUuid = (u: string) => `${u.slice(0, 8)}…`;
</script>

<div class="ble">
  <div class="phone" role="group" aria-label="Phone app">
    <p class="bar">BLE scanner app</p>
    {#if s.stage === 'idle'}
      <button type="button" class="primary" onclick={scan}>Scan</button>
    {:else if s.stage === 'scanning'}
      <p class="muted">Scanning…</p>
    {:else if s.stage === 'found'}
      <div class="dev">
        <span><strong>{DEVICE_NAME}</strong><br /><span class="muted">−52 dBm · connectable</span></span>
        <button type="button" class="primary" onclick={() => act({ type: 'connect' })}>Connect</button>
      </div>
    {:else}
      <div class="tree">
        <p class="svc">Service <code title={LED_SERVICE_UUID}>{shortUuid(LED_SERVICE_UUID)}</code></p>
        <div class="chr">
          <p>Characteristic “LED” <code title={LED_CHAR_UUID}>{shortUuid(LED_CHAR_UUID)}</code></p>
          <p class="muted">READ · WRITE · NOTIFY</p>
          <p>Value: <code>{s.phoneValue === null ? '(not read yet)' : `0x0${s.phoneValue}`}</code></p>
          <div class="row">
            <button type="button" onclick={() => act({ type: 'read' })}>Read</button>
            <button type="button" onclick={() => act({ type: 'write', value: 1 })}>Write 01</button>
            <button type="button" onclick={() => act({ type: 'write', value: 0 })}>Write 00</button>
            <button type="button" aria-pressed={s.subscribed} onclick={() => act({ type: 'subscribe' })}>
              {s.subscribed ? 'Subscribed' : 'Subscribe'}
            </button>
          </div>
        </div>
        <button type="button" class="link" onclick={() => act({ type: 'disconnect' })}>Disconnect</button>
      </div>
    {/if}
  </div>

  <div class="board">
    <div class="pcb" role="img" aria-label="SuperMini, LED {s.led ? 'on' : 'off'}">
      <span class="led" class:on={s.led === 1}></span>
      <span class="name">SuperMini</span>
      <span class="state">{connected ? 'connected' : 'advertising'}</span>
    </div>
    <button type="button" onclick={() => act({ type: 'pressBoot' })}>Press BOOT on the board</button>
    <div class="log" aria-live="polite">
      {#each s.log as line, i (i)}
        <div>{line}</div>
      {:else}
        <div class="muted">The board is advertising. Tap Scan on the phone.</div>
      {/each}
    </div>
  </div>
</div>

<style>
  .ble {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: var(--space-4);
    align-items: start;
  }
  .phone {
    padding: var(--space-3);
    border: 3px solid var(--border-strong);
    border-radius: 18px;
    background: var(--bg-panel);
    min-height: 220px;
  }
  .bar {
    margin: 0 0 var(--space-3);
    font-weight: 600;
    border-bottom: var(--border-w) solid var(--border);
    padding-bottom: var(--space-2);
  }
  .muted {
    color: var(--text-faint);
    font-size: var(--fs-sm);
  }
  .dev {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: var(--space-2);
  }
  .tree p {
    margin: 0 0 var(--space-1);
    font-size: var(--fs-md);
  }
  .chr {
    margin: var(--space-2) 0 var(--space-3) var(--space-3);
    padding-left: var(--space-3);
    border-left: 2px solid var(--accent);
  }
  code {
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
  }
  .row {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin-top: var(--space-2);
  }
  button {
    font: inherit;
    font-size: var(--fs-base);
    padding: 4px 12px;
    border-radius: 999px;
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  button.primary,
  button[aria-pressed='true'] {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  button.link {
    border: none;
    padding: 0;
    text-decoration: underline;
    background: none;
  }
  .board {
    display: grid;
    gap: var(--space-3);
  }
  .pcb {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    padding: var(--space-3) var(--space-4);
    background: var(--guide-pcb);
    border: 2px solid var(--guide-pcb-edge);
    border-radius: 6px;
    color: #d8e0d8;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
  }
  .led {
    width: 18px;
    height: 18px;
    border-radius: 50%;
    background: var(--guide-led-off);
    border: 1px solid var(--guide-pcb-edge);
  }
  .led.on {
    background: #4da3ff;
    box-shadow: 0 0 12px 3px #4da3ff;
  }
  .name {
    font-weight: 700;
  }
  .state {
    margin-left: auto;
    color: #9fb09f;
  }
  .log {
    padding: var(--space-3);
    min-height: 8em;
    background: var(--guide-pcb);
    color: #d8e0d8;
    border-radius: 6px;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    overflow-wrap: anywhere;
  }
  .log .muted {
    color: #9fb09f;
  }
</style>
