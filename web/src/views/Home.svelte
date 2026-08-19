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
  import TerminalPanel from '../lib/TerminalPanel.svelte';
  import Prompt from '../lib/Prompt.svelte';
  import StatusList from '../lib/StatusList.svelte';
  import CommandLine from '../lib/CommandLine.svelte';
  import TraceDivider from '../lib/TraceDivider.svelte';
  import Panel from '../lib/Panel.svelte';
  import { APP_NAME } from '../lib/branding';

  // Static placeholder for "next up" until #0053 wires the real workshops
  // API. Deliberately generic -- no invented dates or locations that could
  // read as a real scheduled event -- since there is no backing data source
  // yet. Capped at three per the acceptance criteria.
  interface UpcomingPlaceholder {
    title: string;
    blurb: string;
  }

  const UPCOMING: readonly UpcomingPlaceholder[] = [
    { title: 'Soldering fundamentals', blurb: 'Beginner-friendly. Tools and kit provided.' },
    { title: 'Microcontroller basics', blurb: 'ESP32 and Arduino, from blink to first sensor.' },
    { title: 'Home automation night', blurb: 'Bring a project, or start one from scratch.' },
  ];
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
    <div class="workshop-cards">
      {#each UPCOMING as w (w.title)}
        <Panel title={w.title}>
          <p class="workshop-blurb">{w.blurb}</p>
        </Panel>
      {/each}
    </div>
    <p class="text-muted next-up-note">
      Full workshop listings with dates and locations are on the way.
    </p>
  </section>

  <TraceDivider />

  <section class="subscribe-cta" aria-labelledby="subscribe-h">
    <h2 id="subscribe-h">Stay in the loop</h2>
    <!-- SubscribeForm.svelte lands in #0029; this is a placeholder region so
         the layout and section anchor exist ahead of that component. -->
    <Panel>
      <p>Get notified about new workshops. Pick the topics you care about — nothing else.</p>
      <p class="text-muted">Sign-up form is on the way.</p>
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

  .workshop-blurb {
    margin: 0;
    color: var(--text-muted);
  }

  .next-up-note {
    margin: var(--space-3) 0 0;
  }
</style>
