<script lang="ts">
  // Multimeter guide 4 (#0666): current. The meter has to be in series: break
  // the LED circuit and let the current flow through the meter. Probes from a
  // current jack straight across the battery blow the fuse.
  import MultimeterMeter from './MultimeterMeter.svelte';
  import DialHintCard from './DialHintCard.svelte';
  import { BATTERY_9V, DIAL_HINTS, LED_CIRCUIT, METERS, dialOffNote, jackById, measure, type MeterId } from './multimeter';

  type Hookup = 'series' | 'across';
  let meterId = $state<MeterId>('a');
  let dial = $state('off');
  let red = $state('vma');
  let hookup = $state<Hookup>('series');
  let blown = $state<string[]>([]);
  let jackNote = $state<string | null>(null);

  const m = $derived(METERS[meterId]);
  const hint = $derived(DIAL_HINTS.current[meterId]);
  const target = $derived(hookup === 'series' ? LED_CIRCUIT : BATTERY_9V);
  const reading = $derived(measure({ meter: m, dial, black: 'com', red, blown }, target));
  // Across the battery the loop is whole, so the LED stays lit unless the
  // meter is shorting the battery.
  const ledLit = $derived(hookup === 'across' ? !(reading.tone === 'bad' && !reading.blowsFuse) : reading.ledLit);

  // A blown fuse stays blown until it is replaced.
  $effect(() => {
    if (reading.blowsFuse && !blown.includes(red)) blown = [...blown, red];
  });

  function pickMeter(id: MeterId) {
    meterId = id;
    dial = 'off';
    red = id === 'a' ? 'vma' : 'v';
    blown = [];
    jackNote = null;
  }
  function plug(id: string) {
    if (id === 'com') {
      jackNote = 'Black stays in COM. Click another jack to move red.';
      return;
    }
    jackNote = null;
    red = id;
  }
</script>

<MultimeterMeter
  {meterId}
  {dial}
  black="com"
  {red}
  {reading}
  note={jackNote === null && dial === 'off' ? dialOffNote(hint) : { tone: reading.tone, title: reading.title, body: jackNote ?? reading.message }}
  hint={hint.target}
  onmeter={pickMeter}
  onjack={plug}
  ondial={(id) => (dial = id)}
>
  <DialHintCard {hint} {dial} onset={(id) => (dial = id)} />
  <p class="how">Click a jack to move the red probe. Red is in <strong>{jackById(m, red)?.label}</strong>.</p>
  <div class="picker" role="radiogroup" aria-label="Connect the probes">
    <button type="button" role="radio" aria-checked={hookup === 'series'} class:on={hookup === 'series'} onclick={() => (hookup = 'series')}>
      In series: break the circuit
    </button>
    <button type="button" role="radio" aria-checked={hookup === 'across'} class:on={hookup === 'across'} onclick={() => (hookup = 'across')}>
      Across the battery
    </button>
  </div>

  <svg class="circuit" viewBox="0 0 300 150" role="img" aria-label="A 9 V battery, a 390 Ω resistor and an LED in a loop. The LED is {ledLit ? 'lit' : 'dark'}.">
    <g class="wire">
      <path d="M40 40 V20 H110" />
      <path d="M170 20 H218" />
      <path d="M242 20 H280 V115 H{hookup === 'series' ? 185 : 40}" />
      {#if hookup === 'series'}
        <path d="M115 115 H40 V100" />
      {:else}
        <path d="M40 115 V100" />
      {/if}
      <path class="res" d="M110 20 l5 -8 l10 16 l10 -16 l10 16 l10 -16 l10 16 l5 -8" />
    </g>
    <rect class="batt" x="22" y="40" width="36" height="60" rx="4" />
    <text x="40" y="74" class="lbl" text-anchor="middle">9V</text>
    <text x="62" y="50" class="lbl small">+</text>
    <text x="62" y="98" class="lbl small">−</text>
    <text x="140" y="44" class="lbl small" text-anchor="middle">390 Ω</text>
    <circle class="led" class:lit={ledLit} cx="230" cy="20" r="12" />
    <text x="230" y="48" class="lbl small" text-anchor="middle">LED</text>
    {#if hookup === 'series'}
      <circle class="end" cx="115" cy="115" r="3" />
      <circle class="end" cx="185" cy="115" r="3" />
      <path class="lead red" d="M185 115 Q185 140 160 145" />
      <path class="lead black" d="M115 115 Q115 140 140 145" />
      <text x="150" y="105" class="lbl small" text-anchor="middle">gap</text>
    {:else}
      <path class="lead red" d="M58 45 Q100 60 110 145" />
      <path class="lead black" d="M58 95 Q85 110 90 145" />
    {/if}
  </svg>

  {#if blown.length > 0}
    <button type="button" class="fix" onclick={() => (blown = [])}>Replace the fuse</button>
  {/if}
</MultimeterMeter>

<style>
  .how {
    margin: 0;
    font-size: var(--fs-md);
  }
  .picker {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }
  .picker button,
  .fix {
    font: inherit;
    font-size: var(--fs-base);
    padding: 4px 10px;
    border-radius: 999px;
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  .picker button.on {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .fix {
    align-self: flex-start;
    border-radius: var(--radius);
  }
  .circuit {
    width: 100%;
    max-width: 320px;
    height: auto;
  }
  .wire path {
    fill: none;
    stroke: var(--text-muted);
    stroke-width: 3;
  }
  .batt {
    fill: var(--bg-subtle);
    stroke: var(--text-muted);
    stroke-width: 2;
  }
  .lbl {
    font-family: var(--font-mono);
    font-size: 13px;
    fill: var(--text);
  }
  .lbl.small {
    font-size: 11px;
    fill: var(--text-muted);
  }
  .led {
    fill: var(--guide-led-off);
    stroke: var(--text-muted);
    stroke-width: 2;
  }
  .led.lit {
    fill: #ff3b30;
    filter: drop-shadow(0 0 6px #ff3b30);
  }
  .end {
    fill: var(--text-muted);
  }
  .lead {
    fill: none;
    stroke-width: 4;
    stroke-linecap: round;
  }
  .lead.red {
    stroke: #d32f2f;
  }
  .lead.black {
    stroke: #222;
  }
  :global([data-theme='dark']) .lead.black {
    stroke: #9aa0a6;
  }
  @media (prefers-color-scheme: dark) {
    :global(:root:not([data-theme='light'])) .lead.black {
      stroke: #9aa0a6;
    }
  }
</style>
