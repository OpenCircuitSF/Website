<!--
  One flyer's editor (#0549): photo with a draggable focal point, template,
  tagline, which locations to print for and how many copies, then Generate
  (one ShortLinks link and QR code per location, shared by every flyer for
  that workshop and location), a live preview at both sizes, the generated
  links with Put up, and links to the print page. Maths and wording are in
  lib/flyers.ts. Designing works without ShortLinks; only Generate needs it.
  Generate saves the design, then the selection, then asks the server, and
  shows the server's remaining count and message in the role="status" line,
  so a partial run can simply be repeated.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import {
    getOutreachFlyer,
    updateOutreachFlyer,
    setOutreachFlyerLocations,
    generateOutreachFlyer,
    deleteOutreachFlyer,
    markFlyerLinkPutUp,
    clearFlyerLinkPutUp,
    listOutreachLocations,
    mediaUpload,
    ApiError,
  } from '../../lib/api';
  import { adminOutreachFlyersHash, adminOutreachHash, adminOutreachPrintHash } from '../../lib/adminRouter';
  import { formatDateTime } from '../../lib/admin';
  import { precheckFile, messageForPrecheckReason, messageForUploadError } from '../../lib/media';
  import {
    DEFAULT_FLYER_TEMPLATE,
    FLYER_TEMPLATES,
    FLYER_TEMPLATE_LABELS,
    MAX_TAGLINE,
    displayShortUrl,
    expandCopies,
    focalFromPointer,
    focalText,
    nudgeFocal,
    photoWarning,
    FLYER_TITLE_POSITIONS,
    FLYER_TITLE_POSITION_LABELS,
    MAX_FLYER_DESCRIPTION,
    MAX_FLYER_DESCRIPTION_SHORT,
    selectionSummary,
    sheetCount,
    utmSummary,
    validateFlyerDescription,
    validateTagline,
    type Focal,
  } from '../../lib/flyers';
  import type { FlyerTemplate, FlyerTitlePosition, FlyerView, OutreachLocation } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import Panel from '../../lib/Panel.svelte';
  import FlyerFace from './FlyerFace.svelte';

  let { flyerId }: { flyerId: number } = $props();

  let view = $state<FlyerView | null>(null);
  let locations = $state<OutreachLocation[]>([]);
  let status = $state('Loading the flyer.');
  let busy = $state(false);
  let uploading = $state(false);
  let confirmDelete = $state(false);

  // The design and selection being edited; saved by Save and Generate.
  let template = $state<FlyerTemplate>(DEFAULT_FLYER_TEMPLATE);
  let tagline = $state('');
  let description = $state('');
  let descriptionShort = $state('');
  let titlePosition = $state<FlyerTitlePosition>('auto');
  let photo = $state<string | null>(null);
  let focal = $state<Focal>({ x: 0.5, y: 0.5 });
  let copies = $state<Record<number, number>>({});
  let photoSize = $state<{ w: number; h: number } | null>(null);
  let dragging = $state(false);

  const taglineError = $derived(validateTagline(tagline));
  // The description limits apply only while their controls are shown (full-photo,
  // #0562). Hidden values are kept, not cleared, and are ignored by validation.
  const descriptionError = $derived(validateFlyerDescription(description, MAX_FLYER_DESCRIPTION, 'Description'));
  const descriptionShortError = $derived(validateFlyerDescription(descriptionShort, MAX_FLYER_DESCRIPTION_SHORT, 'Short description'));
  const textError = $derived(taglineError ?? (template === 'full-photo' ? (descriptionError ?? descriptionShortError) : null));
  const selected = $derived(Object.keys(copies).map(Number));
  const draftSummary = $derived(selectionSummary(selected.map((id) => ({ copies: copies[id] }))));
  const shownLocations = $derived(locations.filter((l) => l.active || l.id in copies));
  const printable = $derived(view ? expandCopies(view.selection) : []);
  const exampleInstance = $derived(printable[0] ?? null);
  const effectivePhoto = $derived(photo ?? view?.facts.cover_image ?? null);
  const fullWarning = $derived(photoSize ? photoWarning(photoSize.w, photoSize.h, 'full') : null);

  function message(err: unknown, fallback: string): string {
    return err instanceof ApiError ? err.message : fallback;
  }

  function adopt(v: FlyerView): void {
    view = v;
    template = v.flyer.template;
    tagline = v.flyer.tagline;
    description = v.flyer.description;
    descriptionShort = v.flyer.description_short;
    titlePosition = v.flyer.title_position;
    photo = v.flyer.photo;
    focal = { x: v.flyer.focal_x, y: v.flyer.focal_y };
    const next: Record<number, number> = {};
    for (const s of v.selection) next[s.location_id] = s.copies;
    copies = next;
  }

  async function load(): Promise<void> {
    try {
      const [v, l] = await Promise.all([getOutreachFlyer(flyerId), listOutreachLocations()]);
      locations = l.locations;
      adopt(v);
      status = '';
    } catch (err) {
      status = message(err, 'Could not load the flyer. Please try again.');
    }
  }

  async function saveAll(): Promise<FlyerView> {
    await updateOutreachFlyer(flyerId, {
      template,
      tagline: tagline.trim(),
      description: description.trim(),
      description_short: descriptionShort.trim(),
      title_position: titlePosition,
      photo: photo ?? '',
      focal_x: focal.x,
      focal_y: focal.y,
    });
    return setOutreachFlyerLocations(
      flyerId,
      selected.map((id) => ({ location_id: id, copies: copies[id] })),
    );
  }

  async function save(): Promise<void> {
    if (busy || textError) return;
    busy = true;
    status = '';
    try {
      adopt(await saveAll());
      status = 'Flyer saved.';
    } catch (err) {
      status = message(err, 'Could not save the flyer. Please try again.');
    } finally {
      busy = false;
    }
  }

  async function generate(): Promise<void> {
    if (busy || textError || selected.length === 0) return;
    busy = true;
    status = 'Generating links and QR codes.';
    try {
      await saveAll();
      const v = await generateOutreachFlyer(flyerId);
      adopt(v);
      status = v.remaining ? `${v.remaining} of ${v.selection.length} locations still need a link or QR code. ${v.message ?? ''}`.trim() : 'Links and QR codes are ready.';
    } catch (err) {
      status = message(err, 'Could not generate. Everything saved so far is kept; try again.');
    } finally {
      busy = false;
    }
  }

  async function togglePutUp(linkId: number, isUp: boolean): Promise<void> {
    status = '';
    try {
      if (isUp) await clearFlyerLinkPutUp(linkId);
      else await markFlyerLinkPutUp(linkId);
      adopt(await getOutreachFlyer(flyerId));
    } catch (err) {
      status = message(err, 'Could not update that location. Please try again.');
    }
  }

  async function remove(): Promise<void> {
    try {
      await deleteOutreachFlyer(flyerId);
      window.location.hash = adminOutreachFlyersHash();
    } catch (err) {
      confirmDelete = false;
      status = message(err, 'Could not delete the flyer. Please try again.');
    }
  }

  async function onPhotoFile(e: Event): Promise<void> {
    const input = e.currentTarget as HTMLInputElement;
    const file = input.files?.[0] ?? null;
    if (!file) return;
    const pre = precheckFile(file);
    if (!pre.ok) {
      status = messageForPrecheckReason(pre.reason!);
      input.value = '';
      return;
    }
    uploading = true;
    status = 'Uploading the photo.';
    try {
      photo = (await mediaUpload(file)).path;
      photoSize = null;
      status = 'Photo uploaded. Save to keep it.';
    } catch (err) {
      status = messageForUploadError(err);
    } finally {
      uploading = false;
      input.value = '';
    }
  }

  function toggleLocation(id: number, on: boolean): void {
    const next = { ...copies };
    if (on) next[id] = next[id] ?? 1;
    else delete next[id];
    copies = next;
  }

  function setCopies(id: number, raw: string): void {
    const n = Math.min(50, Math.max(1, Math.trunc(Number(raw)) || 1));
    copies = { ...copies, [id]: n };
  }

  function onPhotoPointer(e: PointerEvent): void {
    const el = e.currentTarget as HTMLElement;
    focal = focalFromPointer(e.clientX, e.clientY, el.getBoundingClientRect());
  }

  function onFocalKey(e: KeyboardEvent): void {
    const next = nudgeFocal(focal, e.key, e.shiftKey);
    if (next) {
      e.preventDefault();
      focal = next;
    }
  }

  onMount(load);
