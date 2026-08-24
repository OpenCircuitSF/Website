<script lang="ts">
  // Home view (#0015, PRD §4.1/§5.1/§7.3): the landing page every social
  // link points at. Composition follows PRD §4.1's reference layout exactly
  // -- hero terminal block, a short "what this is" statement, next-up
  // workshops, inline subscribe CTA -- rather than inventing a new
  // arrangement. Copy is adapted from placeholder/index.html (the validated
  // design reference already using this exact language, including the
  // venue-independence framing).
  //
  // #0231 added a photo of the physical CRT monitor the terminal motif
  // refers to, inside the terminal panel itself: the prompt/headline/status/
  // command-line block on the left, the CRT on the right, both inside the
  // same bordered terminal-body -- not a separate element beside the panel.
  // At >=860px that split is a two-column grid; below it, the image stacks
  // beneath the text at a smaller size rather than disappearing, since the
  // <img> always carries a real `src` (a bare `<img>` with no src is broken
  // markup) and the byte cost is paid either way. At >=860px the image is
  // large enough to become the LCP candidate, displacing the headline; see
  // the issue's Implementation notes for the measured before/after numbers
  // and why that still clears PRD §7.6's <2.0s-on-4G budget.
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
      <div class="hero-row">
        <div class="hero-text">
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
        </div>

        <div class="hero-crt">
          <div class="crt-frame">
            <picture>
              <source media="(min-width: 860px)" type="image/webp" srcset="/hero-crt-380.webp 1x, /hero-crt-760.webp 2x" />
              <img
                class="crt-photo"
                src="/hero-crt-380.webp"
                alt=""
                width="380"
                height="354"
                decoding="async"
              />
            </picture>
          </div>
        </div>
      </div>
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

  /* #0231: the CRT photo lives inside the terminal panel's own body, not
   * beside it -- .hero-row is what .terminal-body's single child renders,
   * splitting into text (left) and photo (right). Below 860px it stacks:
   * text first, then the photo at a smaller size. It is never removed from
   * the DOM at any width, because the <img> always carries a real `src`
   * (see the script comment) -- a stacked, smaller photo costs no more
   * bytes than a hidden one would, so hiding it outright would only waste
   * the download for nothing. */
  /* #0231: the hero panel is pinned to the CRT photo's own backdrop
   * (#181d17, measured uniform at every corner and mid-edge) in BOTH themes,
   * so the photo's background IS the section's background and no seam,
   * frame, or border is possible. The photo therefore ships with no alpha
   * channel at all -- a flat backdrop compresses to 3.8 KB where the keyed
   * cutout cost 37 KB, and the cutout could never be clean anyway: the
   * monitor's dark right flank and bottom shadow are indistinguishable from
   * the backdrop in the source photograph.
   *
   * Pinning the surface means pinning the text tokens with it, or light mode
   * would render dark type on a dark panel. These are the dark palette's
   * values from app.css, scoped to this panel only -- the rest of the page
   * still follows the theme. */
  .hero :global(.terminal-panel),
  .hero :global(.terminal-body) {
    background: #181d17;
  }

  .hero :global(.terminal-bar) {
    background: #1b231c;
    border-bottom-color: #243028;
  }

  .hero {
    --bg-panel: #181d17;
    --bg-header: #1b231c;
    --text: #e8f0e8;
    --text-muted: #9aa79e;
    --text-faint: #7e8d82;
    --border: #243028;
    --border-strong: #4e6553;
    --accent: #68ff23;
    --accent-dim: #3c9e0f;
    color: var(--text);
  }

  .hero-row {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--space-5);
  }

  .hero-text {
    width: 100%;
  }

  .hero-crt {
    display: flex;
    justify-content: center;
  }

  /* The photo's backdrop measures a uniform #181d17 (issue #0231's
   * Description) at every corner and mid-edge. Verified against both
   * surfaces it can now land on: TerminalPanel's own --bg-panel is #FFFFFF
   * in light mode (the cutout ghosts badly there, matching the same
   * problem the page ground had) and #121712 in dark mode (close enough
   * that compositing directly onto it showed no visible seam -- but that
   * near-match is a coincidence of the current dark palette, not something
   * to depend on, and light mode still needs a real fix). Rather than
   * branch the treatment per theme, the frame stays a single fixed dark
   * surface in both -- like a CRT sitting in a dim alcove built into the
   * terminal chrome -- so there is exactly one asset and one background
   * value, not a per-theme swap. That is why it is a literal hex value
   * here rather than a token: the whole point is that it must NOT change
   * with the theme. --border still comes from the token, same as every
   * other panel on this page. */
  .crt-frame {
    display: flex;
    width: 240px;
  }

  .crt-photo {
    display: block;
    width: 100%;
    height: auto;
  }

  @media (min-width: 860px) {
    .hero-row {
      flex-direction: row;
      align-items: center;
      gap: var(--space-6);
    }

    .hero-text {
      flex: 1 1 auto;
      min-width: 0;
    }

    .hero-crt {
      flex: none;
    }

    .crt-frame {
      width: 380px;
    }
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
