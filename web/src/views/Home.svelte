<script lang="ts">
  // Home view (#0015, PRD §4.1/§5.1/§7.3): the landing page every social
  // link points at. Composition follows PRD §4.1's reference layout exactly
  // -- hero terminal block, a short "what this is" statement, next-up
  // workshops, inline subscribe CTA -- rather than inventing a new
  // arrangement. Copy is adapted from placeholder/index.html (the validated
  // design reference already using this exact language, including the
  // venue-independence framing).
  //
  // The headline is the page's LCP element by construction: no images in the
  // hero, just type -- see PRD §7.6's LCP budget (<2.0s on 4G).
  //
  // "Next up" (#0053): switched from a static placeholder to the real
  // workshops API. GET /api/workshops (#0051) returns upcoming workshops
  // already in chronological order; lib/workshops.ts's nextWorkshops caps
  // that list at three, per this view's original acceptance criteria, and
  // WorkshopCard is the same card component WorkshopsIndex.svelte uses so
  // Home and /workshops render a next-up workshop identically.
  import { onMount } from 'svelte';
  import TerminalPanel from '../lib/TerminalPanel.svelte';
  import Prompt from '../lib/Prompt.svelte';
  import StatusList from '../lib/StatusList.svelte';
  import CommandLine from '../lib/CommandLine.svelte';
  import TraceDivider from '../lib/TraceDivider.svelte';
  import Panel from '../lib/Panel.svelte';
  import WorkshopCard from '../lib/WorkshopCard.svelte';
  import SubscribeForm from '../lib/SubscribeForm.svelte';
  import { listPublicWorkshops } from '../lib/api';
  import { nextWorkshops } from '../lib/workshops';
  import { APP_NAME } from '../lib/branding';
  import type { PublicWorkshop } from '../lib/types';

  type UpcomingStatus = 'loading' | 'loaded' | 'error';

  let upcomingStatus = $state<UpcomingStatus>('loading');
  let upcoming = $state<PublicWorkshop[]>([]);

  onMount(() => {
    listPublicWorkshops()
      .then((res) => {
        upcoming = nextWorkshops(res);
        upcomingStatus = 'loaded';
      })
      .catch(() => {
        upcomingStatus = 'error';
      });
  });
</script>

<main id="main-content" class="app-shell home-shell">
  <section class="hero" aria-labelledby="home-headline">
    <TerminalPanel title="open_circuit_sf">
      <Prompt text="open_circuit // san_francisco" />
      <h1 id="home-headline" class="headline">Hands-on electronics workshops</h1>
      <StatusList
        items={[
          'absolute beginners welcome',
          'tools and parts provided',
          'hosted anywhere — makerspace to neighborhood garage',
          'microcontrollers · soldering · homelab · automation',
        ]}
      />
      <CommandLine command="opencircuitsf.com" />
    </TerminalPanel>
  </section>

  <TraceDivider />

  <section class="lede" aria-labelledby="what-h">
    <h2 id="what-h">What this is</h2>
    <p>
      <strong>{APP_NAME}</strong> is a San Francisco group running hands-on electronics
      workshops — soldering, microcontrollers, homelab, home automation, and whatever
      the room wants to build next.
    </p>
    <p>
      We are deliberately venue-independent. A workshop might run in a makerspace, a
      co-working room, or somebody's garage. Bring curiosity; we bring the tools.
    </p>
  </section>

  <TraceDivider />

  <section class="next-up" aria-labelledby="next-up-h">
    <h2 id="next-up-h">Next up</h2>
    {#if upcomingStatus === 'loading'}
      <p class="text-muted">Loading…</p>
    {:else if upcomingStatus === 'error'}
      <p class="text-error">Couldn't load upcoming workshops right now.</p>
    {:else if upcoming.length === 0}
      <p class="text-muted">Nothing scheduled yet — check back soon, or subscribe below.</p>
    {:else}
      <div class="workshop-cards">
        {#each upcoming as w (w.slug)}
          <WorkshopCard workshop={w} />
        {/each}
      </div>
    {/if}
    <p class="text-muted next-up-note"><a href="/workshops">See all workshops</a></p>
  </section>

  <TraceDivider />

  <section class="subscribe-cta" aria-labelledby="subscribe-h">
    <h2 id="subscribe-h">Stay in the loop</h2>
    <Panel>
      <p>Get notified about new workshops. Pick the topics you care about — nothing else.</p>
      <SubscribeForm />
    </Panel>
  </section>
</main>

<style>
  .home-shell {
    display: flex;
    flex-direction: column;
    gap: var(--space-6);
  }

  .headline {
    margin: var(--space-3) 0 var(--space-5);
    font-family: var(--font);
    font-weight: 800;
    text-transform: uppercase;
    letter-spacing: 0.01em;
    line-height: 1.05;
    /* Scales from a readable minimum on a 375px phone up to a strong display
     * size on desktop, without ever forcing horizontal scroll. */
    font-size: clamp(28px, 7vw, 52px);
    color: var(--text);
  }

  .lede h2,
  .next-up h2,
  .subscribe-cta h2 {
    font-size: var(--fs-lg);
    margin: 0 0 var(--space-3);
  }

  .lede p {
    max-width: var(--measure);
    margin: 0 0 var(--space-3);
  }
  .lede p:last-child {
    margin-bottom: 0;
  }

  .workshop-cards {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
    gap: var(--space-4);
  }

  .next-up-note {
    margin: var(--space-3) 0 0;
  }
</style>
