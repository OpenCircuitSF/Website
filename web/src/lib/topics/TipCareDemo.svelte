<script lang="ts">
  // Look after (or neglect) a soldering tip and watch it change. A tip that
  // oxidizes black stops taking solder, so heat stops reaching the joint.
  // The tip is drawn inline so its color can change; rules in soldering.ts.
  import { NEW_TIP, TIP_ACTIONS, tipLook, tipStep, tipWets, type TipAction, type TipState } from './soldering';

  let tip = $state<TipState>(NEW_TIP);
  let message = $state('A brand-new tip, freshly tinned. Try a few things and see what happens to it.');

  const look = $derived(tipLook(tip));
  const wets = $derived(tipWets(tip));
  const LOOK_TEXT = { shiny: 'Shiny silver', dull: 'Dull grey', brown: 'Brown, oxidizing', black: 'Black, oxidized' } as const;
  const FILL = { shiny: '#D7DDE0', dull: '#8E979D', brown: '#6B4A2B', black: '#1B1A18' } as const;

  function act(a: TipAction) {
    const r = tipStep(tip, a);
    tip = r.state;
    message = r.message;
  }
  function reset() {
    tip = NEW_TIP;
    message = 'A brand-new tip again.';
  }
</script>

<div class="tipcare">
  <div class="layout">
    <figure>
      <svg viewBox="0 0 320 120" role="img" aria-label="Soldering iron tip: {LOOK_TEXT[look].toLowerCase()}{tip.dirty ? ', with burnt flux on it' : ''}">
        <rect x="6" y="38" width="120" height="44" rx="10" fill="#2B302B" stroke="#1F2621" stroke-width="2" />
        <rect x="14" y="44" width="104" height="6" rx="3" fill="#FFFFFF" opacity="0.15" />
        <rect x="124" y="46" width="70" height="28" fill="#9AA29C" stroke="#1F2621" stroke-width="2" />
        <path d="M192,48 L292,58 Q306,60 306,60 Q306,60 292,62 L192,72 Z" fill={FILL[look]} stroke="#1F2621" stroke-width="2" />
        {#if look === 'shiny'}
          <path d="M200,54 L284,59" stroke="#FFFFFF" stroke-width="3" stroke-linecap="round" opacity="0.85" />
        {/if}
        {#if tip.dirty}
          <circle cx="258" cy="62" r="4" fill="#5A3A1A" />
          <circle cx="238" cy="56" r="3" fill="#5A3A1A" />
          <circle cx="276" cy="60" r="2.5" fill="#5A3A1A" />
        {/if}
      </svg>
      <figcaption>
        <span class="look">{LOOK_TEXT[look]}</span>
        <span class="wets" class:no={!wets}>{wets ? 'Takes solder' : 'Won’t take solder'}</span>
      </figcaption>
    </figure>
    <p class="msg" aria-live="polite">{message}</p>
  </div>

  <div class="actions">
    {#each TIP_ACTIONS as a (a.id)}
      <button type="button" onclick={() => act(a.id)}>{a.label}</button>
    {/each}
    <button type="button" class="reset" onclick={reset}>New tip</button>
  </div>
</div>

<style>
  .layout {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: var(--space-4);
    align-items: center;
    margin-bottom: var(--space-4);
  }
  figure {
    margin: 0;
  }
  svg {
    display: block;
    width: 100%;
    height: auto;
  }
  figcaption {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    font-size: var(--fs-md);
  }
  .look {
    font-weight: 700;
  }
  .wets {
    padding: 0 8px;
    border-radius: 999px;
    background: var(--accent-subtle);
  }
  .wets.no {
    background: var(--danger-subtle);
    color: var(--danger);
  }
  .msg {
    margin: 0;
    padding: var(--space-3) var(--space-4);
    border-left: 4px solid var(--accent);
    background: var(--bg-panel);
    font-size: var(--fs-md);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }
  .actions button {
    font: inherit;
    font-size: var(--fs-base);
    padding: 4px 12px;
    border-radius: 999px;
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  .actions button.reset {
    border-style: dashed;
  }
</style>
