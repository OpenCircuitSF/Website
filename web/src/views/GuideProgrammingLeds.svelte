<script lang="ts">
  // /workshops/programming-leds -- the student-facing companion to the
  // Programming LEDs workshop. Entirely client-side: no API calls, no
  // accounts; step progress lives in localStorage. Content is in
  // lib/guide/programmingLeds.ts, adapted from the Workshops repo.
  import TerminalPanel from '../lib/TerminalPanel.svelte';
  import Prompt from '../lib/Prompt.svelte';
  import TraceDivider from '../lib/TraceDivider.svelte';
  import LedStrip from '../lib/guide/LedStrip.svelte';
  import StepList from '../lib/guide/StepList.svelte';
  import BoardPinout from '../lib/guide/BoardPinout.svelte';
  import ChainDemo from '../lib/guide/ChainDemo.svelte';
  import Playground from '../lib/guide/Playground.svelte';
  import HowItWorks from '../lib/guide/HowItWorks.svelte';
  import Troubleshooter from '../lib/guide/Troubleshooter.svelte';
  import { Clock } from '../lib/guide/clock.svelte';
  import { Progress } from '../lib/guide/progress.svelte';
  import { DEFAULT_CONFIG, PHYSICAL_LEDS, physicalStrip, renderFrame, type StripConfig } from '../lib/guide/leds';
  import {
    BEFORE_CLASS,
    GUIDE,
    IN_CLASS,
    NEXT_STEPS,
    TROUBLESHOOTING,
  } from '../lib/guide/programmingLeds';

  const progress = new Progress(GUIDE.slug);
  const allIds = [...BEFORE_CLASS.steps, ...IN_CLASS.steps].map((s) => s.id);
  const doneCount = $derived(progress.count(allIds));

  // ── Hero strip: a few one-tap shows ───────────────────────────────────
  const heroClock = new Clock();
  const SHOWS: { id: string; label: string; cfg: Partial<StripConfig> }[] = [
    { id: 'kit', label: 'As shipped', cfg: {} },
    { id: 'rainbow', label: 'Rainbow', cfg: { palette: ['Red', 'Yellow', 'Lime', 'Cyan', 'Blue', 'Magenta'], ledSpread: 96 } },
    { id: 'fire', label: 'Fire', cfg: { palette: ['Red', 'Orange', 'Yellow', 'Orange'], stepMs: 600, ledSpread: 60 } },
    { id: 'ocean', label: 'Ocean', cfg: { palette: ['Blue', 'Cyan', 'Lime', 'Cyan'], stepMs: 1500, ledSpread: 40 } },
    { id: 'race', label: 'Race', cfg: { stepMs: 200 } },
    { id: 'solid', label: 'Solid', cfg: { ledSpread: 0 } },
  ];
  let show = $state('kit');
  const heroCfg = $derived<StripConfig>({ ...DEFAULT_CONFIG, ...SHOWS.find((s) => s.id === show)!.cfg, brightness: 160 });
  const heroLeds = $derived(physicalStrip(renderFrame(heroClock.elapsed, heroCfg), heroCfg));

  const NAV = [
    { href: '#before', label: 'At home' },
    { href: '#kit', label: 'The kit' },
    { href: '#wire', label: 'One wire' },
    { href: '#class', label: 'Light it up' },
    { href: '#code', label: 'Change the code' },
    { href: '#how', label: 'How it works' },
    { href: '#help', label: 'Help' },
  ];
</script>

