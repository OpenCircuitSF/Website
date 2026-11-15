<script lang="ts">
  // Spot the bad joint: one cross-section at a time, name what you see, then
  // read why it happens and how to fix it. Joint data lives in soldering.ts.
  import { tick } from 'svelte';
  import GuideArt from './GuideArt.svelte';
  import { JOINT_TYPES, QUIZ_ORDER, jointTypeById, type JointTypeId } from './soldering';

  let round = $state(0);
  let picked = $state<JointTypeId | null>(null);
  let score = $state(0);
  let choicesEl = $state<HTMLDivElement>();

  const done = $derived(round >= QUIZ_ORDER.length);
  const current = $derived(done ? null : jointTypeById(QUIZ_ORDER[round]));
  const right = $derived(current !== null && picked === current.id);
  const announcement = $derived(
    done
      ? `Score: ${score} of ${QUIZ_ORDER.length}.`
      : current && picked !== null
        ? `${right ? 'Correct!' : `Not quite. This one is: ${current.name}.`} ${current.fix}`
        : '',
  );

  function pick(id: JointTypeId) {
    if (picked !== null || !current) return;
    picked = id;
    if (id === current.id) score += 1;
  }
  function next() {
    round += 1;
    picked = null;
    // The Next button just disappeared; put focus on the new round's first choice.
    tick().then(() => choicesEl?.querySelector('button')?.focus());
  }
  function restart() {
    round = 0;
    picked = null;
    score = 0;
  }
</script>

<div class="quiz">
  {#if current}
    <p class="progress">Joint {round + 1} of {QUIZ_ORDER.length} · Score {score}</p>
    <div class="layout">
      <!-- Until answered, the alt text describes the joint without naming it. -->
      <GuideArt art={picked === null ? { ...current.art, alt: `A solder joint in cross-section. ${current.looks}` } : current.art} />
      <div>
        <div class="choices" role="group" aria-label="What is this joint?" bind:this={choicesEl}>
          {#each JOINT_TYPES as j (j.id)}
            <button
              type="button"
              class:correct={picked !== null && j.id === current.id}
              class:wrong={picked === j.id && j.id !== current.id}
              aria-disabled={picked !== null}
              onclick={() => pick(j.id)}>{j.name}</button
            >
          {/each}
        </div>
        {#if picked !== null}
          <div class="answer" class:right>
            <p class="verdict">{right ? 'Correct!' : `Not quite. This one is: ${current.name}.`}</p>
            <p><strong>Looks:</strong> {current.looks}</p>
            <p><strong>{current.good ? 'Fix:' : 'Cause and fix:'}</strong> {current.fix}</p>
            <button type="button" class="next" onclick={next}>{round + 1 < QUIZ_ORDER.length ? 'Next joint →' : 'See score'}</button>
          </div>
        {/if}
      </div>
    </div>
  {:else}
    <div class="end">
      <p class="big">{score} / {QUIZ_ORDER.length}</p>
      <p>{score === QUIZ_ORDER.length ? 'A sharp eye. You’re ready to inspect your own joints.' : 'Look at your own joints with the same eye: shape first, then shine.'}</p>
      <button type="button" class="next" onclick={restart}>Try again</button>
    </div>
  {/if}
  <p class="sr-only" aria-live="polite">{announcement}</p>
</div>

<style>
  .progress {
    margin: 0 0 var(--space-3);
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    color: var(--text-faint);
  }
  .layout {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: var(--space-5);
    align-items: start;
  }
  .choices {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }
  .choices button,
  .next {
    font: inherit;
    font-size: var(--fs-base);
    padding: 4px 12px;
    border-radius: 999px;
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  .choices button[aria-disabled='true'] {
    cursor: default;
  }
  .choices button.correct {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .choices button.wrong {
    border-color: var(--danger);
    color: var(--danger);
    text-decoration: line-through;
  }
  .answer {
    margin-top: var(--space-4);
    padding: var(--space-3) var(--space-4);
    border-left: 4px solid var(--danger);
    background: var(--bg-panel);
  }
  .answer.right {
    border-left-color: var(--accent);
  }
  .answer p {
    margin: 0 0 var(--space-2);
    font-size: var(--fs-md);
  }
  .verdict {
    font-weight: 700;
  }
  .next {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .end {
    text-align: center;
  }
  .big {
    margin: 0;
    font-family: var(--font-mono);
    font-size: clamp(32px, 7vw, 48px);
    font-weight: 700;
  }
</style>
