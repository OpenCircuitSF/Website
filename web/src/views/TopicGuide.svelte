<script lang="ts">
  // /guides/{slug} -- one short topic guide. Static and client-side; the
  // content and demo come from lib/topics/topics.ts. router.ts only routes
  // slugs in TOPIC_GUIDE_SLUGS here, so `topic` is always found; the
  // not-found branch is a guard, not a real state.
  import { currentRoute } from '../lib/router';
  import TerminalPanel from '../lib/TerminalPanel.svelte';
  import Prompt from '../lib/Prompt.svelte';
  import TraceDivider from '../lib/TraceDivider.svelte';
  import GuideEnd from '../lib/GuideEnd.svelte';
  import ShareBar from '../lib/topics/ShareBar.svelte';
  import MiniStrip from '../lib/topics/MiniStrip.svelte';
  import { TOPICS, topicBySlug } from '../lib/topics/topics';

  const topic = $derived(topicBySlug($currentRoute.params.slug ?? ''));
  const index = $derived(topic ? TOPICS.indexOf(topic) : 0);
  const others = $derived(TOPICS.filter((t) => t !== topic).slice(0, 3));
</script>

{#if topic}
  {@const Demo = topic.demo}
  <main id="main-content" class="app-shell topic">
    <TerminalPanel title="guides // {topic.slug.replaceAll('-', '_')}">
      <Prompt text="guide --time {topic.minutes}m" />
      <h1 tabindex="-1">{topic.title}</h1>
      <p class="hook">{topic.hook}</p>
      <div class="meta">
        <span class="chip">{topic.minutes} min</span>
        {#each topic.tags as t (t)}<span class="chip">{t}</span>{/each}
      </div>
      <ShareBar path="/guides/{topic.slug}" />
    </TerminalPanel>

    <section class="demo" aria-labelledby="try-it">
      <p class="eyebrow" id="try-it">Try it</p>
      <p class="try">{topic.tryIt}</p>
      <div class="demo-frame">
        <Demo />
      </div>
    </section>

    <section class="ideas" aria-label="What's going on">
      {#each topic.ideas as idea, i (i)}
        <article class="idea">
          <span class="num" aria-hidden="true">{i + 1}</span>
          <h2>{idea.title}</h2>
          <p>{idea.body}</p>
        </article>
      {/each}
    </section>

    <aside class="takeaway">
      <span class="eyebrow">Remember</span>
      <p>{topic.takeaway}</p>
    </aside>

    <section class="cta">
      <MiniStrip seed={index} count={8} />
      <div>
        <h2>Try it with real hardware</h2>
        <p>
          In our Programming LEDs workshop you flash an ESP32-C3 and light up a real LED strip. No experience needed;
          we bring the parts.
        </p>
        <p class="links">
          <a href="/workshops/programming-leds">Workshop guide →</a>
          <a href="/events?type=workshop">Upcoming sessions →</a>
        </p>
      </div>
    </section>

    {#key topic.slug}
      <GuideEnd kind="topic" slug={topic.slug} />
    {/key}

    <TraceDivider />

    <section aria-labelledby="more">
      <h2 id="more" class="more-h">More guides</h2>
      <ul class="more">
        {#each others as t (t.slug)}
          <li>
            <a href="/guides/{t.slug}">
              <strong>{t.title}</strong>
              <span>{t.hook}</span>
            </a>
          </li>
        {/each}
      </ul>
      <p><a href="/guides">All guides →</a></p>
    </section>

    <ShareBar path="/guides/{topic.slug}" />
  </main>
{/if}

<style>
  .topic {
    display: grid;
    gap: var(--space-5);
  }
  h1 {
    margin: var(--space-3) 0 var(--space-2);
    font-size: clamp(26px, 5.5vw, 40px);
    line-height: 1.12;
  }
  .hook {
    margin: 0 0 var(--space-3);
    font-size: var(--fs-lg);
    color: var(--text-muted);
    max-width: var(--measure);
  }
  .meta {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin-bottom: var(--space-4);
  }
  .chip {
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    padding: 2px 10px;
    border: var(--border-w) solid var(--border);
    border-radius: 999px;
    color: var(--text-muted);
  }
  .eyebrow {
    margin: 0;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--accent);
  }
  .try {
    margin: var(--space-1) 0 var(--space-3);
    font-size: var(--fs-lg);
    font-weight: 600;
    max-width: var(--measure);
  }
  .demo-frame {
    padding: var(--space-4);
    border: 2px solid var(--accent-dim);
    border-radius: 8px;
    background: var(--bg-panel);
  }
  .ideas {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
    gap: var(--space-4);
  }
  .idea {
    position: relative;
    padding: var(--space-4);
    padding-top: var(--space-5);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
    background: var(--bg-panel);
  }
  .num {
    position: absolute;
    top: -14px;
    left: var(--space-4);
    width: 28px;
    height: 28px;
    display: grid;
    place-items: center;
    border-radius: 50%;
    background: var(--accent);
    color: var(--accent-text);
    font-family: var(--font-mono);
    font-weight: 700;
  }
  .idea h2 {
    margin: 0 0 var(--space-2);
    font-size: var(--fs-lg);
  }
  .idea p {
    margin: 0;
    font-size: var(--fs-md);
    color: var(--text-muted);
  }
  .takeaway {
    padding: var(--space-4) var(--space-5);
    border-left: 6px solid var(--accent);
    background: var(--accent-subtle);
    border-radius: 0 6px 6px 0;
  }
  .takeaway p {
    margin: var(--space-1) 0 0;
    font-size: var(--fs-xl);
    font-weight: 700;
    line-height: 1.3;
  }
  .cta {
    display: grid;
    gap: var(--space-3);
    padding: var(--space-4);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
    background: var(--bg-panel);
  }
  .cta h2 {
    margin: 0 0 var(--space-1);
    font-size: var(--fs-lg);
  }
  .cta p {
    margin: 0 0 var(--space-2);
    font-size: var(--fs-md);
    color: var(--text-muted);
    max-width: var(--measure);
  }
  .links {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-4);
    font-weight: 600;
  }
  .more-h {
    margin: 0 0 var(--space-3);
    font-size: var(--fs-xl);
  }
  .more {
    list-style: none;
    margin: 0 0 var(--space-3);
    padding: 0;
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
    gap: var(--space-3);
  }
  .more a {
    display: grid;
    gap: var(--space-1);
    height: 100%;
    padding: var(--space-3);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
    background: var(--bg-panel);
    color: var(--text);
    text-decoration: none;
  }
  .more a:hover {
    border-color: var(--accent);
  }
  .more span {
    font-size: var(--fs-base);
    color: var(--text-muted);
  }
</style>
