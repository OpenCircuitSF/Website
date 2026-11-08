<!--
  One workshop's outreach page (#0568, #0582, #0625): header, then the status
  board, then the pinned section tabs and the one open section (one message,
  how it will look, one post button; #0640). The others stay mounted but
  hidden, so typed text survives a tab switch. The default is picked once per
  workshop and selecting from the board or a tab goes through selectSection.
  Then Flyers and "Links and clicks" as collapsed rows with a status and one action each. The
  OutreachPanel stays self-loading (and starts outreach implicitly) and hands
  its view and results to the body snippet. The tracked-links table lives on
  Results, not here.

  Leaving with unsaved typing warns (#0632): a reload or closed tab through the
  browser's own beforeunload prompt, a same-site link through an in-app dialog
  (lib/outreachLeave.ts decides which clicks ask). After a Retry that brings the
  page back, or a Reset, focus goes to the open section's tab, else the heading
  (#0633).
-->
<script lang="ts">
  import { adminOutreachFlyersHash, adminOutreachHash, adminOutreachResultsHash, adminWorkshopHash } from '../../lib/adminRouter';
  import { formatDateTime } from '../../lib/admin';
  import type { FlyerRow, OutreachPost, WorkshopOutreach } from '../../lib/types';
  import Panel from '../../lib/Panel.svelte';
  import OutreachPanel from './OutreachPanel.svelte';
  import OutreachStatusBoard from './OutreachStatusBoard.svelte';
  import OutreachSectionTabs from './OutreachSectionTabs.svelte';
  import OutreachSection from './OutreachSection.svelte';
  import OutreachResetDialog from './OutreachResetDialog.svelte';
  import OutreachConfirmDialog from './OutreachConfirmDialog.svelte';
  import { asksBeforeLeaving } from '../../lib/outreachLeave';
  import Button from '../../lib/Button.svelte';
  import { onMount, tick } from 'svelte';
  import { resetOutreach, listOutreachFlyers, getOutreachLuma, ApiError } from '../../lib/api';
  import { lumaByWorkshop, lumaEventLine, lumaStatusLine, OPEN_ON_LUMA } from '../../lib/outreachLuma';
  import type { OutreachLuma } from '../../lib/types';
  import { defaultOpenSection, flyerLinkFor, sectionHeadId } from '../../lib/outreachCards';
  import { linkReportMessage } from '../../lib/outreachLinks';
  import { resultsSummaryLine } from '../../lib/outreachResults';

  // section (#0646): a link from the home timeline names the section to open first.
  let { workshopId, section = null }: { workshopId: number; section?: string | null } = $props();
  let sectionUsed = false;

  let view = $state<WorkshopOutreach | null>(null);
  let reloadKey = $state(0);
  // #0591: the server said this workshop does not exist; show a not-found state, not Loading… and Retry.
  let notFound = $state(false);
  // Set from Start's response, the only one that carries a link report.
  let linkWarning = $state<string | null>(null);

  // This workshop's flyers, so the Instagram card can open the right one (#0588).
  let flyers = $state<FlyerRow[]>([]);
  const flyer = $derived(flyerLinkFor(flyers, workshopId));
  // This event against the Luma calendar (#0648), loaded on its own so a
  // failed read never affects the page.
  let luma = $state<OutreachLuma | null>(null);
  let lumaFailed = $state(false);
  const lumaEntry = $derived(lumaByWorkshop(luma).get(workshopId));
  onMount(async () => {
    try {
      flyers = (await listOutreachFlyers()).flyers;
    } catch {
      // The link then falls back to "Make a flyer" for this workshop.
    }
  });
  onMount(async () => {
    try {
      luma = await getOutreachLuma();
    } catch {
      lumaFailed = true;
    }
  });

  // Exactly one section is open (#0625). Picked once per workshop, so a reload
  // never moves the screen while someone is working.
  let openWave = $state<string | null>(null);
  let openFor: number | null = null;
  let unsavedWaves = $state<Record<string, boolean>>({});

  // Safari has no scroll anchoring, so switching from a long section would
  // otherwise leave the tabs off screen; focus lands on the selected tab.
  async function selectSection(name: string): Promise<void> {
    openWave = name;
    await tick();
    document.getElementById('outreach-section-tabs')?.scrollIntoView({ block: 'start' });
    document.getElementById(sectionHeadId(name))?.focus({ preventScroll: true });
  }

  let resetOpen = $state(false);
  let resetBusy = $state(false);
  let resetError = $state<string | null>(null);
  let resetNote = $state('');

  async function doReset(): Promise<void> {
    resetBusy = true;
    resetError = null;
    try {
      await resetOutreach(workshopId);
      openFor = null;
      focusAfterLoad = true;
      resetOpen = false;
      resetNote = 'Outreach was reset. Write the announcement to start again.';
      reloadKey++;
    } catch (err) {
      resetError = err instanceof ApiError ? err.message : 'Could not reset outreach. Please try again.';
    } finally {
      resetBusy = false;
    }
  }

  // The reset note describes the state right after the reset; the next change
  // (a post, a schedule, a write) makes it stale, so it goes (#0593).
  function changed(): void {
    resetNote = '';
  }
  function reloadAfterChange(): void {
    changed();
    reloadKey++;
  }

  function onLoaded(v: WorkshopOutreach): void {
    view = v;
    if (openFor !== v.workshop.id || !v.waves.some((w) => w.name === openWave)) {
      openFor = v.workshop.id;
      const named = !sectionUsed && section !== null && v.waves.some((w) => w.name === section);
      sectionUsed = true;
      openWave = named ? section : defaultOpenSection(v);
      if (named) {
        void tick().then(() => document.getElementById('outreach-section-tabs')?.scrollIntoView({ block: 'start' }));
        focusAfterLoad = true;
      }
    }
    if (v.link_report) linkWarning = linkReportMessage(v.link_report);
    if (focusAfterLoad) {
      focusAfterLoad = false;
      void focusOpenSection();
    }
  }

  let headingEl = $state<HTMLElement | undefined>();
  let focusAfterLoad = false;
  async function focusOpenSection(): Promise<void> {
    await tick();
    const el = (openWave ? document.getElementById(sectionHeadId(openWave)) : null) ?? headingEl;
    el?.focus({ preventScroll: true });
  }

  const anyUnsaved = $derived(Object.values(unsavedWaves).some(Boolean));
  let leaving = false;
  let pendingLink = $state<HTMLAnchorElement | null>(null);
  function onBeforeUnload(e: BeforeUnloadEvent): void {
    if (!anyUnsaved || leaving) return;
    e.preventDefault();
    e.returnValue = ''; // Safari needs this as well as preventDefault
  }
  function onLinkClick(e: MouseEvent): void {
    if (!anyUnsaved || leaving) return;
    const a = (e.target as Element | null)?.closest?.('a[href]') as HTMLAnchorElement | null;
    if (!a || !asksBeforeLeaving(e, { href: a.href, target: a.target, hasDownload: a.hasAttribute('download') }, window.location)) return;
    e.preventDefault();
    pendingLink = a;
  }
  function leaveAnyway(): void {
    const a = pendingLink;
    pendingLink = null;
    leaving = true;
    a?.click(); // the same link again, now unguarded, so it goes as usual
  }
  function stay(): void {
    const a = pendingLink;
    pendingLink = null;
    a?.focus();
  }
</script>

<svelte:window onbeforeunload={onBeforeUnload} onclickcapture={onLinkClick} />

{#if notFound}
<Panel>
  <h2 class="not-found">Event not found</h2>
  <p>This event no longer exists.</p>
  <p><a href={adminOutreachHash('workshops')}>All outreach</a></p>
</Panel>
{:else}
<div class="row spread heading-row">
  {#if view}
    <h2 class="heading" tabindex="-1" bind:this={headingEl}>{view.workshop.title}</h2>
  {:else}
    <h2 class="heading" tabindex="-1" bind:this={headingEl}>Loading…</h2>
  {/if}
  <span class="row links-row">
    <a href={adminWorkshopHash(workshopId)}>Edit event</a>
    <a href={adminOutreachHash('workshops')}>All outreach</a>
  </span>
</div>
{#if view}
  <p class="text-muted">
    {view.workshop.starts_at ? formatDateTime(view.workshop.starts_at) : 'Date TBA'} · {view.workshop.status}
  </p>
  {#if lumaEntry}
    <p class="text-muted">
      {lumaEventLine(lumaEntry)}{#if lumaEntry.luma_url}{' · '}<a href={lumaEntry.luma_url} target="_blank" rel="noopener noreferrer">{OPEN_ON_LUMA}</a>{/if}
    </p>
  {:else if lumaFailed || (luma && luma.status !== 'ok')}
    <p class="text-muted">{lumaStatusLine(luma, lumaFailed)}</p>
  {/if}
{/if}

<Panel pinnable>
  <OutreachPanel {workshopId} {reloadKey} {onLoaded} onNotFound={() => (notFound = true)} onRecovered={() => void focusOpenSection()} autoStart>
    {#snippet body({ view: v, results, onChange: panelChange })}
      {@const onChange = (p: OutreachPost) => { changed(); panelChange(p); }}
      <OutreachStatusBoard view={v} {openWave} unsaved={unsavedWaves} onSelect={(w) => void selectSection(w)} />
      {#if openWave}<OutreachSectionTabs view={v} selected={openWave} unsaved={unsavedWaves} onSelect={(w) => void selectSection(w)} />{/if}
      {#each v.waves as w (w.name)}
        <OutreachSection view={v} {results} wave={w} {workshopId} open={openWave === w.name} onUnsavedChange={(u) => (unsavedWaves[w.name] = u)} {onChange} reload={reloadAfterChange} {flyer} />
      {/each}
      <div class="row-details row spread">
        <span><strong>Flyers</strong> <span class="text-muted">· print and post at saved locations</span></span>
        <a href={adminOutreachFlyersHash()}>Open flyers</a>
      </div>
      <div class="row-details">
        <p class="row spread">
          <span>
            <strong>Links and clicks</strong>
            <span class="text-muted">· {resultsSummaryLine(results) || 'No clicks yet'}</span>
          </span>
          <a href={adminOutreachResultsHash(workshopId)}>View results</a>
        </p>
        {#if linkWarning}<p class="text-warn">{linkWarning}</p>{/if}
      </div>
      {#if resetNote}<p class="text-muted">{resetNote}</p>{/if}
      {#if v.can_reset}
        <p>
          <Button onclick={() => { resetError = null; resetOpen = true; }}>Reset outreach</Button>
        </p>
      {/if}
    {/snippet}
  </OutreachPanel>
</Panel>

{#if resetOpen}
  <OutreachResetDialog inFlight={resetBusy} errorMessage={resetError} onConfirm={doReset} onClose={() => (resetOpen = false)} />
{/if}
{/if}

{#if pendingLink}
  <OutreachConfirmDialog title="Leave without saving?" inFlight={false} errorMessage={null} confirmLabel="Leave without saving" danger onConfirm={leaveAnyway} onClose={stay}>
    <p>Some text on this page is not saved yet. If you leave now, it is lost.</p>
  </OutreachConfirmDialog>
{/if}

<style>
  .heading-row {
    margin-bottom: var(--space-2);
  }
  .heading {
    font-size: var(--fs-lg);
    margin: 0;
    overflow-wrap: anywhere;
  }
  .heading:focus {
    outline: none;
  }
  .row-details {
    border-top: var(--border-w) solid var(--border);
    padding: var(--space-2) 0;
  }
  .links-row {
    gap: var(--space-3);
  }
</style>
