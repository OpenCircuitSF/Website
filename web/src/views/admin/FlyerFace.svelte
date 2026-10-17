<!--
  One printable flyer (#0549), drawn once for the editor preview and the print
  page so the three templates exist exactly once. It is a fixed-size box in
  CSS inches (full 8.5 x 11, half 5.5 x 8.5) with a 0.25in safe area: text
  and the QR code live inside the padding, and only the photo may bleed (the
  full-photo template). The palette is the fixed light print palette and
  ignores the site theme. Sizes and crop maths are in lib/flyers.ts.
-->
<script lang="ts">
  import type { FlyerFacts, FlyerTemplate } from '../../lib/types';
  import { FULL_PHOTO_BOX, SITE_URL_TEXT, displayShortUrl, noBreakHyphens, objectPosition, photoBox, qrQuietIn, type Focal, type FlyerInstance, type Size } from '../../lib/flyers';

  interface Props {
    template: FlyerTemplate;
    size: 'full' | 'half';
    facts: FlyerFacts;
    tagline: string;
    photo: string | null;
    focal: Focal;
    /** The location's copy; null in the editor before Generate. */
    instance: FlyerInstance | null;
  }
  let { template, size, facts, tagline, photo, focal, instance }: Props = $props();

  let natural = $state<Size | null>(null);
  const position = $derived(objectPosition(natural, photoBox(template, size), focal));
  const shortText = $derived(instance ? displayShortUrl(instance.shortUrl) : 'go.opencircuitsf.com/…');

  const qrIn = $derived(size === 'half' ? 0.9 : 1.25);
  const qrPad = $derived(`${qrQuietIn(qrIn).toFixed(4)}in`);
  const boxVars = $derived(
    `--box-fill: ${FULL_PHOTO_BOX.fill}; --box-alpha: ${FULL_PHOTO_BOX.alpha}; --box-ink: ${FULL_PHOTO_BOX.ink}; --box-muted: ${FULL_PHOTO_BOX.muted}; --box-accent: ${FULL_PHOTO_BOX.accent}`,
  );

  function onPhotoLoad(e: Event): void {
    const img = e.currentTarget as HTMLImageElement;
    natural = img.naturalWidth > 0 ? { w: img.naturalWidth, h: img.naturalHeight } : null;
  }
</script>

{#snippet photoBlock()}
  <div class="photo" class:tint={!photo}>
    {#if photo}
      <img src={photo} alt="" style:object-position={position} onload={onPhotoLoad} />
    {/if}
  </div>
{/snippet}

{#snippet textBlock()}
  <h2 class="title">{facts.title}</h2>
  {#if tagline}<p class="tagline">{tagline}</p>{/if}
  <p class="line">{facts.date_line}</p>
  <p class="line">{facts.place_line}</p>
{/snippet}

{#snippet footBlock()}
  <div class="foot">
    <div class="url">
      <p class="wordmark">open_circuit_sf</p>
      <p class="short">{shortText}</p>
    </div>
    <div class="qr">
      {#if instance}
        <img class="qr-img" src={instance.qrSrc} alt="QR code for {instance.label}" />
        <p class="label">{instance.label}</p>
      {:else}
        <div class="qr-img qr-placeholder"><span>QR</span></div>
      {/if}
    </div>
  </div>
{/snippet}

<div class="face t-{template} s-{size}">
  {#if template === 'photo-top'}
    {@render photoBlock()}
    <div class="body">{@render textBlock()}</div>
    {@render footBlock()}
  {:else if template === 'full-photo'}
    {@render photoBlock()}
    <div class="fp" style={boxVars}>
      <div class="fp-text">
        {@render textBlock()}
        <p class="fp-site">{SITE_URL_TEXT}</p>
      </div>
      <div class="fp-qr" style:--qpad={qrPad}>
        {#if instance}
          <img class="qr-img" src={instance.qrSrc} alt="QR code for {instance.label}" />
          <p class="fp-label">{noBreakHyphens(instance.label)}</p>
        {:else}
          <div class="qr-img qr-placeholder"><span>QR</span></div>
        {/if}
      </div>
    </div>
  {:else}
    {@render photoBlock()}
    <div class="col">
      <div class="body">{@render textBlock()}</div>
      {@render footBlock()}
    </div>
  {/if}
</div>

<style>
  .face {
    --ink: #10140f;
    --secondary: #3b4539;
    --accent: #30800c;
    --label: #5f6a5c;
    position: relative;
    box-sizing: border-box;
    overflow: hidden;
    padding: 0.25in;
    background: #fff;
    color: var(--ink);
    font-family: 'Archivo', ui-sans-serif, system-ui, sans-serif;
    display: flex;
    flex-direction: column;
    -webkit-print-color-adjust: exact;
    print-color-adjust: exact;
  }
  .s-full {
    width: 8.5in;
    height: 11in;
    --title: 48pt;
    --lede: 16pt;
    --line: 14pt;
    --short: 18pt;
    --qr: 1.25in;
    --label-size: 7pt;
    --fp-label: 8pt;
    --label-max: 3.4in;
    --site: 16pt;
  }
  .s-half {
    width: 5.5in;
    height: 8.5in;
    --title: 30pt;
    --lede: 11pt;
    --line: 11pt;
    --short: 12pt;
    --qr: 0.9in;
    --label-size: 6pt;
    --fp-label: 7pt;
    --label-max: 2.4in;
    --site: 12pt;
  }
  p,
  h2 {
    margin: 0;
  }
  .photo {
    position: relative;
    overflow: hidden;
    background: #eaf3e4;
  }
  .photo img {
    display: block;
    width: 100%;
    height: 100%;
    object-fit: cover;
  }
  .title {
    font-size: var(--title);
    font-weight: 800;
    line-height: 1.02;
    letter-spacing: -0.01em;
    overflow-wrap: anywhere;
    display: -webkit-box;
    -webkit-line-clamp: 3;
    line-clamp: 3;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
  .tagline {
    margin-top: 0.1in;
    font-size: var(--lede);
    line-height: 1.25;
    color: var(--secondary);
  }
  .line {
    margin-top: 0.06in;
    font-size: var(--line);
    font-weight: 700;
    line-height: 1.2;
    color: var(--accent);
  }
  .foot {
    display: flex;
    justify-content: space-between;
    align-items: flex-end;
    gap: 0.2in;
  }
  .wordmark {
    font-family: 'JetBrains Mono', ui-monospace, Menlo, monospace;
    font-size: 9pt;
    font-weight: 700;
    color: var(--accent);
  }
  .short {
    margin-top: 0.04in;
    font-family: 'JetBrains Mono', ui-monospace, Menlo, monospace;
    font-size: var(--short);
    font-weight: 700;
    overflow-wrap: anywhere;
  }
  .qr {
    flex: 0 0 auto;
    width: var(--qr);
  }
  .qr-img {
    display: block;
    width: var(--qr);
    height: var(--qr);
    background: #fff;
  }
  .qr-placeholder {
    display: flex;
    align-items: center;
    justify-content: center;
    box-sizing: border-box;
    border: 0.02in dashed var(--label);
    color: var(--label);
    font-family: 'JetBrains Mono', ui-monospace, Menlo, monospace;
    font-size: 10pt;
  }
  .label {
    margin-top: 0.03in;
    font-family: 'JetBrains Mono', ui-monospace, Menlo, monospace;
    font-size: var(--label-size);
    line-height: 1.2;
    color: var(--label);
    overflow-wrap: anywhere;
  }

  /* Photo top: the photo fills the top of the content box, text and the foot below. */
  .t-photo-top .photo {
    flex: 0 0 58%;
  }
  .t-photo-top.s-half .photo {
    flex-basis: 53%;
  }
  .t-photo-top .body {
    flex: 1 1 auto;
    min-height: 0;
    padding-top: 0.2in;
    overflow: hidden;
  }

  /* Full photo: the photo fills the whole face. One semi-transparent dark
     box holds the text; a separate box holds the QR code. The fill is a
     background-colour with alpha so the text itself stays fully opaque. */
  .t-full-photo .photo {
    position: absolute;
    inset: 0;
  }
  .t-full-photo .fp {
    position: absolute;
    inset: 0.25in;
    display: flex;
    flex-direction: column;
    justify-content: space-between;
    pointer-events: none;
  }
  .t-full-photo .fp-text {
    box-sizing: border-box;
    padding: 0.2in;
    border-radius: 0.08in;
    background: color-mix(in srgb, var(--box-fill) calc(var(--box-alpha) * 100%), transparent);
    color: var(--box-ink);
  }
  .t-full-photo .fp-text .tagline {
    color: var(--box-muted);
  }
  .t-full-photo .fp-text .line {
    color: var(--box-accent);
  }
  .t-full-photo .fp-site {
    margin-top: 0.12in;
    font-family: 'JetBrains Mono', ui-monospace, Menlo, monospace;
    font-size: var(--site);
    font-weight: 700;
    color: var(--box-ink);
  }
  .t-full-photo .fp-qr {
    align-self: flex-end;
    box-sizing: border-box;
    width: max-content;
    max-width: 100%;
    padding: var(--qpad);
    border-radius: 0.06in;
    background: #fff;
    color: var(--ink);
  }
  .t-full-photo .fp-qr .qr-img {
    margin: 0 auto;
  }
  .t-full-photo .fp-label {
    margin-top: 0.04in;
    max-width: var(--label-max);
    text-align: center;
    font-family: 'JetBrains Mono', ui-monospace, Menlo, monospace;
    font-size: var(--fp-label);
    font-weight: 700;
    line-height: 1.2;
    color: var(--ink);
    overflow-wrap: normal;
    word-break: normal;
  }

  /* Split: the photo takes the left half, the text column the right. */
  .t-split {
    flex-direction: row;
    gap: 0.2in;
  }
  .t-split .photo {
    flex: 0 0 calc(50% - 0.1in);
  }
  .t-split .col {
    flex: 1 1 0;
    min-width: 0;
    display: flex;
    flex-direction: column;
  }
  .t-split .col .body {
    flex: 1 1 auto;
    min-height: 0;
    overflow: hidden;
  }
  .t-split .foot {
    flex-direction: column;
    align-items: flex-start;
  }
</style>
