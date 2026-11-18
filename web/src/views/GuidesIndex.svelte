<script lang="ts">
  // /guides -- every short topic guide, grouped into its set (LEDs,
  // Multimeter, Soldering), plus the full workshop guides. The filter at the
  // top narrows the page to one set via ?set=<id> (lib/guideSets.ts).
  import { currentRoute } from '../lib/router';
  import TerminalPanel from '../lib/TerminalPanel.svelte';
  import Prompt from '../lib/Prompt.svelte';
  import { TOPICS } from '../lib/topics/topics';
  import { GUIDE_SETS, GUIDE_THUMB, WORKSHOP_GUIDES, guideThumbPath } from '../lib/guides';
  import { ALL, countsBySet, groupBySet, hrefForSet, setFromQuery } from '../lib/guideSets';

  const filter = $derived(setFromQuery($currentRoute.query?.get('set')));
  const groups = $derived(groupBySet(TOPICS, filter));
  const counts = countsBySet(TOPICS);
  const workshops = $derived(WORKSHOP_GUIDES.filter((g) => filter === ALL || g.set === filter));
  const choices = [{ id: ALL, title: 'All' }, ...GUIDE_SETS];
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

  <nav class="filter" aria-label="Filter guides">
    {#each choices as c (c.id)}
      <a href={hrefForSet(c.id)} aria-current={filter === c.id ? 'page' : undefined} class:on={filter === c.id}>
        {c.title} <span class="count">{counts[c.id]}</span>
      </a>
    {/each}
  </nav>

  {#each groups as g (g.set.id)}
    <section class="set" aria-labelledby="set-{g.set.id}">
      <h2 id="set-{g.set.id}">{g.set.title}</h2>
      <p class="text-muted">{g.set.blurb}</p>
      <ul class="cards">
        {#each g.guides as t (t.slug)}
          <li>
            <a class="card" href="/guides/{t.slug}">
              <img
                class="thumb"
                src={guideThumbPath(t.slug)}
                alt=""
                width={GUIDE_THUMB.width}
                height={GUIDE_THUMB.height}
                loading="lazy"
                decoding="async"
              />
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
    </section>
  {/each}

  {#if workshops.length > 0}
  <section class="workshops">
    <h2>Workshop guides</h2>
    <p class="text-muted">Step-by-step companions to our in-person workshops.</p>
    {#each workshops as g (g.slug)}
      <a class="card wide" href="/workshops/{g.slug}">
        <img
                class="thumb"
                src={guideThumbPath(g.slug)}
                alt=""
                width={GUIDE_THUMB.width}
                height={GUIDE_THUMB.height}
                loading="lazy"
                decoding="async"
              />
        <span class="tags">{#each g.tags as tag (tag)}<span>{tag}</span>{/each}</span>
        <strong>{g.title}</strong>
        <span class="hook">{g.hook}</span>
      </a>
    {/each}
  </section>
  {/if}
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
  .filter {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }
  .filter a {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    padding: 6px 14px;
    border-radius: 999px;
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    text-decoration: none;
    font-size: var(--fs-base);
  }
  .filter a:hover {
    border-color: var(--accent);
  }
  .filter a.on {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .filter a:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
  .count {
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    opacity: 0.75;
  }
  .set h2,
  .workshops h2 {
    margin: 0 0 var(--space-1);
    font-size: var(--fs-xl);
  }
  .set > p {
    margin: 0 0 var(--space-3);
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
  .thumb {
    display: block;
    width: 100%;
    height: auto;
    aspect-ratio: 640 / 336;
    border-radius: 4px;
    background: var(--guide-pcb);
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
  .workshops p {
    margin: 0 0 var(--space-3);
  }
  .wide {
    max-width: 640px;
  }
</style>
