<script lang="ts">
  // SD card backup guide (#0725): pick the computer, the source and the
  // destination, and see the dd command, or why it would erase the wrong
  // disk. A command is never shown without its warning. Logic in piSoftware.ts.
  import { COPIER_NOTE, DISKS, HOSTS, IMAGE, ddPlan, endLabel, type EndId, type HostOs } from './piSoftware';

  let os = $state<HostOs>('mac');
  let src = $state<EndId>('disk4');
  let dst = $state<EndId>('file');

  const disks = $derived(DISKS[os]);
  const ends = $derived([...disks.map((d) => d.id), 'file']);
  const plan = $derived(ddPlan(os, src, dst));
  const srcDisk = $derived(disks.find((d) => d.id === src));
  const dstDisk = $derived(disks.find((d) => d.id === dst));
  const dstText = $derived(
    plan.command === null ? (plan.tone === 'bad' ? 'would be erased' : 'no copy') : plan.verdict === 'backup' ? 'new file' : 'overwritten',
  );
  const svgLabel = $derived(
    `dd from ${srcDisk ? srcDisk.dev : IMAGE} to ${dstDisk ? dstDisk.dev : IMAGE}: the destination ${dstText}.`,
  );

  function pickOs(next: HostOs) {
    os = next;
    const card = DISKS[next].find((d) => d.kind === 'sdcard')!;
    if (src !== 'file') src = card.id;
    if (dst !== 'file') dst = card.id;
    if (src === dst) dst = 'file';
  }
</script>

<!-- #0727: illustration slot -->
<div class="bk">
  <p class="group">Your computer</p>
  <div class="opts" role="radiogroup" aria-label="Your computer">
    {#each HOSTS as h (h.id)}
      <button type="button" role="radio" aria-checked={os === h.id} class:on={os === h.id} onclick={() => pickOs(h.id)}>{h.label}</button>
    {/each}
  </div>

  <p class="caption">{os === 'mac' ? 'diskutil list' : 'lsblk'} (simplified)</p>
  <div class="list">
    {#each disks as d (d.id)}
      <div class="row"><span>{d.dev}</span><span>{d.size}</span><span>{d.label}</span></div>
    {/each}
  </div>

  <div class="pick">
    <div>
      <p class="group">Source (if=)</p>
      <div class="col" role="radiogroup" aria-label="Source (if=)">
        {#each ends as e (e)}
          <button type="button" role="radio" aria-checked={src === e} class:on={src === e} onclick={() => (src = e)}>{endLabel(os, e)}</button>
        {/each}
      </div>
    </div>
    <div>
      <p class="group">Destination (of=)</p>
      <div class="col" role="radiogroup" aria-label="Destination (of=)">
        {#each ends as e (e)}
          <button type="button" role="radio" aria-checked={dst === e} class:on={dst === e} onclick={() => (dst = e)}>{endLabel(os, e)}</button>
        {/each}
      </div>
    </div>
  </div>

  <svg viewBox="0 0 400 140" role="img" aria-label={svgLabel}>
    <rect x="10" y="40" width="130" height="60" rx="4" class="box" />
    {#if srcDisk?.kind === 'internal'}<path d="M40 112 h70 l8 8 h-86 z" class="laptop" />{/if}
    <text x="75" y="66" class="txt">{srcDisk ? srcDisk.dev : IMAGE}</text>
    <text x="75" y="84" class="txt faint">{srcDisk ? srcDisk.size : 'file'}</text>
    <line x1="145" y1="70" x2="245" y2="70" class="arrow" class:dashed={plan.command === null} class:ok={plan.verdict === 'backup'} />
    <path d="M245 64 l10 6 l-10 6 z" class="head" class:ok={plan.verdict === 'backup'} />
    <text x="195" y="60" class="txt">dd</text>
    <rect x="260" y="40" width="130" height="60" rx="4" class="box" class:danger={plan.command === null && plan.tone === 'bad'} />
    {#if dstDisk?.kind === 'internal'}<path d="M290 112 h70 l8 8 h-86 z" class="laptop" />{/if}
    <text x="325" y="66" class="txt">{dstDisk ? dstDisk.dev : IMAGE}</text>
    <text x="325" y="84" class="txt faint">{dstDisk ? dstDisk.size : 'file'}</text>
    <text x="325" y="30" class="txt" class:bad={plan.command === null && plan.tone === 'bad'}
      >{plan.command === null && plan.tone === 'bad' ? '✕ ' : ''}{dstText}</text
    >
  </svg>

  <div class="result {plan.tone}" aria-live="polite">
    <h3>{plan.title}</h3>
    <p>{plan.body}</p>
    {#if plan.command !== null && plan.warning}
      <p class="warning" role="note"><strong>{plan.warning.strong}</strong> {plan.warning.rest}</p>
      <pre><code>{[...plan.before, plan.command, ...plan.after].join('\n')}</code></pre>
    {:else if plan.tone === 'bad'}
      <p>No command: this would erase the wrong disk.</p>
    {/if}
  </div>

  <p class="note">{COPIER_NOTE}</p>
  <p class="note">To restore, Raspberry Pi Imager → Choose OS → Use custom → pick pi.img.gz.</p>
  <p class="note"><a href="/guides/pi-sd-card-corruption">Why pulling the plug corrupts the card</a></p>
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
  .pick {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
    gap: var(--space-3);
    margin-bottom: var(--space-3);
  }
  .col {
    display: grid;
    gap: var(--space-1);
  }
  button[role='radio'] {
    font: inherit;
    font-size: var(--fs-sm);
    text-align: left;
    padding: 6px 12px;
    border-radius: 6px;
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  button[role='radio'].on {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
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
  .list {
    margin: var(--space-1) 0 var(--space-3);
    padding: var(--space-3);
    background: var(--bg-panel);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
  }
  .row {
    display: grid;
    grid-template-columns: 8.5em 6em 1fr;
    gap: var(--space-2);
  }
  svg {
    display: block;
    width: 100%;
    max-width: 480px;
    margin: 0 auto var(--space-3);
  }
  .box {
    fill: var(--bg-panel);
    stroke: var(--border-strong);
    stroke-width: 1.5;
  }
  .box.danger {
    stroke: var(--danger);
    stroke-width: 2.5;
  }
  .laptop {
    fill: none;
    stroke: var(--text-muted);
    stroke-width: 1.5;
  }
  .arrow {
    stroke: var(--text-muted);
    stroke-width: 2.5;
  }
  .arrow.ok {
    stroke: var(--accent);
  }
  .arrow.dashed {
    stroke-dasharray: 6 4;
  }
  .head {
    fill: var(--text-muted);
  }
  .head.ok {
    fill: var(--accent);
  }
  .txt {
    fill: var(--text);
    font: 11px var(--font-mono);
    text-anchor: middle;
  }
  .txt.faint {
    fill: var(--text-faint);
  }
  .txt.bad {
    fill: var(--danger);
    font-weight: 700;
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
  .warning {
    padding: var(--space-2) var(--space-3);
    border-left: 4px solid var(--danger);
  }
  .note {
    margin: var(--space-2) 0 0;
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
</style>