</script>

<Panel title={view ? `Flyer: ${view.flyer.workshop_title}` : 'Flyer'}>
  <p><a href={adminOutreachFlyersHash()}>All flyers</a></p>
  <p class="status" role="status">{status}</p>

  {#if view}
    <div class="editor">
      <div class="setup">
        <section aria-labelledby="fl-photo">
          <h3 id="fl-photo">Photo</h3>
          {#if effectivePhoto}
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <div
              class="focal-box"
              onpointerdown={(e) => {
                dragging = true;
                (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId);
                onPhotoPointer(e);
              }}
              onpointermove={(e) => dragging && onPhotoPointer(e)}
              onpointerup={() => (dragging = false)}
            >
              <img
                src={effectivePhoto}
                alt=""
                draggable="false"
                onload={(e) => {
                  const img = e.currentTarget as HTMLImageElement;
                  photoSize = img.naturalWidth > 0 ? { w: img.naturalWidth, h: img.naturalHeight } : null;
                }}
              />
              <span
                class="focal"
                role="slider"
                tabindex="0"
                aria-label="Focal point"
                aria-orientation="horizontal"
                aria-valuemin="0"
                aria-valuemax="100"
                aria-valuenow={Math.round(focal.x * 100)}
                aria-valuetext={focalText(focal)}
                style:left="{focal.x * 100}%"
                style:top="{focal.y * 100}%"
                onkeydown={onFocalKey}
              ></span>
            </div>
            <p class="text-muted">Drag, or use the arrow keys (Shift for bigger steps), to choose what stays in frame. {focalText(focal)}.</p>
          {:else}
            <p class="text-muted">No photo. The flyer prints with a plain tint.</p>
          {/if}
          {#if fullWarning}<p class="warn">{fullWarning}</p>{/if}
          <div class="row">
            <label for="fl-upload" class="sr-only">Upload a photo</label>
            <input id="fl-upload" type="file" accept=".jpg,.jpeg,.png,image/jpeg,image/png" disabled={busy || uploading} onchange={onPhotoFile} />
            {#if view.facts.cover_image && photo !== view.facts.cover_image}
              <Button onclick={() => (photo = view?.facts.cover_image ?? null)}>Use cover image</Button>
            {/if}
            {#if photo}<Button onclick={() => (photo = null)}>Remove photo</Button>{/if}
          </div>
        </section>

        <section aria-labelledby="fl-template">
          <h3 id="fl-template">Template</h3>
          <div class="row" role="radiogroup" aria-labelledby="fl-template">
            {#each FLYER_TEMPLATES as t (t)}
              <label><input type="radio" name="fl-template" value={t} bind:group={template} /> {FLYER_TEMPLATE_LABELS[t]}</label>
            {/each}
          </div>
        </section>

        <section>
          <h3><label for="fl-tagline-input">Tagline</label></h3>
          <input id="fl-tagline-input" type="text" bind:value={tagline} aria-describedby="fl-tagline-err" />
          <p class="text-muted">{[...tagline].length} of {MAX_TAGLINE}</p>
          <p class="text-error" id="fl-tagline-err">{taglineError ?? ''}</p>
        </section>

        {#if template === 'full-photo'}
          <section aria-labelledby="fl-titlepos">
            <h3 id="fl-titlepos">Title position</h3>
            <div class="stack" role="radiogroup" aria-labelledby="fl-titlepos">
              {#each FLYER_TITLE_POSITIONS as p (p)}
                <label><input type="radio" name="fl-titlepos" value={p} bind:group={titlePosition} /> {FLYER_TITLE_POSITION_LABELS[p]}</label>
              {/each}
            </div>
          </section>

          <section>
            <h3><label for="fl-desc-input">Description</label></h3>
            <textarea id="fl-desc-input" rows="5" bind:value={description} placeholder={view.facts.description} aria-describedby="fl-desc-hint fl-desc-err"></textarea>
            <p class="text-muted" id="fl-desc-hint">Leave empty to use the workshop summary.</p>
            <p class="text-muted">{[...description].length} of {MAX_FLYER_DESCRIPTION}</p>
            <p class="text-error" id="fl-desc-err">{descriptionError ?? ''}</p>
          </section>

          <section>
            <h3><label for="fl-short-input">Short description (half size)</label></h3>
            <input id="fl-short-input" type="text" bind:value={descriptionShort} aria-describedby="fl-short-hint fl-short-err" />
            <p class="text-muted" id="fl-short-hint">Leave empty to shorten the description automatically.</p>
            <p class="text-muted">{[...descriptionShort].length} of {MAX_FLYER_DESCRIPTION_SHORT}</p>
            <p class="text-error" id="fl-short-err">{descriptionShortError ?? ''}</p>
          </section>
        {/if}

        <section aria-labelledby="fl-locs">
          <h3 id="fl-locs">Locations</h3>
          {#if shownLocations.length === 0}
            <p class="text-muted">No locations yet. <a href={adminOutreachHash('locations')}>Manage locations</a></p>
          {:else}
            <ul class="locs">
              {#each shownLocations as l (l.id)}
                <li>
                  <label>
                    <input type="checkbox" checked={l.id in copies} onchange={(e) => toggleLocation(l.id, e.currentTarget.checked)} />
                    {l.name} <span class="mono">{l.code}</span>{l.active ? '' : ' (inactive)'}
                  </label>
                  {#if l.id in copies}
                    <label class="copies">
                      Copies
                      <input type="number" min="1" max="50" value={copies[l.id]} onchange={(e) => setCopies(l.id, e.currentTarget.value)} />
                    </label>
                  {/if}
                </li>
              {/each}
            </ul>
            <p><a href={adminOutreachHash('locations')}>Manage locations</a></p>
          {/if}
          <p class="text-muted">{draftSummary}</p>
          <p class="text-muted">Each link is tagged <span class="mono">{utmSummary(view.flyer.workshop_slug)}</span> and is shared by every flyer for this workshop and location.</p>
        </section>

        <div class="row">
          <Button variant="primary" onclick={save} disabled={busy || !!textError}>Save</Button>
          <Button variant="primary" onclick={generate} disabled={busy || !!textError || selected.length === 0}>Generate links and QR codes</Button>
        </div>
      </div>

      <div class="preview">
        <h3>Preview</h3>
        <div class="previews">
          <figure>
            <div class="scaled" style="--w: 8.5in; --h: 11in; --s: 0.36">
              <div class="inner full">
                <FlyerFace {template} size="full" facts={view.facts} {tagline} {description} {descriptionShort} {titlePosition} photo={effectivePhoto} {focal} instance={exampleInstance} />
              </div>
            </div>
            <figcaption>
              Full page · {sheetCount(printable.length, 'full')} {sheetCount(printable.length, 'full') === 1 ? 'sheet' : 'sheets'}
              <a href={adminOutreachPrintHash(flyerId, 'full')} target="_blank" rel="noopener">Print all · full page</a>
            </figcaption>
          </figure>
          <figure>
            <div class="scaled" style="--w: 5.5in; --h: 8.5in; --s: 0.46">
              <div class="inner half">
                <FlyerFace {template} size="half" facts={view.facts} {tagline} {description} {descriptionShort} {titlePosition} photo={effectivePhoto} {focal} instance={exampleInstance} />
              </div>
            </div>
            <figcaption>
              Half-size double · {sheetCount(printable.length, 'half')} {sheetCount(printable.length, 'half') === 1 ? 'sheet' : 'sheets'}
              <a href={adminOutreachPrintHash(flyerId, 'half')} target="_blank" rel="noopener">Print all · half-size double</a>
            </figcaption>
          </figure>
        </div>
        <p class="text-muted">In the print dialog choose Letter, portrait, scale Actual size (100%), margins None, and turn headers and footers off.</p>

        <h3>Generated links</h3>
        {#if view.selection.length === 0}
          <p class="text-muted">Choose locations and save.</p>
        {:else}
          <ul class="links">
            {#each view.selection as s (s.location_id)}
              <li>
                <strong>{s.name}</strong> <span class="mono">{s.code}</span>
                {#if s.link}
                  <span class="mono">{displayShortUrl(s.link.short_url)}</span>
                  {#if s.link.has_qr && s.link.qr_url}
                    <img class="thumb" src={s.link.qr_url} alt="QR code for {s.name}" />
                    <a href={s.link.qr_url} download>QR (SVG)</a>
                  {:else}
                    <span class="warn">No QR code yet</span>
                  {/if}
                  {#if s.link.put_up_at}
                    <span>Put up {formatDateTime(s.link.put_up_at)}</span>
                    <Button variant="subtle" onclick={() => togglePutUp(s.link!.id, true)}>Undo</Button>
                  {:else}
                    <Button variant="subtle" onclick={() => togglePutUp(s.link!.id, false)}>Mark put up</Button>
                  {/if}
                {:else}
                  <span class="text-muted">Not generated yet</span>
                {/if}
              </li>
            {/each}
          </ul>
        {/if}

        <p>
          {#if confirmDelete}
            Delete this flyer? The links stay.
            <Button variant="danger" onclick={remove}>Yes</Button>
            <Button onclick={() => (confirmDelete = false)}>No</Button>
          {:else}
            <Button variant="subtle" onclick={() => (confirmDelete = true)}>Delete flyer</Button>
          {/if}
        </p>
      </div>
    </div>
  {/if}
</Panel>

<style>
  .status {
    min-height: 1.5em;
    margin: 0 0 var(--space-2);
    color: var(--text-muted);
  }
  .editor {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: var(--space-4);
  }
  @media (min-width: 64rem) {
    .editor {
      grid-template-columns: minmax(0, 24rem) minmax(0, 1fr);
    }
  }
  h3 {
    font-size: var(--fs-lg);
    margin: var(--space-3) 0 var(--space-2);
  }
  .row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-3);
  }
  .stack {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }
  textarea {
    width: 100%;
    box-sizing: border-box;
    font: inherit;
  }
  .warn {
    color: var(--warning, #b45309);
  }
  .mono {
    font-family: var(--font-mono);
  }
  .focal-box {
    position: relative;
    max-width: 22rem;
    touch-action: none;
    cursor: crosshair;
    user-select: none;
  }
  .focal-box img {
    display: block;
    width: 100%;
    height: auto;
  }
  .focal {
    position: absolute;
    width: 1.5rem;
    height: 1.5rem;
    margin: -0.75rem 0 0 -0.75rem;
    border: 3px solid #fff;
    border-radius: 50%;
    box-shadow: 0 0 0 2px var(--accent), 0 0 6px rgba(0, 0, 0, 0.6);
  }
  .focal:focus-visible {
    outline: 3px solid var(--accent);
    outline-offset: 3px;
  }
  .locs {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .locs li {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
    align-items: center;
    margin-bottom: var(--space-1);
  }
  .copies input {
    width: 4rem;
  }
  .previews {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-4);
    align-items: flex-start;
  }
  figure {
    margin: 0;
  }
  figcaption {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    margin-top: var(--space-1);
    font-size: var(--fs-sm);
  }
  .scaled {
    width: calc(var(--w) * var(--s));
    height: calc(var(--h) * var(--s));
    overflow: hidden;
    border: var(--border-w) solid var(--border);
  }
  .inner {
    transform: scale(var(--s));
    transform-origin: 0 0;
  }
  .inner.full {
    width: 8.5in;
    height: 11in;
  }
  .inner.half {
    width: 5.5in;
    height: 8.5in;
  }
  .links {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .links li {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2) var(--space-3);
    align-items: center;
    margin-bottom: var(--space-2);
  }
  .thumb {
    width: 3rem;
    height: 3rem;
    background: #fff;
  }
</style>
