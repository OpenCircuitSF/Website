<script lang="ts">
  // Type or pick a vcgencmd get_throttled value; see which flags are set.
  // Logic in piPower.ts (#0720).
  import { THROTTLE_FLAGS, decodeThrottled, parseThrottled } from './piPower';

  const PRESETS = ['0x0', '0x50000', '0x50005', '0x80008'];
  let input = $state('0x50005');

  const parsed = $derived(parseThrottled(input));
  const d = $derived(parsed.ok ? decodeThrottled(parsed.value) : undefined);
  const BITS = Array.from({ length: 20 }, (_, i) => 19 - i);
  const flagFor = (bit: number) => THROTTLE_FLAGS.find((f) => f.bit === bit);
  const isSet = (bit: number) => (parsed.ok ? ((parsed.value >>> bit) & 1) === 1 : false);
  const setBits = $derived(parsed.ok ? Array.from({ length: 32 }, (_, b) => b).filter((b) => isSet(b)) : []);
  const ariaLabel = $derived(setBits.length ? `Bits set: ${setBits.join(', ')}` : 'No bits set');
  const fillOf = (bit: number) => (!isSet(bit) ? 'off' : bit < 16 ? 'now' : 'past');
</script>

<div class="td">
  <!-- #0722: illustration slot -->
  <h3>Decode get_throttled</h3>
  <pre><code>vcgencmd get_throttled
sudo dmesg | grep -i voltage</code></pre>
  <label class="field">
    <span>get_throttled value</span>
    <input type="text" bind:value={input} spellcheck="false" autocomplete="off" />
  </label>
  <div class="presets">
    {#each PRESETS as p (p)}
      <button type="button" onclick={() => (input = p)}>{p}</button>
    {/each}
  </div>

  <figure>
    <svg viewBox="0 0 400 70" role="img" aria-label={ariaLabel}>
      {#each BITS as bit, i (bit)}
        {@const f = flagFor(bit)}
        <rect x={10 + i * 19} y="18" width="17" height="24" rx="2" class="cell {fillOf(bit)}" />
        <text x={18.5 + i * 19} y="56" class="t muted" text-anchor="middle">{bit}</text>
        {#if f}
          <text x={18.5 + i * 19} y="12" class="t" text-anchor="middle">{f.short}</text>
        {/if}
      {/each}
    </svg>
  </figure>
  <p class="note">Red: happening now. Amber: has happened since boot.</p>

  <div class="out" aria-live="polite">
    {#if d}
      <h3 class={d.tone}>{d.title}</h3>
      <p>{d.body}</p>
      {#if d.flags.length || d.unknownBits.length}
        <ul>
          {#each d.flags as f (f.bit)}
            <li>{f.label}</li>
          {/each}
          {#each d.unknownBits as b (b)}
            <li>Bit {b} isn’t a documented flag</li>
          {/each}
        </ul>
      {/if}
    {:else if !parsed.ok}
      <p role="alert" class="err">{parsed.error}</p>
    {/if}
  </div>
</div>

<style>
  .td {
    margin-top: var(--space-4);
  }
  .td > h3 {
    margin: 0 0 var(--space-2);
    font-size: var(--fs-lg);
  }
  pre {
    margin: 0 0 var(--space-3);
    padding: var(--space-2) var(--space-3);
    overflow-x: auto;
    background: var(--bg-panel);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
  }
  .field {
    display: grid;
    gap: var(--space-1);
    max-width: 16rem;
    margin-bottom: var(--space-2);
    font-size: var(--fs-base);
  }
  .field input {
    font: inherit;
    font-family: var(--font-mono);
    padding: 4px 8px;
  }
  .presets {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin-bottom: var(--space-3);
  }
  .presets button {
    font-family: var(--font-mono);
    font-size: var(--fs-base);
    padding: 4px 12px;
    border-radius: 999px;
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  figure {
    margin: 0;
  }
  svg {
    display: block;
    width: 100%;
    max-width: 520px;
    height: auto;
  }
  .cell {
    stroke: var(--border-strong);
    stroke-width: 1;
  }
  .cell.off {
    fill: var(--bg-panel);
  }
  .cell.now {
    fill: var(--danger);
  }
  .cell.past {
    fill: var(--warning);
  }
  .t {
    font-family: var(--font-mono);
    font-size: 9px;
    fill: var(--text);
  }
  .t.muted {
    fill: var(--text-muted);
  }
  .note {
    margin: var(--space-1) 0 0;
    font-size: var(--fs-base);
    color: var(--text-muted);
  }
  .out {
    margin: var(--space-3) 0;
    padding: var(--space-3);
    border: var(--border-w) solid var(--border);
    border-radius: 8px;
    background: var(--bg-panel);
  }
  .out h3 {
    margin: 0 0 var(--space-1);
    font-size: var(--fs-lg);
  }
  .out h3.good {
    color: var(--accent);
  }
  .out h3.meh {
    color: var(--warning);
  }
  .out h3.bad,
  .err {
    color: var(--danger);
  }
  .out p,
  .out ul {
    margin: var(--space-1) 0;
  }
</style>
