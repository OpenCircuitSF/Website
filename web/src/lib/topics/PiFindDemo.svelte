<script lang="ts">
  // Finding a headless Pi (#0723): pick the network and the laptop's system
  // and see which ways of finding the Pi work. The outcomes are in piStart.ts.
  import { FIND_METHODS, FIND_NETWORKS, LAPTOP_OSES, findCommands, findPi, type LaptopOs, type NetworkId } from './piStart';

  let net = $state<NetworkId>('home');
  let os = $state<LaptopOs>('mac');

  const network = $derived(FIND_NETWORKS.find((n) => n.id === net)!);
  const osLabel = $derived(LAPTOP_OSES.find((o) => o.id === os)!.label);
  const result = $derived(findPi(net, os));
  const bestLabel = $derived(result.best ? FIND_METHODS.find((m) => m.id === result.best)!.label : '');
  const commands = $derived(findCommands(os).join('\n'));
  const WORD = { yes: 'Works', maybe: 'Maybe', no: 'Blocked' } as const;
  const svgLabel = $derived(
    `A ${osLabel} laptop and the Pi on ${network.label}. ${result.tone === 'good' ? `Found with ${bestLabel}.` : result.tone === 'meh' ? 'Maybe found.' : 'Blocked: the laptop can’t find the Pi.'}${network.barrier ? ` Barrier: ${network.barrier}.` : ''}`,
  );
</script>

<!-- #0727: illustration slot -->
<div class="find">
  <p class="group">Network</p>
  <div class="nets" role="radiogroup" aria-label="Network">
    {#each FIND_NETWORKS as n (n.id)}
      <button type="button" role="radio" aria-checked={net === n.id} class:on={net === n.id} onclick={() => (net = n.id)}>
        <strong>{n.label}</strong>
        <span>{n.detail}</span>
      </button>
    {/each}
  </div>

  <p class="group">Laptop</p>
  <div class="nets" role="radiogroup" aria-label="Laptop">
    {#each LAPTOP_OSES as o (o.id)}
      <button type="button" role="radio" aria-checked={os === o.id} class:on={os === o.id} onclick={() => (os = o.id)}>
        <strong>{o.label}</strong>
      </button>
    {/each}
  </div>

  <svg viewBox="0 0 360 150" role="img" aria-label={svgLabel}>
    <rect x="20" y="90" width="60" height="34" rx="3" class="box" />
    <text x="50" y="142" class="lbl">{osLabel}</text>
    <rect x="150" y="94" width="60" height="26" rx="3" class="box" />
    <text x="180" y="111" class="lbl">{network.short}</text>
    <rect x="280" y="92" width="60" height="30" rx="4" class="pi" />
    <text x="310" y="111" class="pitxt">mypi</text>
    <line x1="80" y1="107" x2="150" y2="107" class="wire" />
    <line x1="210" y1="107" x2="280" y2="107" class="wire" />
    {#if network.barrier}
      <line x1="245" y1="90" x2="245" y2="124" class="bar" />
      <text x="245" y="140" class="lbl bad">{network.barrier}</text>
    {/if}
    {#if result.tone === 'good'}
      <path d="M50 88 Q180 0 310 90" class="arc" />
      <text x="180" y="36" class="lbl">{bestLabel}</text>
    {:else if result.tone === 'meh'}
      <path d="M50 88 Q180 0 310 90" class="arc meh" />
      <text x="180" y="36" class="lbl">maybe</text>
    {:else}
      <path d="M50 88 Q180 0 310 90" class="arc broken" />
      <text x="180" y="36" class="lbl bad">✕ blocked</text>
    {/if}
  </svg>

  <div class="result {result.tone}" aria-live="polite">
    <h3>{result.title}</h3>
    <p>{result.advice}</p>
    <ul class="methods">
      {#each result.methods as m (m.id)}
        <li><strong>{m.label}</strong> <span class="v {m.verdict}">{WORD[m.verdict]}</span>: {m.why}</li>
      {/each}
    </ul>
  </div>

  <h4>From the laptop</h4>
  <pre><code>{commands}</code></pre>
  <pre><code>sudo nmap -sn 192.168.1.0/24</code></pre>
  <p class="note">nmap is a separate install; use your own network’s range.</p>

  <p class="note">
    First <a href="/guides/pi-imager-setup">set the hostname in Imager</a>. See also
    <a href="/guides/pi-wifi-2-4ghz">a Pi that never joins at all</a> and
    <a href="/guides/pi-wifi-dropouts">one that drops off later</a>.
  </p>
</div>

<style>
  .group {
    margin: 0 0 var(--space-1);
    font-weight: 600;
  }
  .nets {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
    gap: var(--space-2);
    margin-bottom: var(--space-3);
  }
  .nets button {
    display: grid;
    gap: 2px;
    text-align: left;
    font: inherit;
    font-size: var(--fs-base);
    padding: var(--space-2) var(--space-3);
    border-radius: 6px;
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  .nets button span {
    font-size: var(--fs-sm);
    color: var(--text-faint);
  }
  .nets button.on {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .nets button.on span {
    color: inherit;
  }
  svg {
    display: block;
    width: 100%;
    max-width: 480px;
    margin: 0 auto var(--space-4);
  }
  .box {
    fill: var(--bg-panel);
    stroke: var(--text);
    stroke-width: 1.5;
  }
  .pi {
    fill: var(--bg-panel);
    stroke: var(--accent);
    stroke-width: 2;
  }
  .pitxt {
    fill: var(--text);
    font: 700 11px var(--font-mono);
    text-anchor: middle;
  }
  .lbl {
    fill: var(--text-faint);
    font: 11px var(--font-mono);
    text-anchor: middle;
  }
  .lbl.bad {
    fill: var(--danger);
  }
  .wire {
    stroke: var(--text-faint);
    stroke-width: 2;
  }
  .bar {
    stroke: var(--danger);
    stroke-width: 4;
  }
  .arc {
    fill: none;
    stroke: var(--accent);
    stroke-width: 3;
  }
  .arc.meh {
    stroke: var(--warning);
    stroke-dasharray: 6 4;
  }
  .arc.broken {
    stroke: var(--danger);
    stroke-dasharray: 6 4;
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
    margin: 0;
    font-size: var(--fs-md);
  }
  .methods {
    margin: var(--space-3) 0 0;
    padding-left: var(--space-5);
  }
  .methods li {
    margin-bottom: var(--space-1);
  }
  .v {
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
  }
  h4 {
    margin: var(--space-4) 0 var(--space-1);
  }
  .note {
    font-size: var(--fs-sm);
    color: var(--text-faint);
  }
  pre {
    margin: var(--space-2) 0 0;
    padding: var(--space-3);
    overflow-x: auto;
    background: var(--bg-panel);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
  }
</style>
