<script lang="ts">
  // /guides -- every short topic guide, plus the full workshop guides.
  import TerminalPanel from '../lib/TerminalPanel.svelte';
  import Prompt from '../lib/Prompt.svelte';
  import MiniStrip from '../lib/topics/MiniStrip.svelte';
  import { TOPICS } from '../lib/topics/topics';
</script>

<main id="main-content" class="app-shell guides">
  <TerminalPanel title="guides // open_circuit_sf">
    <Prompt text="ls guides/" />
    <h1 tabindex="-1">Guides</h1>
    <p class="lede">
      Short, hands-on explainers. Each one is a single idea with something to press, drag or break. A few minutes each,
      no experience needed.
    </p>
  </TerminalPanel>

  <ul class="cards">
    {#each TOPICS as t, i (t.slug)}
      <li>
        <a class="card" href="/guides/{t.slug}">
          <MiniStrip seed={i} />
          <span class="tags">
            {#each t.tags as tag (tag)}<span>{tag}</span>{/each}
            <span>{t.minutes} min</span>
          </span>
          <strong>{t.title}</strong>
          <span class="hook">{t.hook}</span>
        </a>
      </li>
    {/each}
  </ul>

  <section class="workshops">
    <h2>Workshop guides</h2>
    <p class="text-muted">Step-by-step companions to our in-person workshops.</p>
    <a class="card wide" href="/workshops/programming-leds">
      <MiniStrip seed={7} count={10} />
      <span class="tags"><span>workshop</span><span>60 min</span><span>beginner</span></span>
      <strong>Programming LEDs</strong>
      <span class="hook">Flash an ESP32-C3, light up an LED strip, then change the code and make it do something new.</span>
    </a>
  </section>
</main>

<style>
  .guides {
    display: grid;
    gap: var(--space-5);
  }
  h1 {
    margin: var(--space-3) 0 var(--space-2);
    font-size: clamp(28px, 6vw, 44px);
  }
  .lede {
    margin: 0;
    font-size: var(--fs-lg);
    color: var(--text-muted);
    max-width: var(--measure);
  }
  .cards {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
    gap: var(--space-4);
  }
  .card {
    display: grid;
    align-content: start;
    gap: var(--space-2);
    height: 100%;
    padding: var(--space-4);
    border: var(--border-w) solid var(--border);
    border-radius: 8px;
    background: var(--bg-panel);
    color: var(--text);
    text-decoration: none;
    transition: border-color 0.15s, transform 0.15s;
  }
  .card:hover {
    border-color: var(--accent);
    transform: translateY(-2px);
  }
  @media (prefers-reduced-motion: reduce) {
    .card:hover {
      transform: none;
    }
  }
  .card strong {
    font-size: var(--fs-lg);
    line-height: 1.25;
  }
  .tags {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1);
  }
  .tags span {
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    padding: 1px 8px;
    border-radius: 999px;
    background: var(--accent-subtle);
    color: var(--accent);
  }
  .hook {
    font-size: var(--fs-base);
    color: var(--text-muted);
  }
  .workshops h2 {
    margin: 0 0 var(--space-1);
    font-size: var(--fs-xl);
  }
  .workshops p {
    margin: 0 0 var(--space-3);
  }
  .wide {
    max-width: 640px;
  }
</style>
