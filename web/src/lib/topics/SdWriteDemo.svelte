<script lang="ts">
  // Cut the power at any moment of a file save and see what the SD card is
  // left with, with and without the overlay file system. Logic in piPower.ts
  // (#0720).
  import { STAGES, cutPower, type Fill } from './piPower';

  let t = $state(40);
  let overlay = $state(false);

  const r = $derived(cutPower(Number(t), overlay));
  const X0 = 10;
  const W = 380;
  const xOf = (v: number) => X0 + (v / 100) * W;
  const FILL_TEXT: Record<Fill, string> = { blank: 'not written', partial: 'half written', written: 'written' };
  const boxes = $derived([
    { label: 'New data', cls: r.card.data, text: FILL_TEXT[r.card.data] },
    { label: 'Journal', cls: r.card.journal, text: FILL_TEXT[r.card.journal] },
    { label: 'Directory', cls: r.card.directory, text: FILL_TEXT[r.card.directory] },
    { label: 'Other files', cls: r.card.others, text: r.card.others === 'ok' ? 'fine' : 'damaged' },
  ]);
</script>

<div class="sd">
  <!-- #0722: illustration slot -->
  <label class="slider">
    <span>When the power is cut: <strong>{r.stage.label}</strong></span>
    <input type="range" min="0" max="100" step="1" bind:value={t} aria-valuetext={r.stage.label} />
  </label>
  <label class="check">
    <input type="checkbox" bind:checked={overlay} />
    <span>Overlay file system on (read-only card)</span>
  </label>

  <figure>
    <svg
      viewBox="0 0 400 190"
      role="img"
      aria-label="Power cut during stage {STAGES.indexOf(r.stage) + 1}, {r.stage.label}. New data {boxes[0].text}, journal {boxes[1].text}, directory {boxes[2].text}, other files {boxes[3].text}{r.card.inRam ? ', the save is in RAM' : ''}."
    >
      {#each STAGES as s, i (s.id)}
        <rect x={xOf(s.from)} y="22" width={xOf(s.to + 1) - xOf(s.from)} height="18" class="seg" class:cur={s.id === r.stage.id} />
        <text x={(xOf(s.from) + xOf(s.to + 1)) / 2} y="35" class="t" text-anchor="middle">{i + 1}</text>
      {/each}
      <!-- plug marker -->
      <line x1={xOf(Number(t))} y1="10" x2={xOf(Number(t))} y2="48" class="cut" />
      <rect x={xOf(Number(t)) - 5} y="2" width="10" height="9" rx="1" class="plug" />

      <rect x="10" y="62" width={r.card.inRam ? 290 : 380} height="100" rx="8" class="card" />
      <text x="20" y="78" class="t muted">SD card</text>
      {#each boxes as b, i (b.label)}
        <rect x={20 + i * (r.card.inRam ? 70 : 92)} y="86" width={r.card.inRam ? 62 : 84} height="40" rx="4" class="cell {b.cls}" />
        <text x={20 + i * (r.card.inRam ? 70 : 92) + (r.card.inRam ? 31 : 42)} y="110" class="t" text-anchor="middle">{b.label}</text>
        <text x={20 + i * (r.card.inRam ? 70 : 92) + (r.card.inRam ? 31 : 42)} y="144" class="t muted" text-anchor="middle">{b.text}</text>
      {/each}
      {#if r.card.inRam}
        <rect x="310" y="62" width="80" height="100" rx="8" class="ram" />
        <text x="350" y="108" class="t" text-anchor="middle">RAM</text>
        <text x="350" y="124" class="t muted" text-anchor="middle">the save</text>
      {/if}
    </svg>
  </figure>
  <ol class="legend">
    {#each STAGES as s (s.id)}
      <li class:cur={s.id === r.stage.id}>{s.label}</li>
    {/each}
  </ol>

  <div class="out {r.tone}" aria-live="polite">
    <h3>{r.title}</h3>
    <p>{r.body}</p>
    <p>After reboot: <strong>{r.file === 'old' ? 'old file' : 'new file'}</strong></p>
  </div>
  <p class="note">A simplified model of an ext4 save on an SD card.</p>
  <pre><code>sudo poweroff
sudo raspi-config</code></pre>
  <p class="links"><a href="/guides/pi-undervoltage">Under-voltage does the same damage</a>.</p>
</div>

<style>
  .slider,
  .check {
    display: grid;
    gap: var(--space-1);
    max-width: 28rem;
    margin-bottom: var(--space-3);
    font-size: var(--fs-base);
  }
  .check {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }
  input[type='range'],
  input[type='checkbox'] {
    accent-color: var(--accent);
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
  .seg {
    fill: var(--bg-panel);
    stroke: var(--border-strong);
    stroke-width: 1;
  }
  .seg.cur {
    fill: var(--accent);
  }
  .cut {
    stroke: var(--danger);
    stroke-width: 2;
  }
  .plug {
    fill: var(--danger);
  }
  .card,
  .ram {
    fill: none;
    stroke: var(--border-strong);
    stroke-width: 1.5;
  }
  .ram {
    stroke: var(--accent);
    stroke-dasharray: 4 3;
  }
  .cell {
    stroke: var(--border-strong);
    stroke-width: 1;
    fill: var(--bg-panel);
  }
  .cell.partial {
    fill: var(--warning);
  }
  .cell.written,
  .cell.ok {
    fill: var(--accent);
    fill-opacity: 0.35;
  }
  .cell.damaged {
    fill: var(--danger);
  }
  .t {
    font-family: var(--font-mono);
    font-size: 10px;
    fill: var(--text);
  }
  .t.muted {
    fill: var(--text-muted);
  }
  .legend {
    margin: var(--space-2) 0;
    padding-left: 1.4em;
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  .legend li.cur {
    color: var(--text);
    font-weight: 600;
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
    color: var(--accent);
  }
  .out.meh h3 {
    color: var(--warning);
  }
  .out.bad h3 {
    color: var(--danger);
  }
  .out p {
    margin: var(--space-1) 0;
  }
  .note,
  .links {
    margin: 0 0 var(--space-3);
    font-size: var(--fs-base);
  }
  .note {
    color: var(--text-muted);
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
</style>