<main id="main-content" class="app-shell guide">
  <TerminalPanel title="workshops // programming_leds">
    <Prompt text="workshop --level beginner --time 60m" />
    <h1 tabindex="-1">Programming LEDs</h1>
    <p class="lede">{GUIDE.lede}</p>
    <ul class="meta">
      <li><span>Time</span>{GUIDE.duration}</li>
      <li><span>Level</span>{GUIDE.level}</li>
      <li><span>Board</span>ESP32-C3</li>
      <li><span>LEDs</span>{PHYSICAL_LEDS} × WS2812B</li>
    </ul>

    <div class="hero-strip">
      <LedStrip leds={heroLeds} brightness={heroCfg.brightness} size="lg" label="Demo strip" />
      <div class="shows" role="group" aria-label="Try a pattern">
        {#each SHOWS as s (s.id)}
          <button type="button" class:on={show === s.id} aria-pressed={show === s.id} onclick={() => (show = s.id)}>
            {s.label}
          </button>
        {/each}
      </div>
      <p class="hint">
        Every one of these is the same program with different numbers at the top. By the end of class you'll make your
        own.
      </p>
    </div>
  </TerminalPanel>

  <nav class="toc" aria-label="Guide sections">
    <div class="progress" title="Steps marked done">
      <span class="bar" style:--pct="{(doneCount / allIds.length) * 100}%"><span></span></span>
      <span class="mono">{doneCount}/{allIds.length} steps</span>
    </div>
    <ul>
      {#each NAV as n (n.href)}<li><a href={n.href}>{n.label}</a></li>{/each}
    </ul>
  </nav>

  <!-- ── Before class ───────────────────────────────────────────────── -->
  <section id="before" class="sec">
    <p class="eyebrow">Part 1 · {BEFORE_CLASS.label}</p>
    <h2>{BEFORE_CLASS.title}</h2>
    <p class="intro">
      Please do this at home. It downloads several hundred megabytes and takes 15–20 minutes. If something goes sideways,
      come anyway and arrive a few minutes early.
    </p>
    <StepList steps={BEFORE_CLASS.steps} {progress} />
  </section>

  <TraceDivider />

  <!-- ── The kit ────────────────────────────────────────────────────── -->
  <section id="kit" class="sec">
    <p class="eyebrow">Part 2 · Meet the hardware</p>
    <h2>The kit</h2>
    <div class="kit">
      <figure>
        <img
          src="/img/workshops/programming-leds/kit-wired.webp"
          width="600"
          height="450"
          alt="The kit: an ESP32-C3 on a breadboard, wired to a WS2812B LED strip"
          loading="lazy"
        />
      </figure>
      <dl class="parts">
        <dt>ESP32-C3 board</dt>
        <dd>The computer. A full CPU with WiFi and Bluetooth, the size of a stick of gum. It runs one program, forever, the moment it gets power.</dd>
        <dt>Breadboard</dt>
        <dd>Holds the board and joins wires together. No soldering today.</dd>
        <dt>LED strip</dt>
        <dd>A row of WS2812B LEDs. Each one is a tiny controller chip with a red, a green and a blue LED inside.</dd>
        <dt>Three wires</dt>
        <dd>Power, ground and data. Already connected for you.</dd>
      </dl>
    </div>

    <h3>Find the three pins</h3>
    <p class="intro">Hover or tap a wire to find its pin on the board.</p>
    <BoardPinout />
  </section>

  <TraceDivider />

  <!-- ── One wire ───────────────────────────────────────────────────── -->
  <section id="wire" class="sec">
    <p class="eyebrow">Part 3 · Serial</p>
    <h2>How {PHYSICAL_LEDS} LEDs share one data wire</h2>
    <p class="intro">
      No LED has an address. The board sends one color per LED, in order, down one wire. Each LED keeps the first color
      it receives and passes the rest along. Press the buttons to watch it, including the ways it goes wrong without any
      warning.
    </p>
    <ChainDemo />
  </section>

  <TraceDivider />

  <!-- ── In class ───────────────────────────────────────────────────── -->
  <section id="class" class="sec">
    <p class="eyebrow">Part 4 · {IN_CLASS.label}</p>
    <h2>{IN_CLASS.title}</h2>
    <div class="buttons-ref">
      <p>The three buttons you need, in the blue bar at the bottom of VS Code:</p>
      <div class="vsbar" aria-hidden="true">
        <span>✓ Build</span><span>→ Upload</span><span>🔌 Serial monitor</span>
      </div>
      <p class="warn">Close the serial monitor before you upload, or the upload fails with <code>Could not exclusively lock port</code>.</p>
    </div>
    <StepList steps={IN_CLASS.steps} {progress} start={BEFORE_CLASS.steps.length + 1} />
  </section>

  <!-- ── Playground ─────────────────────────────────────────────────── -->
  <section id="code" class="sec">
    <p class="eyebrow">Part 5 · The fun part</p>
    <h2>Change the code</h2>
    <p class="intro">
      Pick an exercise, predict what will happen, then press <strong>Upload</strong>. Edit the numbers yourself too.
      Nothing changes on the strip until you upload, just like the real board. When it looks right here, make the same
      edit in <code>src/main.cpp</code> and upload it to your strip.
    </p>
    <Playground />
  </section>

  <TraceDivider />

  <section id="how" class="sec">
    <p class="eyebrow">Part 6 · Under the hood</p>
    <h2>How it works</h2>
    <HowItWorks />
  </section>

  <TraceDivider />

  <section id="help" class="sec">
    <p class="eyebrow">Stuck?</p>
    <h2>If something isn't working</h2>
    <Troubleshooter problems={TROUBLESHOOTING} />
  </section>

  <TraceDivider />

  <section id="next" class="sec">
    <p class="eyebrow">After the workshop</p>
    <h2>Take it home</h2>
    <p class="intro">The kit is yours. Some directions to go:</p>
    <ul class="next">
      {#each NEXT_STEPS as n, i (i)}<li>{n}</li>{/each}
    </ul>
    <p class="intro">
      All the code and notes are on GitHub:
      <a href={GUIDE.repoUrl} target="_blank" rel="noopener noreferrer">github.com/OpenCircuitSF/Workshops</a>.
      {#if doneCount > 0}
        <button type="button" class="link" onclick={() => progress.clear()}>Reset my checklist</button>
      {/if}
    </p>
  </section>
</main>

<style>
  .guide {
    display: grid;
    gap: var(--space-5);
  }
  h1 {
    margin: var(--space-3) 0 var(--space-2);
    font-size: clamp(28px, 6vw, 44px);
    line-height: 1.1;
  }
  .lede {
    margin: 0 0 var(--space-4);
    font-size: var(--fs-lg);
    max-width: var(--measure);
    color: var(--text-muted);
  }
  .meta {
    list-style: none;
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin: 0 0 var(--space-5);
    padding: 0;
  }
  .meta li {
    display: inline-flex;
    gap: var(--space-2);
    padding: 4px 10px;
    border: var(--border-w) solid var(--border);
    border-radius: 999px;
    font-size: var(--fs-base);
  }
  .meta span {
    font-family: var(--font-mono);
    color: var(--text-faint);
    text-transform: uppercase;
    font-size: var(--fs-sm);
  }
  .hero-strip {
    display: grid;
    gap: var(--space-3);
  }
  .shows {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }
  .shows button {
    font: inherit;
    font-size: var(--fs-md);
    padding: var(--space-2) var(--space-4);
    border-radius: 999px;
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  .shows button:hover {
    background: var(--bg-subtle);
  }
  .shows button.on {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .hint {
    margin: 0;
    font-size: var(--fs-base);
    color: var(--text-faint);
  }

  .toc {
    position: sticky;
    top: 0;
    z-index: 5;
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-3);
    padding: var(--space-2) var(--space-3);
    background: color-mix(in srgb, var(--bg-page) 92%, transparent);
    backdrop-filter: blur(6px);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
  }
  .toc ul {
    list-style: none;
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1) var(--space-3);
    margin: 0;
    padding: 0;
  }
  .toc a {
    font-size: var(--fs-base);
    color: var(--text-muted);
    text-decoration: none;
  }
  .toc a:hover {
    color: var(--accent);
  }
  .progress {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }
  .progress .bar {
    width: 80px;
    height: 6px;
    background: var(--bg-header);
    border-radius: 3px;
    overflow: hidden;
  }
  .progress .bar span {
    display: block;
    height: 100%;
    width: var(--pct);
    background: var(--accent);
    transition: width 0.3s;
  }
  .mono {
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    color: var(--text-faint);
  }

  .sec {
    scroll-margin-top: 64px;
  }
  .eyebrow {
    margin: 0;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--accent);
  }
  h2 {
    margin: var(--space-1) 0 var(--space-3);
    font-size: clamp(22px, 4vw, 30px);
  }
  h3 {
    margin: var(--space-5) 0 var(--space-2);
    font-size: var(--fs-lg);
  }
  .intro {
    margin: 0 0 var(--space-4);
    font-size: var(--fs-md);
    max-width: var(--measure);
    color: var(--text-muted);
  }
  code {
    font-family: var(--font-mono);
    background: var(--bg-subtle);
    padding: 1px 4px;
    border-radius: 3px;
  }

  .kit {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
    gap: var(--space-5);
    align-items: start;
  }
  @media (max-width: 720px) {
    .kit {
      grid-template-columns: 1fr;
    }
  }
  .kit figure {
    margin: 0;
  }
  .kit img {
    display: block;
    width: 100%;
    height: auto;
    border-radius: 6px;
  }
  .parts {
    margin: 0;
  }
  .parts dt {
    font-weight: 700;
    font-size: var(--fs-md);
  }
  .parts dd {
    margin: 2px 0 var(--space-3);
    color: var(--text-muted);
    font-size: var(--fs-md);
  }

  .buttons-ref {
    margin-bottom: var(--space-4);
  }
  .buttons-ref p {
    margin: 0 0 var(--space-2);
    font-size: var(--fs-md);
  }
  .vsbar {
    display: inline-flex;
    flex-wrap: wrap;
    gap: 2px;
    padding: 4px;
    margin-bottom: var(--space-2);
    background: var(--accent);
    border-radius: var(--radius);
  }
  .vsbar span {
    padding: 4px 12px;
    color: var(--accent-text);
    font-size: var(--fs-base);
    font-weight: 600;
  }
  .warn {
    color: var(--warning);
    font-weight: 600;
  }

  .next {
    margin: 0 0 var(--space-4);
    padding-left: var(--space-5);
    font-size: var(--fs-md);
    max-width: var(--measure);
  }
  .next li {
    margin-bottom: var(--space-1);
  }
  .link {
    font: inherit;
    background: none;
    border: 0;
    padding: 0;
    margin-left: var(--space-2);
    color: var(--accent);
    text-decoration: underline;
    cursor: pointer;
  }
</style>
