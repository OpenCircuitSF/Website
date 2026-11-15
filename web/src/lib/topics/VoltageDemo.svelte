<script lang="ts">
  // Multimeter guide 3 (#0666): DC volts. Set the dial, then measure
  // batteries and the SuperMini's power pins. Swap the probes to see the
  // minus sign.
  import MultimeterMeter from './MultimeterMeter.svelte';
  import TargetPicker from './TargetPicker.svelte';
  import DialHintCard from './DialHintCard.svelte';
  import { DIAL_HINTS, METERS, VOLTAGE_TARGETS, dialOffNote, measure, voltsJack, type MeterId } from './multimeter';

  let meterId = $state<MeterId>('a');
  let dial = $state('off');
  let target = $state(VOLTAGE_TARGETS[1].id);
  let reversed = $state(false);

  const m = $derived(METERS[meterId]);
  const hint = $derived(DIAL_HINTS.voltage[meterId]);
  const t = $derived(VOLTAGE_TARGETS.find((x) => x.id === target) ?? null);
  const reading = $derived(measure({ meter: m, dial, black: 'com', red: voltsJack(m).id, reversed }, t));
</script>

<MultimeterMeter
  {meterId}
  {dial}
  black="com"
  red={voltsJack(m).id}
  {reading}
  note={dial === 'off' ? dialOffNote(hint) : { tone: reading.tone, title: reading.title, body: reading.message }}
  hint={hint.target}
  onmeter={(id) => {
    meterId = id;
    dial = 'off';
  }}
  ondial={(id) => (dial = id)}
>
  <DialHintCard {hint} {dial} onset={(id) => (dial = id)} />
  <TargetPicker label="Measure" targets={VOLTAGE_TARGETS} bind:value={target} />
  <label class="swap">
    <input type="checkbox" bind:checked={reversed} />
    Swap the probes (red on −, black on +)
  </label>
</MultimeterMeter>

<style>
  .swap {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    font-size: var(--fs-md);
    cursor: pointer;
  }
</style>
