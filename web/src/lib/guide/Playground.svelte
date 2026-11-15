<script lang="ts">
  // The "change the code" playground. Mirrors the real loop on purpose:
  // edit the constants -> press Upload -> the strip changes. Edits do nothing
  // until uploaded, uploading with the serial monitor open fails exactly the
  // way PlatformIO does, and the monitor's baud rate can be set wrong.
  import { onDestroy } from 'svelte';
  import LedStrip from './LedStrip.svelte';
  import { Clock } from './clock.svelte';
  import {
    DEFAULT_CONFIG,
    NAMED_COLORS,
    POWER_LIMIT_MA,
    describePosition,
    frameMilliamps,
    limitedBrightness,
    physicalStrip,
    positionAt,
    renderFrame,
    type StripConfig,
  } from './leds';
  import { EXERCISES, EXERCISE_GROUPS, type Exercise } from './programmingLeds';

  const clock = new Clock();

  let draft = $state<StripConfig>(structuredClone(DEFAULT_CONFIG));
  let running = $state<StripConfig>(structuredClone(DEFAULT_CONFIG));
  let exercise = $state<Exercise | null>(null);
  let revealed = $state(false);

  type UploadState = 'idle' | 'busy' | 'ok' | 'locked';
  let upload = $state<UploadState>('idle');
  let uploadPct = $state(0);
  let uploadLog = $state<string[]>([]);
  let timers: ReturnType<typeof setTimeout>[] = [];
  onDestroy(() => timers.forEach(clearTimeout));

  const uploadAnnouncement = $derived(
    upload === 'busy'
      ? 'Uploading…'
      : upload === 'ok'
        ? 'Done. ' + (uploadLog[uploadLog.length - 1] ?? '')
        : upload === 'locked'
          ? 'Upload failed: the serial monitor is holding the port.'
          : '',
  );

  let monitorOpen = $state(false);
  let baud = $state(115200);

  // ── What the strip shows ────────────────────────────────────────────────
  const sent = $derived(renderFrame(clock.elapsed, running));
  const effective = $derived(limitedBrightness(sent, running.brightness));
  const milliamps = $derived(Math.round(frameMilliamps(sent, effective)));
  const strip = $derived(physicalStrip(sent, running));
  const now = $derived(
    describePosition(positionAt(clock.elapsed, running.stepMs, running.palette.length), running.palette),
  );

  const dirty = $derived(JSON.stringify(draft) !== JSON.stringify(running));
  const changed = (k: keyof StripConfig) => JSON.stringify(draft[k]) !== JSON.stringify(running[k]);

  // ── Serial output, derived from the board's clock like the firmware ─────
  const serialLines = $derived.by(() => {
    const t = clock.elapsed;
    if (t < 500) return [] as string[];
    const lines = [
      '=== ProgrammingLEDs ===',
      `${running.numLeds} LEDs on GPIO 4, blending through ${running.palette.length} colors, ${running.stepMs}ms per color`,
    ];
    const ticks = Math.floor(t / running.stepMs);
    const from = Math.max(0, ticks - 10);
    for (let n = from; n < ticks; n++) {
      lines.push(`[I][LEDS] blending toward ${running.palette[n % running.palette.length].toLowerCase()}`);
    }
    return lines;
  });

  const GARBAGE = '­¾ýÿ¿þ§¤øÐ×þ¬ÆÿÞ';
  function garble(s: string): string {
    let out = '';
    for (let i = 0; i < s.length; i++) out += GARBAGE[(s.charCodeAt(i) * 7 + i) % GARBAGE.length];
    return out.slice(0, Math.ceil(s.length * 0.6));
  }

  // ── Actions ──────────────────────────────────────────────────────────────
  function load(ex: Exercise) {
    exercise = ex;
    revealed = false;
    draft = ex.apply(structuredClone(DEFAULT_CONFIG));
  }

  function resetDraft() {
    exercise = null;
    revealed = false;
    draft = structuredClone(DEFAULT_CONFIG);
  }

  function later(ms: number, fn: () => void) {
    timers.push(setTimeout(fn, ms));
  }

  function doUpload() {
    if (upload === 'busy') return;
    timers.forEach(clearTimeout);
    timers = [];
    if (monitorOpen) {
      upload = 'locked';
      uploadLog = [
        'Uploading .pio/build/esp32c3/firmware.bin',
        'A fatal error occurred: Could not exclusively lock port /dev/cu.usbmodem101',
        '*** [upload] Error 2',
        '========== [FAILED] ==========',
      ];
      return;
    }
    upload = 'busy';
    uploadPct = 0;
    uploadLog = ['Building in release mode', 'Compiling .pio/build/esp32c3/src/main.cpp.o'];
    later(450, () => (uploadLog = [...uploadLog, 'Linking .pio/build/esp32c3/firmware.elf']));
    later(800, () => (uploadLog = [...uploadLog, 'Uploading .pio/build/esp32c3/firmware.bin']));
    for (let p = 1; p <= 5; p++) {
      later(800 + p * 180, () => {
        uploadPct = p * 20;
        uploadLog = [...uploadLog.filter((l) => !l.startsWith('Writing')), `Writing at 0x000${p}0000... (${p * 20} %)`];
      });
    }
    later(1900, () => {
      uploadLog = [...uploadLog, 'Hard resetting via RTS pin...', '========== [SUCCESS] =========='];
      running = structuredClone($state.snapshot(draft));
      clock.reset();
      upload = 'ok';
      revealed = true;
    });
  }

  function doBuild() {
    if (upload === 'busy') return;
    timers.forEach(clearTimeout);
    timers = [];
    upload = 'busy';
    uploadPct = 0;
    uploadLog = ['Building in release mode', 'Compiling .pio/build/esp32c3/src/main.cpp.o'];
    later(600, () => {
      uploadLog = [...uploadLog, 'Linking .pio/build/esp32c3/firmware.elf', '========== [SUCCESS] ==========', 'Built, but not sent to the board. Press Upload to flash it.'];
      upload = 'ok';
    });
  }

  function addColor(e: Event) {
    const sel = e.currentTarget as HTMLSelectElement;
    if (sel.value) draft.palette = [...draft.palette, sel.value];
    sel.value = '';
  }
  function removeColor(i: number) {
    if (draft.palette.length > 1) draft.palette = draft.palette.filter((_, j) => j !== i);
  }
  function moveColor(i: number, d: -1 | 1) {
    const j = i + d;
    if (j < 0 || j >= draft.palette.length) return;
    const p = [...draft.palette];
    [p[i], p[j]] = [p[j], p[i]];
    draft.palette = p;
  }

  function clampInt(v: number, min: number, max: number) {
    return Number.isFinite(v) ? Math.max(min, Math.min(max, Math.round(v))) : min;
  }
