<script lang="ts">
  // Build a platformio.ini for the SuperMini line by line. Tap a line to see
  // what it does; wrong choices are flagged. Rules live in codeOnBoard.ts.
  import { DEFAULT_INI, INI_EXPLAIN, INI_OPTIONS, buildIni, checkIni, type IniConfig, type IniKey } from './codeOnBoard';

  let cfg = $state<IniConfig>({ ...DEFAULT_INI });
  let picked = $state<IniKey>('board');

  const lines = $derived(buildIni(cfg));
  const problems = $derived(checkIni(cfg));
  const errors = $derived(problems.filter((p) => p.severity === 'error').length);
  const summary = $derived(
    problems.length === 0
      ? 'Looks good. This builds and uploads to a SuperMini.'
      : `${problems.length} ${problems.length === 1 ? 'thing' : 'things'} to fix: ${problems.map((p) => p.text).join(' ')}`,
  );
  const badKeys = $derived(new Set(problems.map((p) => p.key)));
</script>

<div class="ini">
  <div class="controls">
    <label>
      <span>Environment name</span>
      <input type="text" bind:value={cfg.env} spellcheck="false" autocomplete="off" />
    </label>
    <label>
      <span>platform</span>
      <select bind:value={cfg.platform}>
        {#each INI_OPTIONS.platform as o (o)}<option value={o}>{o}</option>{/each}
      </select>
    </label>
    <label>
      <span>board</span>
      <select bind:value={cfg.board}>
        {#each INI_OPTIONS.board as o (o)}<option value={o}>{o}</option>{/each}
      </select>
    </label>
    <label>
      <span>framework</span>
      <select bind:value={cfg.framework}>
        {#each INI_OPTIONS.framework as o (o)}<option value={o}>{o}</option>{/each}
      </select>
    </label>
    <label>
      <span>monitor_speed</span>
      <select bind:value={cfg.monitorSpeed}>
        {#each INI_OPTIONS.monitorSpeed as o (o)}<option value={o}>{o}</option>{/each}
      </select>
    </label>
    <label class="check"><input type="checkbox" bind:checked={cfg.usbFlags} /> USB build flags</label>
    <label class="check"><input type="checkbox" bind:checked={cfg.fastled} /> FastLED library</label>
  </div>

  <div class="layout">
    <div class="file">
      <p class="name">platformio.ini</p>
      <ul class="lines" aria-label="platformio.ini lines">
        {#each lines as l (l.key)}
          <li>
            <button
              type="button"
              class:on={picked === l.key}
              class:bad={badKeys.has(l.key)}
              aria-pressed={picked === l.key}
              onclick={() => (picked = l.key)}><code>{l.text}</code></button
            >
          </li>
        {/each}
      </ul>
    </div>
    <div class="side">
      <div class="explain">
        <p class="name">{picked}</p>
        <p>{INI_EXPLAIN[picked]}</p>
      </div>
      <div class="verdict" class:err={errors > 0} class:warn={errors === 0 && problems.length > 0}>
        <p class="name">{problems.length === 0 ? 'Check' : errors > 0 ? 'Won’t work' : 'Works, but…'}</p>
        <ul>
          {#each problems as p (p.text)}<li>{p.text}</li>{:else}<li>Looks good. This builds and uploads to a SuperMini.</li>{/each}
        </ul>
      </div>
      <p class="sr-only" aria-live="polite">{summary}</p>
    </div>
  </div>

  <p class="where">
    Your code goes in <code>src/main.cpp</code>, next to <code>platformio.ini</code>. In VS Code, the PlatformIO toolbar along
    the bottom has <strong>Build</strong> (✓), <strong>Upload</strong> (→) and <strong>Serial Monitor</strong> (plug icon).
  </p>
</div>

<style>
  .controls {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
    gap: var(--space-2) var(--space-3);
    margin-bottom: var(--space-4);
  }
  .controls label {
    display: flex;
    flex-direction: column;
    gap: 2px;
    font-size: var(--fs-sm);
    color: var(--text-muted);
    min-width: 0;
  }
  .controls label.check {
    flex-direction: row;
    align-items: center;
    gap: var(--space-2);
    font-size: var(--fs-base);
    color: var(--text);
  }
  .controls span {
    font-family: var(--font-mono);
  }
  input[type='text'],
  select {
    font: inherit;
    font-size: var(--fs-base);
    padding: 4px 6px;
    border-radius: var(--radius);
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    min-width: 0;
  }
  .layout {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: var(--space-4);
    align-items: start;
  }
  .file,
  .side {
    min-width: 0;
  }
  .name {
    margin: 0 0 var(--space-1);
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  .lines {
    list-style: none;
    margin: 0;
    padding: var(--space-2);
    border-radius: 6px;
    border: var(--border-w) solid var(--border);
    background: var(--bg-panel);
  }
  .lines button {
    display: block;
    width: 100%;
    text-align: left;
    padding: 2px 6px;
    border: var(--border-w) solid transparent;
    border-radius: 4px;
    background: transparent;
    color: var(--text);
    cursor: pointer;
    font-size: var(--fs-sm);
  }
  .lines code {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .lines button.on {
    border-color: var(--accent);
    background: var(--accent-subtle);
  }
  .lines button.bad {
    color: var(--danger);
  }
  .explain,
  .verdict {
    padding: var(--space-3);
    border-radius: 6px;
    border: var(--border-w) solid var(--border);
    background: var(--bg-panel);
    margin-bottom: var(--space-3);
    font-size: var(--fs-md);
  }
  .explain p:last-child {
    margin: 0;
  }
  .verdict {
    border: 2px solid var(--accent);
  }
  .verdict.warn {
    border-color: var(--warning);
  }
  .verdict.err {
    border-color: var(--danger);
  }
  .verdict ul {
    margin: 0;
    padding-left: var(--space-5);
  }
  .where {
    margin: var(--space-3) 0 0;
    font-size: var(--fs-base);
    color: var(--text-muted);
  }
</style>
