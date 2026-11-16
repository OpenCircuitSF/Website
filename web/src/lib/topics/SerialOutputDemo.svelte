<script lang="ts">
  // Two switches decide where Serial.println() ends up on an ESP32-C3: the
  // USB build flags in platformio.ini, and whether setup() waits for the
  // monitor. Press RST and compare the monitor with pin 21. Rules live in
  // codeOnBoard.ts.
  import { SETUP_LINE, USB_FLAGS, runSerial } from './codeOnBoard';

  let usbFlags = $state(false);
  let waitForMonitor = $state(false);
  let ran = $state(false);

  const result = $derived(runSerial({ usbFlags, waitForMonitor }));

  function change() {
    ran = false;
  }
</script>

<div class="ser">
  <div class="files">
    <div class="file">
      <p class="name">platformio.ini</p>
      <label class="toggle">
        <input type="checkbox" bind:checked={usbFlags} onchange={change} />
        Add the USB build flags
      </label>
      <pre><code
          >{`[env:supermini]
platform = espressif32
board = esp32-c3-devkitm-1
framework = arduino
monitor_speed = 115200`}{usbFlags ? `\nbuild_flags =\n  ${USB_FLAGS.join('\n  ')}` : ''}</code
        ></pre>
    </div>
    <div class="file">
      <p class="name">src/main.cpp</p>
      <label class="toggle">
        <input type="checkbox" bind:checked={waitForMonitor} onchange={change} />
        Wait for the monitor in setup()
      </label>
      <pre><code
          >{`void setup() {
  Serial.begin(115200);`}{waitForMonitor ? `\n  while (!Serial && millis() < 2000) {}` : ''}{`
  Serial.println("${SETUP_LINE}");
}`}</code
        ></pre>
    </div>
  </div>

  <div class="actions">
    <button type="button" class="primary" onclick={() => (ran = true)}>Upload, then watch the monitor</button>
  </div>

  <div class="outs">
    <div class="out">
      <p class="name">Serial monitor (USB)</p>
      <pre class="screen">{ran ? (result.usb.length ? result.usb.join('\n') : '(nothing)') : ''}</pre>
    </div>
    <div class="out">
      <p class="name">Pin 21 (TX), nothing connected</p>
      <pre class="screen faint">{ran ? (result.txPin.length ? result.txPin.join('\n') : '(nothing)') : ''}</pre>
    </div>
  </div>
  <p class="explain" aria-live="polite">{ran ? result.explain : 'Pick your settings, then upload.'}</p>
</div>

<style>
  .files,
  .outs {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
    gap: var(--space-3);
  }
  .file,
  .out {
    min-width: 0;
  }
  .name {
    margin: 0 0 var(--space-1);
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  .toggle {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    margin-bottom: var(--space-2);
    font-size: var(--fs-base);
  }
  pre {
    margin: 0;
    padding: var(--space-3);
    border-radius: 6px;
    border: var(--border-w) solid var(--border);
    background: var(--bg-panel);
    font-size: var(--fs-sm);
    overflow-x: auto;
  }
  .screen {
    min-height: 6.5em;
    color: var(--accent);
  }
  .screen.faint {
    color: var(--text-muted);
  }
  .actions {
    margin: var(--space-4) 0;
  }
  .actions button {
    font: inherit;
    font-size: var(--fs-base);
    padding: 6px 14px;
    border-radius: var(--radius);
    border: var(--border-w) solid var(--accent);
    background: var(--accent);
    color: var(--accent-text);
    cursor: pointer;
  }
  .explain {
    margin: var(--space-3) 0 0;
    font-size: var(--fs-md);
  }
</style>
