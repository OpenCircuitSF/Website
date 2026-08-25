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
  // At >=860px that split is a flex row; below it, the image stacks
  // beneath the text at a smaller size rather than disappearing.
  //
  // The headline remains the LCP element -- measured on the shipped build
  // with a buffered largest-contentful-paint observer, at 1440px (h1,
  // 65,727 px2) and 390px (h1, 19,089 px2); .crt-frame never appears as a
  // candidate. An earlier revision of this comment said the image displaced
  // the headline. That was measured against a different build, when the
  // photo was an <img>; a CSS background is not the same LCP question, and
  // the claim was carried forward rather than re-measured. Note that
  // background-image LCP candidacy is engine-specific, and this figure is
  // from WebKit only.
  //
  // The photo is a CSS background rather than an <img> because it must swap
  // per theme, and a <picture> cannot: `<source media="(prefers-color-
  // scheme: dark)">` reads only the OS preference, so the explicit
  // light/dark toggle in the header (lib/theme.ts writes `data-theme` on
  // <html>) would leave a dark photo on a light panel. A background driven
  // by a custom property answers to both signals and still downloads only
  // the one asset that matches -- two <img>s toggled by `display` would
  // fetch both. The photo is decorative (it repeats what the adjacent
  // headline and command line already say), so it carried alt="" as an
  // <img> and needs no text alternative as a background.
  //
  // "Next up" (#0053): switched from a static placeholder to the real
  // workshops API. GET /api/workshops (#0051) returns upcoming workshops
  // already in chronological order; lib/workshops.ts's nextWorkshops caps
  // that list at three, per this view's original acceptance criteria, and
  // WorkshopCard is the same card component WorkshopsIndex.svelte uses so
  // Home and /workshops render a next-up workshop identically.
  import { onMount } from 'svelte';
  import {
    CRT_SRC_W,
    CRT_SRC_H,
    CRT_SESSION,
    crtMatrix3d,
    visibleLines,
  } from '../lib/crtScreen';
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

  let crtFrame: HTMLDivElement | undefined = $state();
  let screenPlane: HTMLDivElement | undefined = $state();
  let termCanvas: HTMLCanvasElement | undefined = $state();
  let glowCanvas: HTMLCanvasElement | undefined = $state();

  // #0270. The screen is decorative, so every cost it adds must be optional:
  // it stops entirely under prefers-reduced-motion, when the tab is hidden,
  // and when the hero scrolls out of view. #0233 measured ~11 canvas repaints
  // a second while visible and 0 while hidden; the scroll case was the gap
  // that measurement exposed, and IntersectionObserver closes it.
  onMount(() => {
    if (!termCanvas || !glowCanvas || !crtFrame || !screenPlane) return;
    const term: HTMLCanvasElement = termCanvas;
    const glow: HTMLCanvasElement = glowCanvas;
    const frame: HTMLDivElement = crtFrame;
    const plane: HTMLDivElement = screenPlane;
    const ctx0 = term.getContext('2d');
    const gctx0 = glow.getContext('2d');
    if (!ctx0 || !gctx0) return;
    // Annotated rather than inferred: paint() is a hoisted function
    // declaration, so TypeScript will not carry the null-narrowing above into
    // it on its own.
    const ctx: CanvasRenderingContext2D = ctx0;
    const gctx: CanvasRenderingContext2D = gctx0;

    const PHOS = '#3dff86';
    const HOT = '#d5ffe2';
    const FONT_PX = 26;
    const LINE_H = 33;
    const LEFT = 40;
    const TOP = 38;
    const PAUSE_MS = 3000;

    const reduce = window.matchMedia('(prefers-reduced-motion: reduce)');
    let lines: string[] = [];
    let cursorOn = true;
    let generation = 0;
    let onScreen = true;
    let timers: ReturnType<typeof setTimeout>[] = [];
    let blink: ReturnType<typeof setInterval> | undefined;

    const wait = (ms: number) => new Promise<void>((r) => { timers.push(setTimeout(r, ms)); });
    const idle = () => document.hidden || !onScreen;

    function fit() {
      plane.style.transform = crtMatrix3d(frame.clientWidth, frame.clientHeight);
    }

    function paint() {
      if (idle()) return;
      ctx.setTransform(1, 0, 0, 1, 0, 0);
      ctx.globalAlpha = 1;
      ctx.globalCompositeOperation = 'source-over';
      ctx.shadowBlur = 0;
      // Cleared, not filled: the photograph's own screen -- texture, curvature,
      // corner falloff -- shows through, and only the glyphs are drawn on top.
      ctx.clearRect(0, 0, CRT_SRC_W, CRT_SRC_H);
      ctx.textBaseline = 'top';
      ctx.font = FONT_PX + "px 'JetBrains Mono', ui-monospace, Menlo, monospace";
      ctx.fillStyle = PHOS;
      ctx.shadowColor = PHOS;
      ctx.shadowBlur = 3;
      const vis = visibleLines(lines);
      vis.forEach((l, i) => ctx.fillText(l, LEFT, TOP + i * LINE_H));
      if (cursorOn && vis.length) {
        const last = vis[vis.length - 1];
        ctx.save();
        ctx.fillStyle = HOT;
        ctx.shadowColor = PHOS;
        ctx.shadowBlur = 9;
        ctx.fillRect(LEFT + ctx.measureText(last).width + 4, TOP + (vis.length - 1) * LINE_H + 3, 13, FONT_PX - 3);
        ctx.restore();
      }
      gctx.setTransform(1, 0, 0, 1, 0, 0);
      gctx.globalCompositeOperation = 'source-over';
      gctx.clearRect(0, 0, CRT_SRC_W, CRT_SRC_H);
      gctx.drawImage(term, 0, 0);
    }

    const push = (t: string) => { lines.push(t); paint(); };

    async function typeLine(gen: number, text: string) {
      lines.push('');
      for (const ch of text) {
        if (gen !== generation) return false;
        lines[lines.length - 1] += ch;
        paint();
        await wait(42 + Math.random() * 30);
      }
      return gen === generation;
    }

    async function session() {
      const gen = ++generation;
      if (reduce.matches) {
        const step = CRT_SESSION[0];
        lines = ['open circuit sf // sf, ca', '', '> ' + step.cmd, ...step.out];
        paint();
        return;
      }
      lines = [];
      paint();
      await wait(240);
      if (gen !== generation) return;
      push('open circuit sf // sf, ca');
      await wait(300);
      push('memory ok. phosphor ok.');
      await wait(420);
      for (let i = 0; gen === generation; i++) {
        const step = CRT_SESSION[i % CRT_SESSION.length];
        push('');
        if (!(await typeLine(gen, '> ' + step.cmd))) return;
        await wait(260);
        for (const line of step.out) {
          if (gen !== generation) return;
          push(line);
          await wait(150);
        }
        await wait(PAUSE_MS);
      }
    }

    fit();
    const onResize = () => fit();
    window.addEventListener('resize', onResize);
    const onVis = () => { if (!document.hidden) paint(); };
    document.addEventListener('visibilitychange', onVis);

    const io = new IntersectionObserver((entries) => {
      onScreen = entries[0]?.isIntersecting ?? true;
      if (onScreen) paint();
    });
    io.observe(frame);

    if (!reduce.matches) {
      blink = setInterval(() => { cursorOn = !cursorOn; paint(); }, 530);
    }
    void session();

    return () => {
      generation++;
      timers.forEach(clearTimeout);
      timers = [];
      if (blink) clearInterval(blink);
      io.disconnect();
      window.removeEventListener('resize', onResize);
      document.removeEventListener('visibilitychange', onVis);
    };
  });

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
          <div class="crt-frame" bind:this={crtFrame}>
            <!-- #0270: the live screen. aria-hidden because it repeats what the
                 headline and command line beside it already say (#0233's
                 decision: illustrative copy, never the workshops API — real
                 dates would be information, and hiding information is wrong).
                 With JS off this stays empty and the photograph alone shows,
                 which is exactly #0231's correct still hero. -->
            <div class="screen-plane" bind:this={screenPlane} aria-hidden="true">
              <canvas bind:this={glowCanvas} width={CRT_SRC_W} height={CRT_SRC_H} class="crt-glow"></canvas>
              <canvas bind:this={termCanvas} width={CRT_SRC_W} height={CRT_SRC_H}></canvas>
              <div class="crt-scanlines"></div>
              <div class="crt-glass"></div>
            </div>
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
   * the DOM at any width -- a stacked, smaller photo costs no more bytes
   * than a hidden one would, so hiding it outright would only waste the
   * download for nothing. */
  /* #0231: the hero panel is pinned to the CRT photo's own backdrop, so the
   * photo's background IS the panel's background and no seam, frame, border,
   * or alpha channel is possible. The photo ships fully opaque: a flat
   * backdrop compresses to a few KB where a keyed cutout cost ten times that,
   * and the cutout could never be clean anyway -- the monitor's dark right
   * flank and bottom shadow are indistinguishable from the backdrop in the
   * source photograph, which two separate keying attempts confirmed.
   *
   * There are two photographs, one shot against a dark backdrop and one
   * against a light one, because pinning a single dark photo forced the whole
   * hero to stay dark in light mode -- one dark band across the top of an
   * otherwise pale page. Each theme pins to its own photo's backdrop instead.
   *
   * The pinned values are the colors the WebP *decodes* to, not the colors the
   * source PNGs contain: lossy encoding shifts a flat field by a step or two
   * (#E9EEE6 decodes to #E8EEE5, #181D17 to #191C17), and it is the decoded
   * value the browser paints next to the panel background. Lossless encoding
   * would hold the source values exactly but costs 262 KB against 19 KB at 2x
   * -- not worth it for a difference of one 255th. Both figures are measured;
   * re-measure with the mode of the border pixels if either asset is
   * re-encoded.
   *
   * Everything below drives TerminalPanel through the custom properties it
   * already reads (--bg-panel, --bg-header, --border), so no :global()
   * override of its internals is needed. Light is the default branch, dark is
   * applied by the same two-selector pattern app.css uses: the media query for
   * the OS preference, guarded against an explicit light choice, plus the
   * explicit dark attribute. Text tokens are NOT overridden -- each theme's
   * own palette already contrasts correctly against its own photo backdrop. */
  .hero {
    --bg-panel: #e8eee5;
    --bg-header: #dae3d6;
    --border: #c6d1c2;
    --crt-photo: url('/hero-crt-blank-light-760.webp');
    --crt-photo-set: image-set(
      url('/hero-crt-blank-light-760.webp') 1x,
      url('/hero-crt-blank-light-760.webp') 2x
    );
  }

  @media (prefers-color-scheme: dark) {
    :root:not([data-theme='light']) .hero {
      --bg-panel: #191c17;
      --bg-header: #1b231c;
      --border: #243028;
      --crt-photo: url('/hero-crt-blank-760.webp');
      --crt-photo-set: image-set(
        url('/hero-crt-blank-760.webp') 1x,
        url('/hero-crt-blank-760.webp') 2x
      );
    }
  }

  :root[data-theme='dark'] .hero {
    --bg-panel: #191c17;
    --bg-header: #1b231c;
    --border: #243028;
    --crt-photo: url('/hero-crt-blank-760.webp');
    --crt-photo-set: image-set(
      url('/hero-crt-blank-760.webp') 1x,
      url('/hero-crt-blank-760.webp') 2x
    );
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

  /* Both photographs are 1402x1122; the ratio is declared here so the frame
   * reserves its height before the background loads. A background image has
   * no intrinsic size to fall back on the way an <img> does, so without this
   * the row would collapse and then jump. The 1x/2x pair is selected by
   * image-set, opted into via @supports below. */
  .crt-frame {
    position: relative;
    width: 240px;
    aspect-ratio: 1402 / 1122;
    background-image: var(--crt-photo);
    background-size: 100% 100%;
    background-repeat: no-repeat;
  }

  /* The 2x asset is opted INTO, rather than the 1x being a fallback beneath
   * it. The usual two-declaration idiom -- plain url() then image-set(), later
   * wins where supported -- silently fails through var(): a substituted value
   * that the property cannot parse is invalid at computed-value time, which
   * makes background-image `none`, NOT the earlier declaration. Measured in
   * Chromium: through var() the computed value is "none", written directly it
   * is the url(). So an engine without image-set() got an empty box, not the
   * 1x photo the comment here used to claim. @supports asks first, so the
   * declaration is never attempted where it cannot work. Affects Safari <= 16
   * and Chrome < 113. */
  @supports (background-image: image-set(url('/hero-crt-blank-760.webp') 1x)) {
    .crt-frame {
      background-image: var(--crt-photo-set);
    }
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

  /* #0270: the live screen. An ordinary rectangle that JS warps onto the four
   * calibrated corners of the photographed glass with a projective matrix3d.
   * transform-origin MUST be 0 0 or the homography is wrong. */
  .screen-plane {
    position: absolute;
    left: 0;
    top: 0;
    width: 640px;
    height: 512px;
    transform-origin: 0 0;
    overflow: hidden;
    border-radius: 8% / 6%;
    /* No background: the canvas is cleared rather than filled, so the
     * photograph's own screen shows through and only the glyphs sit on top. */
    background: transparent;
    will-change: transform;
    pointer-events: none;
  }

  .screen-plane :global(canvas),
  .crt-scanlines,
  .crt-glass {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
  }

  .screen-plane :global(canvas) {
    display: block;
  }

  .screen-plane :global(.crt-glow) {
    filter: blur(9px);
    opacity: 0.42;
    mix-blend-mode: screen;
  }

  /* Both overlays are far lighter than they would need to be over a flat fill:
   * the photographed screen already carries its own scan texture and corner
   * falloff, and doubling either reads as moire or crushed corners. */
  .crt-scanlines {
    background: repeating-linear-gradient(to bottom, rgba(0, 0, 0, 0.16) 0 1px, transparent 1px 3px);
    opacity: 0.22;
  }

  .crt-glass {
    background:
      radial-gradient(ellipse at center, transparent 62%, rgba(0, 0, 0, 0.06) 80%, rgba(0, 0, 0, 0.26) 100%),
      linear-gradient(150deg, rgba(255, 255, 255, 0.03), transparent 26%, transparent 74%, rgba(95, 255, 145, 0.02));
  }
</style>
