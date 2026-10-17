<!--
  The admin-only print page for a flyer (#0549): /admin#/outreach?view=print.
  Full page is one 8.5 x 11 in flyer per sheet; half-size double is two
  5.5 x 8.5 in flyers per sheet, each rotated 90 degrees, with a dashed cut
  line at 5.5 in. The sheet geometry is plain CSS inches; the maths it
  relies on (the rotation bounds, filling order, sheet counts) is tested in
  lib/flyers.ts.

  The page is chrome-less. Its root is moved directly under <body> and
  <body> gets the flyer-print class, so the global rule below hides the site
  header, footer and admin nav on screen and in print. The @page rule is
  inserted into <head> only while this view is mounted, because a @page rule
  in bundled CSS would apply to every page of the site.

  The Print button waits for fonts and every image (photo and QR codes) to
  settle, so nothing prints half-loaded; an image that failed is named in the
  toolbar. jsdom proves this wiring, not the layout: the layout is checked in
  a real browser's print preview.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { getOutreachFlyer, ApiError } from '../../lib/api';
  import { adminOutreachFlyersHash, adminOutreachPrintHash, type FlyerPrintLayout } from '../../lib/adminRouter';
  import {
    PRINT_PAGE_CSS,
    expandCopies,
    missingQrCount,
    missingQrNote,
    paginate,
    photoWarning,
    sheetCount,
  } from '../../lib/flyers';
  import type { FlyerView } from '../../lib/types';
  import FlyerFace from './FlyerFace.svelte';

  let { flyerId, layout = 'full' }: { flyerId: number; layout?: FlyerPrintLayout } = $props();

  let data = $state<FlyerView | null>(null);
  let loadError = $state<string | null>(null);
  let ready = $state(false);
  let failedImages = $state<string[]>([]);
  let photoWarn = $state<string | null>(null);
  let root = $state<HTMLElement | null>(null);

  const instances = $derived(data ? expandCopies(data.selection) : []);
  const sheets = $derived(paginate(instances, layout));
  const missingNote = $derived(data ? missingQrNote(missingQrCount(data.selection)) : null);
  const summary = $derived(
    `${instances.length} ${instances.length === 1 ? 'flyer' : 'flyers'} on ${sheetCount(instances.length, layout)} ${sheetCount(instances.length, layout) === 1 ? 'sheet' : 'sheets'}`,
  );

  /** Moves the node under <body> so the page can hide everything else. */
  function portal(node: HTMLElement): { destroy(): void } {
    document.body.appendChild(node);
    return {
      destroy() {
        node.remove();
      },
    };
  }

  // The print-only page rule and the body class, both removed on destroy.
  $effect(() => {
    const style = document.createElement('style');
    style.setAttribute('data-flyer-print', '');
    style.textContent = PRINT_PAGE_CSS;
    document.head.appendChild(style);
    document.body.classList.add('flyer-print');
    return () => {
      style.remove();
      document.body.classList.remove('flyer-print');
    };
  });

  onMount(async () => {
    try {
      data = await getOutreachFlyer(flyerId);
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : 'Could not load the flyer. Please try again.';
    }
  });

  function settle(img: HTMLImageElement): Promise<boolean> {
    if (img.complete) return Promise.resolve(img.naturalWidth > 0);
    return new Promise((resolve) => {
      img.addEventListener('load', () => resolve(true), { once: true });
      img.addEventListener('error', () => resolve(false), { once: true });
    });
  }

  // Ready gate: fonts, then every image in the root. Re-armed whenever the
  // sheets change.
  $effect(() => {
    void sheets;
    if (!data || !root) return;
    const el = root;
    let cancelled = false;
    ready = false;
    (async () => {
      try {
        await document.fonts?.ready;
      } catch {
        // A font failure must not leave the button disabled for good.
      }
      const imgs = Array.from(el.querySelectorAll<HTMLImageElement>('.sheet img'));
      const results = await Promise.all(imgs.map(settle));
      if (cancelled) return;
      failedImages = imgs.filter((_, i) => !results[i]).map((img) => img.getAttribute('alt') || 'photo');
      const photo = imgs.find((img) => img.closest('.photo'));
      photoWarn = photo && photo.naturalWidth > 0 ? photoWarning(photo.naturalWidth, photo.naturalHeight, layout) : null;
      ready = true;
    })();
    return () => {
      cancelled = true;
    };
  });
</script>

<div class="flyer-print-root" bind:this={root} use:portal>
  <div class="toolbar">
    <a href={adminOutreachFlyersHash(flyerId)}>Back to the flyer</a>
    <span class="layouts">
      <a href={adminOutreachPrintHash(flyerId, 'full')} aria-current={layout === 'full' ? 'page' : undefined}>Full page</a>
      <a href={adminOutreachPrintHash(flyerId, 'half')} aria-current={layout === 'half' ? 'page' : undefined}>Half-size double</a>
    </span>
    <button type="button" class="print-btn" disabled={!ready || instances.length === 0} onclick={() => window.print()}>Print</button>
    <p class="note" role="status">
      {#if loadError}
        {loadError}
      {:else if !data}
        Loading the flyer.
      {:else if !ready}
        Loading images before printing.
      {:else}
        {summary}. Choose Letter, portrait, scale Actual size (100%), margins None.
      {/if}
    </p>
    {#if missingNote}<p class="note warn">{missingNote}</p>{/if}
    {#if photoWarn}<p class="note warn">{photoWarn}</p>{/if}
    {#if failedImages.length > 0}<p class="note warn">Could not load: {failedImages.join(', ')}.</p>{/if}
  </div>

  {#if data}
    {#each sheets as sheet, i (i)}
      <section class="sheet" class:last={i === sheets.length - 1} aria-label="Sheet {i + 1} of {sheets.length}">
        {#if layout === 'full'}
          {#each sheet as inst}
            {#if inst}
              <FlyerFace
                template={data.flyer.template}
                size="full"
                facts={data.facts}
                tagline={data.flyer.tagline}
                photo={data.flyer.photo ?? data.facts.cover_image}
                focal={{ x: data.flyer.focal_x, y: data.flyer.focal_y }}
                instance={inst}
              />
            {/if}
          {/each}
        {:else}
          {#each sheet as inst, k}
            <div class="slot" style:--y={k === 0 ? '5.5in' : '11in'}>
              {#if inst}
                <div class="rot">
                  <FlyerFace
                    template={data.flyer.template}
                    size="half"
                    facts={data.facts}
                    tagline={data.flyer.tagline}
                    photo={data.flyer.photo ?? data.facts.cover_image}
                    focal={{ x: data.flyer.focal_x, y: data.flyer.focal_y }}
                    instance={inst}
                  />
                </div>
              {/if}
            </div>
          {/each}
          <div class="cut" aria-hidden="true"><span>cut at 5.5 in</span></div>
        {/if}
      </section>
    {/each}
  {/if}
</div>

<style>
  :global(body.flyer-print > :not(.flyer-print-root)) {
    display: none !important;
  }
  :global(body.flyer-print) {
    margin: 0;
    background: #d8ddd5;
  }
  .flyer-print-root {
    font-family: 'Archivo', ui-sans-serif, system-ui, sans-serif;
  }
  .toolbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 12px 20px;
    padding: 12px 16px;
    background: #fff;
    color: #10140f;
    border-bottom: 1px solid #b9c2b4;
  }
  .toolbar a {
    color: #30800c;
  }
  .toolbar a[aria-current='page'] {
    font-weight: 700;
    text-decoration: underline;
  }
  .layouts {
    display: flex;
    gap: 16px;
  }
  .print-btn {
    padding: 8px 16px;
    font: inherit;
    font-weight: 700;
    color: #fff;
    background: #30800c;
    border: 0;
    border-radius: 4px;
    cursor: pointer;
  }
  .print-btn:disabled {
    background: #8a9885;
    cursor: not-allowed;
  }
  .note {
    flex: 1 1 100%;
    margin: 0;
    font-size: 14px;
  }
  .warn {
    color: #8a2d00;
  }
  .sheet {
    position: relative;
    width: 8.5in;
    height: 11in;
    margin: 16px auto;
    overflow: hidden;
    background: #fff;
    box-shadow: 0 1px 6px rgba(0, 0, 0, 0.3);
    break-after: page;
    page-break-after: always;
    -webkit-print-color-adjust: exact;
    print-color-adjust: exact;
  }
  .sheet.last {
    break-after: auto;
    page-break-after: auto;
  }
  /* Both slots span the whole sheet (no clipping: a flyer fills its half
     exactly once rotated). --y is how far down the rotated flyer lands. */
  .slot {
    position: absolute;
    inset: 0;
    pointer-events: none;
  }
  .rot {
    position: absolute;
    left: 0;
    top: 0;
    width: 5.5in;
    height: 8.5in;
    transform-origin: 0 0;
    /* Rotate about the corner, then drop into the half (--y: 5.5in for the
       top half, 11in for the bottom). Every flyer is laid out at the top of
       the sheet, inside the page, and only the transform moves it: a box
       laid out past the page's bottom edge is fragmented away in print. */
    transform: translateY(var(--y)) rotate(-90deg);
  }
  .cut {
    position: absolute;
    left: 0;
    right: 0;
    top: 5.5in;
    border-top: 0.02in dashed #87977f;
    pointer-events: none;
  }
  .cut span {
    position: absolute;
    right: 0.25in;
    top: 0.02in;
    font-family: 'JetBrains Mono', ui-monospace, Menlo, monospace;
    font-size: 7pt;
    color: #87977f;
  }
  @media print {
    .toolbar {
      display: none;
    }
    :global(body.flyer-print) {
      background: #fff;
    }
    .sheet {
      margin: 0;
      box-shadow: none;
    }
  }
</style>
