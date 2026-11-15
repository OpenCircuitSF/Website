<script lang="ts">
  // Multimeter guide 2 (#0666): continuity. Set the dial to the beep symbol,
  // then touch the probes to a wire or a switch. Until the dial is on it, the
  // continuity setting is highlighted and the card says what the symbol looks
  // like and where it is on this meter.
  import MultimeterMeter from './MultimeterMeter.svelte';
  import TargetPicker from './TargetPicker.svelte';
  import { CONTINUITY_TARGETS, METERS, measure, voltsJack, type MeterId } from './multimeter';

  let meterId = $state<MeterId>('a');
  let dial = $state('off');
  let target = $state(CONTINUITY_TARGETS[0].id);

  const m = $derived(METERS[meterId]);
  const t = $derived(CONTINUITY_TARGETS.find((x) => x.id === target) ?? null);
  const reading = $derived(measure({ meter: m, dial, black: 'com', red: voltsJack(m).id }, t));
  const onCont = $derived(dial === 'cont');
  const where = $derived(
    meterId === 'a'
      ? 'On this meter it is on the right of the dial, between hFE and 10A.'
      : 'On this meter it is at the bottom of the dial, between Ω and Hz%.',
  );
  const note = $derived(
    dial === 'off'
      ? {
          tone: 'info' as const,
          title: 'Turn the dial to continuity',
          body: 'Click the flashing setting on the dial: the diode symbol with sound waves.',
        }
      : { tone: reading.tone, title: reading.title, body: reading.message },
  );
</script>

<MultimeterMeter
  {meterId}
  {dial}
  black="com"
  red={voltsJack(m).id}
  {reading}
  {note}
  hint="cont"
  onmeter={(id) => {
    meterId = id;
    dial = 'off';
  }}
  ondial={(id) => (dial = id)}
>
  <div class="find" class:done={onCont}>
    <svg class="sym" viewBox="0 0 64 32" role="img" aria-label="Continuity symbol: a diode with sound waves">
      <path d="M4 16h8M12 6v20l16-10z M28 6v20 M28 16h6" />
      <path d="M41 11a7 7 0 0 1 0 10 M47 7a13 13 0 0 1 0 18 M53 3a19 19 0 0 1 0 26" />
    </svg>
    <div>
      <strong>{onCont ? 'The dial is on continuity' : 'Find this symbol on the dial'}</strong>
      <p>
        A diode (a triangle pointing at a bar) with sound waves beside it. {where} Some meters print a speaker or
        <span class="mono">•)))</span> instead.
      </p>
      {#if !onCont}
        <button type="button" onclick={() => (dial = 'cont')}>Turn the dial to continuity</button>
      {/if}
    </div>
  </div>
  <TargetPicker label="Touch the probes to" targets={CONTINUITY_TARGETS} bind:value={target} />
</MultimeterMeter>

<style>
  .find {
    display: flex;
    gap: var(--space-3);
    align-items: flex-start;
    padding: var(--space-3) var(--space-4);
    border: var(--border-w) solid var(--accent);
    border-radius: 6px;
    background: var(--bg-panel);
  }
  .find.done {
    border-color: var(--border);
  }
  .find p {
    margin: var(--space-1) 0 var(--space-2);
    font-size: var(--fs-md);
  }
  .find.done p {
    margin-bottom: 0;
  }
  .sym {
    flex: none;
    width: 64px;
    height: 32px;
    color: var(--text);
  }
  .sym path {
    fill: none;
    stroke: currentColor;
    stroke-width: 2.5;
    stroke-linecap: round;
    stroke-linejoin: round;
  }
  .mono {
    font-family: var(--font-mono);
  }
  .find button {
    font: inherit;
    font-size: var(--fs-base);
    padding: var(--space-2) var(--space-3);
    border: var(--border-w) solid var(--accent);
    border-radius: var(--radius);
    background: var(--accent);
    color: var(--accent-text);
    cursor: pointer;
  }
  .find button:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
</style>
