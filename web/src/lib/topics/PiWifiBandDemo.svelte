<script lang="ts">
  // 2.4 vs 5 GHz guide (#0719): pick a router setup and a board and see
  // whether the Pi joins, and why. The outcomes are in piSetup.ts.
  import { PI_BOARDS, ROUTERS, joinOutcome, routerById, type BoardId, type RouterId } from './piSetup';

  let router = $state<RouterId>('one-name');
  let board = $state<BoardId>('zero2w');
  let typed = $state('Home');

  const setup = $derived(routerById(router));
  const net = $derived(setup.networks.find((n) => n.ssid === typed) ?? setup.networks[0]);
  const outcome = $derived(joinOutcome(board, router, net.ssid));
  const boardInfo = $derived(PI_BOARDS.find((b) => b.id === board)!);
  const broadcasts = $derived(new Set(setup.networks.flatMap((n) => n.bands)));
  const svgLabel = $derived(
    `The router broadcasts ${[...broadcasts].map((b) => b + ' GHz').join(' and ')}. The ${boardInfo.label} hears ${boardInfo.bands.map((b) => b + ' GHz').join(' and ')}. ${outcome.joins ? `It joins on ${outcome.band} GHz.` : 'It can’t see the network.'}`,
  );
</script>

<div class="wifi">
  <p class="group">Router</p>
  <div class="nets" role="radiogroup" aria-label="Router">
    {#each ROUTERS as r (r.id)}
      <button type="button" role="radio" aria-checked={router === r.id} class:on={router === r.id} onclick={() => (router = r.id)}>
        <strong>{r.label}</strong>
        <span>{r.detail}</span>
      </button>
    {/each}
  </div>

  <p class="group">Board</p>
  <div class="nets" role="radiogroup" aria-label="Board">
    {#each PI_BOARDS as b (b.id)}
      <button type="button" role="radio" aria-checked={board === b.id} class:on={board === b.id} onclick={() => (board = b.id)}>
        <strong>{b.label}</strong>
        <span>{b.bands.map((x) => x + ' GHz').join(' + ')}</span>
      </button>
    {/each}
  </div>

  {#if router === 'split'}
    <p class="group">Network typed in Imager</p>
    <div class="nets" role="radiogroup" aria-label="Network typed in Imager">
      {#each setup.networks as n (n.ssid)}
        <button type="button" role="radio" aria-checked={net.ssid === n.ssid} class:on={net.ssid === n.ssid} onclick={() => (typed = n.ssid)}>
          <strong>{n.ssid}</strong>
        </button>
      {/each}
    </div>
  {/if}

  <svg viewBox="0 0 360 170" role="img" aria-label={svgLabel}>
    <rect x="20" y="90" width="60" height="26" rx="3" class="box" />
    <text x="50" y="136" class="lbl">{setup.networks.map((n) => n.ssid).join(' / ')}</text>
    {#if broadcasts.has('2.4')}
      <path d="M86 70 A40 40 0 0 1 86 136" class="ring r24" />
      <text x="96" y="62" class="lbl small">2.4 GHz</text>
    {/if}
    {#if broadcasts.has('5')}
      <path d="M100 50 A60 60 0 0 1 100 156" class="ring r5" />
      <text x="124" y="44" class="lbl small">5 GHz</text>
    {/if}
    <rect x="270" y="88" width="70" height="30" rx="4" class="pi" />
    <text x="305" y="108" class="pitxt">{boardInfo.label}</text>
    <text x="305" y="136" class="lbl">hears {boardInfo.bands.join(' + ')} GHz</text>
    {#if outcome.joins}
      <line x1="140" y1="103" x2="270" y2="103" class="link" />
      <text x="205" y="96" class="lbl">{outcome.band} GHz</text>
    {:else}
      <line x1="140" y1="103" x2="270" y2="103" class="link broken" />
      <text x="205" y="96" class="lbl bad">✕ can’t see it</text>
    {/if}
  </svg>

  <div class="layout">
    <div class="result {outcome.tone}" aria-live="polite">
      <h3>{outcome.title}</h3>
      <p>{outcome.reason}</p>
    </div>
    {#if outcome.joins}
      <div class="monitor" role="group" aria-label="Pi terminal">
        {#each outcome.check as line, i (i)}
          <div class="line">{line}</div>
        {/each}
      </div>
    {/if}
  </div>

  <h4>Check once it’s connected</h4>
  <pre><code>iwgetid -r
iw dev wlan0 link</code></pre>
  <p class="note">A <code>freq</code> starting 24… means 2.4 GHz; one starting 5… means 5 GHz.</p>
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
  .lbl.small {
    font-size: 10px;
  }
  .lbl.bad {
    fill: var(--danger);
  }
  .ring {
    fill: none;
    stroke-width: 2;
  }
  .r24 {
    stroke: var(--accent);
  }
  .r5 {
    stroke: var(--warning);
  }
  .link {
    stroke: var(--accent);
    stroke-width: 3;
  }
  .broken {
    stroke: var(--danger);
    stroke-dasharray: 6 4;
  }
  .layout {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: var(--space-4);
    align-items: start;
  }
  .monitor {
    padding: var(--space-3);
    background: var(--guide-pcb);
    color: #d8e0d8;
    border-radius: 6px;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    overflow-wrap: anywhere;
  }
  .line {
    white-space: pre-wrap;
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
