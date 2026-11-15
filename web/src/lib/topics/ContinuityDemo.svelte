<script lang="ts">
  // Multimeter guide 2 (#0666): continuity. Set the dial to the beep symbol,
  // then touch the probes to a wire or a switch.
  import MultimeterMeter from './MultimeterMeter.svelte';
  import TargetPicker from './TargetPicker.svelte';
  import { CONTINUITY_TARGETS, METERS, measure, voltsJack, type MeterId } from './multimeter';

  let meterId = $state<MeterId>('a');
  let dial = $state('off');
  let target = $state(CONTINUITY_TARGETS[0].id);

  const m = $derived(METERS[meterId]);
  const t = $derived(CONTINUITY_TARGETS.find((x) => x.id === target) ?? null);
  const reading = $derived(measure({ meter: m, dial, black: 'com', red: voltsJack(m).id }, t));
</script>

<MultimeterMeter
  {meterId}
  {dial}
  black="com"
  red={voltsJack(m).id}
  {reading}
  note={{ tone: reading.tone, title: reading.title, body: reading.message }}
  onmeter={(id) => {
    meterId = id;
    dial = 'off';
  }}
  ondial={(id) => (dial = id)}
>
  <TargetPicker label="Touch the probes to" targets={CONTINUITY_TARGETS} bind:value={target} />
</MultimeterMeter>
