<script lang="ts">
  // Multimeter guide 1 (#0666): which jack each probe goes in. Pick a meter,
  // then click a jack for black, then for red.
  import MultimeterMeter from './MultimeterMeter.svelte';
  import { METERS, probeFeedback, type Feedback, type MeterId } from './multimeter';

  let meterId = $state<MeterId>('a');
  let black = $state<string | null>(null);
  let red = $state<string | null>(null);
  let fb = $state<Feedback | null>(null);

  const m = $derived(METERS[meterId]);
  const step = $derived(black === null ? 'black' : red === null ? 'red' : 'done');

  function reset() {
    black = null;
    red = null;
    fb = null;
  }
  function pickMeter(id: MeterId) {
    meterId = id;
    reset();
  }
  function plug(id: string) {
    if (step === 'done') return;
    const f = probeFeedback(m, step, id, black);
    fb = f;
    if (!f.ok) return;
    if (step === 'black') black = id;
    else red = id;
  }
</script>

<MultimeterMeter
  {meterId}
  dial="off"
  {black}
  {red}
  reading={null}
  note={fb ? { tone: fb.ok ? 'ok' : 'warn', title: fb.title, body: fb.body } : null}
  onmeter={pickMeter}
  onjack={plug}
>
  <ol class="steps">
    <li class:now={step === 'black'} class:done={black !== null}>Click the jack for the <strong>black</strong> probe.</li>
    <li class:now={step === 'red'} class:done={red !== null}>Click the jack for the <strong>red</strong> probe.</li>
  </ol>
  {#if step === 'done'}
    <p class="done-msg">
      Set up for volts, resistance and continuity. Only current needs red somewhere else, and that has its own guide.
    </p>
  {/if}
  {#if black !== null || fb}
    <button type="button" class="again" onclick={reset}>Start over</button>
  {/if}
</MultimeterMeter>

<style>
  .steps {
    margin: 0;
    padding-left: var(--space-5);
    font-size: var(--fs-md);
  }
  .steps li {
    color: var(--text-muted);
    margin-bottom: var(--space-1);
  }
  .steps li.now {
    color: var(--text);
  }
  .steps li.done {
    text-decoration: line-through;
  }
  .done-msg {
    margin: 0;
    font-size: var(--fs-md);
    color: var(--success);
    font-weight: 600;
  }
  .again {
    align-self: flex-start;
    font: inherit;
    font-size: var(--fs-base);
    padding: var(--space-2) var(--space-3);
    border: var(--border-w) solid var(--border-strong);
    border-radius: var(--radius);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
</style>