</script>

<div class="playground">
  <!-- The board ------------------------------------------------------------>
  <div class="live">
    <LedStrip leds={strip} brightness={effective} size="lg" label="Simulated strip" />
    <div class="readouts">
      <div class="readout">
        <span class="k">LED 0</span>
        <span class="v">
          {now.from.toLowerCase()} → {now.to.toLowerCase()}
          <span class="text-faint">{Math.round((now.amount / 256) * 100)}%</span>
        </span>
      </div>
      <div class="readout power" style:--pct="{Math.min(100, (milliamps / 600) * 100)}%">
        <span class="k">Current</span>
        <span class="v">~{milliamps} mA <span class="text-faint">/ {POWER_LIMIT_MA} mA USB cap</span></span>
        <span class="meter" aria-hidden="true"><span></span><i></i></span>
        {#if effective < running.brightness}
          <span class="capped">FastLED capped brightness to {effective}</span>
        {/if}
      </div>
      <button type="button" class="ghost" onclick={() => clock.toggle()}>
        {clock.playing ? '❚❚ Pause' : '▶ Play'}
      </button>
    </div>
  </div>

  <div class="cols">
    <!-- Exercises --------------------------------------------------------->
    <section class="exercises" aria-label="Exercises">
      {#each EXERCISE_GROUPS as g (g.id)}
        <div class="group">
          <h4>{g.title} <span class="text-faint">{g.blurb}</span></h4>
          <div class="chips">
            {#each EXERCISES.filter((e) => e.group === g.id) as ex (ex.id)}
              <button type="button" class="chip" class:on={exercise?.id === ex.id} onclick={() => load(ex)}>
                {ex.label}
              </button>
            {/each}
          </div>
        </div>
      {/each}
      <button type="button" class="ghost" onclick={resetDraft}>↺ Back to the kit defaults</button>

      {#if exercise}
        <div class="card">
          <p class="predict"><strong>Predict:</strong> {exercise.predict}</p>
          <p class="mono-line">{exercise.edit}</p>
          {#if revealed && !dirty}
            <p class="explain"><strong>What happened:</strong> {exercise.explain}</p>
          {:else}
            <p class="text-faint">Say what you expect out loud, then press Upload.</p>
          {/if}
        </div>
      {/if}
    </section>

    <!-- The editor ---------------------------------------------------------->
    <section class="editor" aria-label="main.cpp">
      <div class="tab">
        <span>src/main.cpp</span>
        {#if dirty}<span class="unsaved" title="Edited, not uploaded yet">● not uploaded</span>{/if}
      </div>
      <div class="code">
        <div class="line" class:chg={changed('numLeds')}>
          <span class="kw">#define</span> NUM_LEDS&nbsp;&nbsp;&nbsp;<input
            type="number" min="0" max="20" aria-label="NUM_LEDS"
            value={draft.numLeds}
            onchange={(e) => (draft.numLeds = clampInt(+e.currentTarget.value, 0, 20))}
          />
        </div>
        <div class="line"><span class="kw">#define</span> DATA_PIN&nbsp;&nbsp;&nbsp;4</div>
        <div class="line" class:chg={changed('brightness')}>
          <span class="kw">#define</span> BRIGHTNESS <input
            type="number" min="0" max="255" aria-label="BRIGHTNESS"
            value={draft.brightness}
            onchange={(e) => (draft.brightness = clampInt(+e.currentTarget.value, 0, 255))}
          />
        </div>
        <div class="line blank"></div>
        <div class="line" class:chg={changed('stepMs')}>
          <span class="kw">static const</span> uint32_t STEP_MS = <input
            type="number" min="20" max="60000" step="100" aria-label="STEP_MS"
            value={draft.stepMs}
            onchange={(e) => (draft.stepMs = clampInt(+e.currentTarget.value, 20, 60000))}
          />;
        </div>
        <div class="line" class:chg={changed('frameMs')}>
          <span class="kw">static const</span> uint32_t FRAME_MS = <input
            type="number" min="1" max="1000" aria-label="FRAME_MS"
            value={draft.frameMs}
            onchange={(e) => (draft.frameMs = clampInt(+e.currentTarget.value, 1, 1000))}
          />;
        </div>
        <div class="line" class:chg={changed('ledSpread')}>
          <span class="kw">static const</span> uint16_t LED_SPREAD = <input
            type="number" min="0" max="512" aria-label="LED_SPREAD"
            value={draft.ledSpread}
            onchange={(e) => (draft.ledSpread = clampInt(+e.currentTarget.value, 0, 512))}
          />;
        </div>
        <div class="line blank"></div>
        <div class="line"><span class="kw">static const</span> NamedColor PALETTE[] = &#123;</div>
        {#each draft.palette as name, i (i + name)}
          {@const c = NAMED_COLORS.find((n) => n.name === name)!}
          <div class="line pal" class:chg={changed('palette')}>
            &nbsp;&nbsp;&nbsp;&nbsp;&#123; CRGB(CRGB::<span
              class="swatch"
              style:--sw="rgb({c.rgb.r},{c.rgb.g},{c.rgb.b})"
            ></span>{name}), <span class="str">"{name.toLowerCase()}"</span> &#125;,
            <span class="tools">
              <button type="button" aria-label="Move {name} up" onclick={() => moveColor(i, -1)}>↑</button>
              <button type="button" aria-label="Move {name} down" onclick={() => moveColor(i, 1)}>↓</button>
              <button type="button" aria-label="Remove {name}" onclick={() => removeColor(i)}>×</button>
            </span>
          </div>
        {/each}
        <div class="line">
          &nbsp;&nbsp;&nbsp;&nbsp;<select aria-label="Add a color" onchange={addColor}>
            <option value="">+ add a color…</option>
            {#each NAMED_COLORS as c (c.name)}<option value={c.name}>CRGB::{c.name}</option>{/each}
          </select>
        </div>
        <div class="line">&#125;;</div>
        <div class="line blank"></div>
        <div class="line" class:chg={changed('colorOrder')}>
          FastLED.addLeds&lt;WS2812B, DATA_PIN, <select
            aria-label="Color order"
            value={draft.colorOrder}
            onchange={(e) => (draft.colorOrder = e.currentTarget.value as 'GRB' | 'RGB')}
          ><option>GRB</option><option>RGB</option></select>&gt;(leds, NUM_LEDS);
        </div>
        <div class="line blank"></div>
        <div class="line"><span class="kw">for</span> (uint8_t i = 0; i &lt; NUM_LEDS; i++) &#123;</div>
        <div class="line" class:chg={changed('reverse')}>
          &nbsp;&nbsp;&nbsp;&nbsp;leds[i] = colorAt(position <button
            type="button" class="op" aria-label="Toggle plus or minus"
            onclick={() => (draft.reverse = !draft.reverse)}
          >{draft.reverse ? '-' : '+'}</button> i * LED_SPREAD);
        </div>
        <div class="line" class:chg={changed('evenOnly')}>
          &nbsp;&nbsp;&nbsp;&nbsp;<label class="inline-check"
            ><input type="checkbox" bind:checked={draft.evenOnly} /><span class:comment={!draft.evenOnly}
              >{draft.evenOnly ? '' : '// '}if (i % 2) leds[i] = CRGB::Black;</span
            ></label
          >
        </div>
        <div class="line">&#125;</div>
      </div>

      <!-- VS Code's PlatformIO status bar -->
      <div class="statusbar">
        <button type="button" title="Build: compile only, nothing changes on the board" disabled={upload === 'busy'} onclick={doBuild}>✓ Build</button>
        <button type="button" class="upload" class:pulse={dirty && upload !== 'busy'} disabled={upload === 'busy'} onclick={doUpload}>
          → Upload
        </button>
        <button type="button" class:on={monitorOpen} onclick={() => (monitorOpen = !monitorOpen)}>
          🔌 {monitorOpen ? 'Close monitor' : 'Serial monitor'}
        </button>
      </div>

      <p class="sr-only" aria-live="polite">{uploadAnnouncement}</p>
      {#if upload !== 'idle'}
        <div class="term upload-log" class:fail={upload === 'locked'}>
          {#if upload === 'busy'}
            <div class="bar" style:--pct="{uploadPct}%"><span></span></div>
          {/if}
          {#each uploadLog as l, i (i)}<div>{l}</div>{/each}
          {#if upload === 'locked'}
            <p class="hint">The serial monitor is holding the port. Close it, then upload again.</p>
          {/if}
        </div>
      {/if}

      {#if monitorOpen}
        <div class="term monitor">
          <div class="term-head">
            <span>Serial monitor</span>
            <label>
              monitor_speed
              <select bind:value={baud}>
                <option value={115200}>115200</option>
                <option value={9600}>9600</option>
              </select>
            </label>
          </div>
          {#each serialLines as l, i (i)}
            <div>{baud === 115200 ? l : garble(l)}</div>
          {/each}
          {#if baud !== 115200}
            <p class="hint">
              Garbage, not silence: the wiring is fine, only the speed is wrong. Serial.begin(115200) and
              monitor_speed must match.
            </p>
          {/if}
        </div>
      {/if}
    </section>
  </div>
</div>

<style>
  .live {
    display: grid;
    gap: var(--space-3);
    margin-bottom: var(--space-5);
  }
  .readouts {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
    align-items: center;
  }
  .readout {
    display: grid;
    gap: 2px;
    padding: var(--space-2) var(--space-3);
    background: var(--bg-subtle);
    border-radius: var(--radius);
    min-width: 180px;
  }
  .k {
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-faint);
  }
  .v {
    font-family: var(--font-mono);
    font-size: var(--fs-md);
  }
  .meter {
    position: relative;
    display: block;
    height: 6px;
    background: var(--bg-header);
    border-radius: 3px;
    margin-top: 4px;
  }
  .meter span {
    position: absolute;
    inset: 0 auto 0 0;
    width: var(--pct);
    background: var(--accent);
    border-radius: 3px;
    transition: width 0.2s;
  }
  .meter i {
    position: absolute;
    top: -3px;
    bottom: -3px;
    left: calc(500 / 600 * 100%);
    width: 2px;
    background: var(--danger);
  }
  .capped {
    font-size: var(--fs-sm);
    color: var(--warning);
    font-weight: 600;
  }
  .ghost {
    font: inherit;
    font-size: var(--fs-base);
    padding: var(--space-2) var(--space-3);
    background: transparent;
    border: var(--border-w) solid var(--border-strong);
    border-radius: var(--radius);
    color: var(--text);
    cursor: pointer;
  }

  .cols {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1.35fr);
    gap: var(--space-5);
    align-items: start;
  }
  @media (max-width: 860px) {
    .cols {
      grid-template-columns: 1fr;
    }
  }

  .group {
    margin-bottom: var(--space-3);
  }
  h4 {
    margin: 0 0 var(--space-2);
    font-size: var(--fs-md);
  }
  h4 .text-faint {
    font-weight: 400;
    font-size: var(--fs-base);
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }
  .chip {
    font-family: var(--font-mono);
    font-size: var(--fs-base);
    padding: 6px 10px;
    border-radius: 999px;
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  .chip:hover {
    background: var(--bg-subtle);
  }
  .chip.on {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .card {
    margin-top: var(--space-4);
    padding: var(--space-3) var(--space-4);
    border: var(--border-w) solid var(--accent-dim);
    border-radius: 6px;
    background: var(--bg-panel);
  }
  .card p {
    margin: 0 0 var(--space-2);
    font-size: var(--fs-md);
  }
  .mono-line {
    font-family: var(--font-mono);
    font-size: var(--fs-base) !important;
    padding: var(--space-2);
    background: var(--bg-subtle);
    border-radius: var(--radius);
    overflow-x: auto;
  }
  .explain {
    padding: var(--space-2) var(--space-3);
    background: var(--accent-subtle);
    border-left: 3px solid var(--accent);
  }

  .editor {
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
    overflow: hidden;
    background: var(--bg-panel);
  }
  .tab {
    display: flex;
    justify-content: space-between;
    padding: var(--space-2) var(--space-3);
    background: var(--bg-header);
    border-bottom: var(--border-w) solid var(--border);
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  .unsaved {
    color: var(--warning);
    font-weight: 600;
  }
  .code {
    padding: var(--space-3) 0;
    font-family: var(--font-mono);
    font-size: var(--fs-base);
    overflow-x: auto;
  }
  .line {
    padding: 1px var(--space-3);
    white-space: nowrap;
    min-height: 1.6em;
    line-height: 1.6;
    border-left: 3px solid transparent;
  }
  .line.chg {
    background: var(--accent-subtle);
    border-left-color: var(--accent);
  }
  .kw {
    color: var(--accent);
  }
  .str {
    color: var(--warning);
  }
  .comment {
    color: var(--text-faint);
  }
  .code input[type='number'],
  .code select {
    width: auto;
    min-height: 0;
    display: inline-block;
    font-family: var(--font-mono);
    font-size: var(--fs-base);
    padding: 0 4px;
    line-height: 1.4;
    border-radius: 3px;
  }
  .code input[type='number'] {
    width: 7ch;
  }
  .swatch {
    display: inline-block;
    width: 10px;
    height: 10px;
    margin-right: 3px;
    border-radius: 2px;
    background: var(--sw);
    border: 1px solid var(--border-strong);
    vertical-align: -1px;
  }
  .tools {
    margin-left: var(--space-2);
    opacity: 0.5;
  }
  .pal:hover .tools,
  .tools:focus-within {
    opacity: 1;
  }
  .tools button,
  .op {
    font-family: var(--font-mono);
    font-size: var(--fs-base);
    padding: 0 6px;
    border: var(--border-w) solid var(--border);
    border-radius: 3px;
    background: var(--bg-subtle);
    color: var(--text);
    cursor: pointer;
  }
  .op {
    border-color: var(--border-strong);
    font-weight: 700;
  }
  .inline-check {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    margin: 0;
    font: inherit;
    color: inherit;
    cursor: pointer;
  }
  .inline-check input {
    accent-color: var(--accent);
  }

  .statusbar {
    display: flex;
    flex-wrap: wrap;
    gap: 2px;
    padding: 4px;
    background: var(--accent);
  }
  .statusbar button {
    font: inherit;
    font-size: var(--fs-base);
    padding: 6px 12px;
    background: transparent;
    border: 0;
    border-radius: 3px;
    color: var(--accent-text);
    cursor: pointer;
  }
  .statusbar button:hover:not(:disabled),
  .statusbar button.on {
    background: color-mix(in srgb, var(--accent-text) 18%, transparent);
  }
  .statusbar .upload {
    font-weight: 700;
  }
  .statusbar .upload.pulse {
    animation: nudge 1.2s ease-in-out infinite;
  }
  @keyframes nudge {
    50% {
      background: color-mix(in srgb, var(--accent-text) 25%, transparent);
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .statusbar .upload.pulse {
      animation: none;
      text-decoration: underline;
    }
  }

  .term {
    padding: var(--space-3);
    background: var(--bg-page);
    border-top: var(--border-w) solid var(--border);
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    line-height: 1.6;
    max-height: 240px;
    overflow: auto;
  }
  .upload-log.fail div:nth-last-child(n + 2) {
    color: var(--danger);
  }
  .bar {
    height: 4px;
    background: var(--bg-header);
    margin-bottom: var(--space-2);
  }
  .bar span {
    display: block;
    height: 100%;
    width: var(--pct);
    background: var(--accent);
    transition: width 0.15s;
  }
  .term-head {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: var(--space-2);
    color: var(--text-faint);
  }
  .term-head label {
    display: inline-flex;
    gap: var(--space-2);
    align-items: center;
    margin: 0;
    font-family: var(--font-mono);
    font-weight: 400;
  }
  .term-head select {
    width: auto;
    min-height: 0;
    padding: 0 4px;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
  }
  .hint {
    margin: var(--space-2) 0 0;
    font-family: var(--font);
    font-size: var(--fs-base);
    color: var(--warning);
    font-weight: 600;
  }
</style>
