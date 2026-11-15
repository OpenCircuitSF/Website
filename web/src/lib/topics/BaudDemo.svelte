<script lang="ts">
  // The board sends a message at 115200 baud. Pick the speed the serial
  // monitor listens at, and watch where it samples the wire and what it
  // decodes. A real UART decode (lib/topics/uart.ts), not canned garbage.
  import { decode, encode } from './uart';

  const TX = 115200;
  const RATES = [9600, 57600, 115200, 230400];

  let message = $state('Hello!');
  let rx = $state(9600);

  const wire = $derived(encode(message.slice(0, 24) || ' ', TX));
  const result = $derived(decode(wire, rx));
  const match = $derived(rx === TX);

  // Chart only the first few characters so the bits stay readable.
  const shown = $derived(encode((message || ' ').slice(0, 3), TX));
  const shownEnd = $derived(shown[shown.length - 1].end);
  const shownSamples = $derived(decode(shown, rx).samples.filter((s) => s.t <= shownEnd));

  const W = 720;
  const H = 120;
  const x = (t: number) => 20 + (t / shownEnd) * (W - 40);
  const y = (level: 0 | 1) => (level ? 30 : 80);

  const path = $derived.by(() => {
    let d = `M ${x(0)} ${y(shown[0].level)}`;
    for (const s of shown) d += ` L ${x(s.start)} ${y(s.level)} L ${x(s.end)} ${y(s.level)}`;
    return d;
  });

  const bitWidth = $derived(x(1e6 / TX) - x(0));
</script>

<div class="baud">
  <div class="row">
    <label class="msg">
      The board sends
      <input type="text" maxlength="24" bind:value={message} />
    </label>
    <span class="at">at <strong>115200</strong> baud</span>
  </div>

  <svg class="scope" viewBox="0 0 {W} {H}" role="img" aria-label="Signal on the serial wire, with the receiver's sample points">
    <text class="lbl" x="4" y="34">1</text>
    <text class="lbl" x="4" y="84">0</text>
    {#each shown as s, i (i)}
      {#if i % 10 === 2 && i < shown.length - 2}
        <rect class="frame" x={x(s.start)} y="18" width={bitWidth * 10} height="74" rx="3" />
      {/if}
    {/each}
    <path class="trace" d={path} />
    {#each shownSamples as s, i (i)}
      <line class="tick {s.kind}" x1={x(s.t)} x2={x(s.t)} y1="12" y2="100" />
      <circle class="dot {s.kind}" cx={x(s.t)} cy={y(s.level)} r="4.5" />
    {/each}
    <text class="lbl" x={W / 2} y="116" text-anchor="middle">
      first {Math.min(3, message.length || 1)} characters · each box is one character: start bit, 8 data bits, stop bit
    </text>
  </svg>

  <div class="row">
    <span class="lead">Serial monitor listens at</span>
    <div class="rates" role="radiogroup" aria-label="Receiver baud rate">
      {#each RATES as r (r)}
        <button
          type="button"
          role="radio"
          aria-checked={rx === r}
          class:on={rx === r}
          class:right={r === TX}
          onclick={() => (rx = r)}
        >
          {r}
        </button>
      {/each}
    </div>
  </div>

  <div class="monitor" class:ok={match}>
    <div class="mon-head">
      <span>Serial monitor · {rx} baud</span>
      {#if !match && result.framingErrors > 0}<span class="err">{result.framingErrors} framing errors</span>{/if}
    </div>
    <div class="mon-text">{result.text || '(nothing)'}</div>
  </div>

  <p class="verdict" aria-live="polite">
    {#if match}
      Both sides agree on the speed, so every sample lands in the middle of a bit.
    {:else if rx < TX}
      The monitor samples too slowly: each dot skips over several bits, so it reads the wrong ones. Garbage, not silence.
    {:else}
      The monitor samples too fast: it reads the same bit more than once and runs out of bits mid-character.
    {/if}
  </p>
</div>

<style>
  .row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-3);
    margin-bottom: var(--space-3);
  }
  .msg {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    margin: 0;
    font-size: var(--fs-md);
    font-weight: 400;
    color: var(--text);
  }
  .msg input {
    width: 16ch;
    font-family: var(--font-mono);
  }
  .at,
  .lead {
    font-size: var(--fs-md);
  }
  .scope {
    display: block;
    width: 100%;
    height: auto;
    margin-bottom: var(--space-4);
    background: var(--guide-pcb);
    border-radius: 6px;
  }
  .trace {
    fill: none;
    stroke: var(--guide-wire-data);
    stroke-width: 2.5;
  }
  .frame {
    fill: color-mix(in srgb, var(--guide-wire-data) 8%, transparent);
    stroke: color-mix(in srgb, var(--guide-wire-data) 30%, transparent);
  }
  .tick {
    stroke: color-mix(in srgb, var(--warning) 40%, transparent);
    stroke-dasharray: 2 3;
  }
  .dot {
    fill: var(--warning);
  }
  .dot.start,
  .dot.stop {
    fill: var(--guide-wire-5v);
  }
  .lbl {
    fill: var(--text-faint);
    font-family: var(--font-mono);
    font-size: 11px;
  }
  .rates {
    display: inline-flex;
    flex-wrap: wrap;
    border: var(--border-w) solid var(--border-strong);
    border-radius: var(--radius);
    overflow: hidden;
  }
  .rates button {
    font-family: var(--font-mono);
    font-size: var(--fs-md);
    padding: var(--space-2) var(--space-3);
    border: 0;
    border-right: var(--border-w) solid var(--border);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  .rates button.on {
    background: var(--warning);
    color: var(--bg-panel);
  }
  .rates button.on.right {
    background: var(--accent);
    color: var(--accent-text);
  }
  .monitor {
    border: 2px solid var(--danger);
    border-radius: 6px;
    overflow: hidden;
  }
  .monitor.ok {
    border-color: var(--accent);
  }
  .mon-head {
    display: flex;
    justify-content: space-between;
    padding: var(--space-1) var(--space-3);
    background: var(--bg-header);
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  .err {
    color: var(--danger);
  }
  .mon-text {
    padding: var(--space-3);
    min-height: 2.2em;
    background: var(--bg-page);
    font-family: var(--font-mono);
    font-size: clamp(18px, 4vw, 28px);
    overflow-wrap: anywhere;
  }
  .verdict {
    margin: var(--space-3) 0 0;
    font-size: var(--fs-md);
    max-width: var(--measure);
  }
</style>
