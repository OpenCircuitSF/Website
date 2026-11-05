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
  import Button from '../../lib/Button.svelte';
  import { onMount, tick } from 'svelte';
  import { resetOutreach, listOutreachFlyers, ApiError } from '../../lib/api';
  import { defaultOpenSection, flyerLinkFor, sectionHeadId } from '../../lib/outreachCards';
  import { linkReportMessage } from '../../lib/outreachLinks';
  import { resultsSummaryLine } from '../../lib/outreachResults';

  let { workshopId }: { workshopId: number } = $props();

  let view = $state<WorkshopOutreach | null>(null);
  let reloadKey = $state(0);
  // #0591: the server said this workshop does not exist; show a not-found state, not Loading… and Retry.
  let notFound = $state(false);
  // Set from Start's response, the only one that carries a link report.
  let linkWarning = $state<string | null>(null);

  // This workshop's flyers, so the Instagram card can open the right one (#0588).
  let flyers = $state<FlyerRow[]>([]);
  const flyer = $derived(flyerLinkFor(flyers, workshopId));
  onMount(async () => {
    try {
      flyers = (await listOutreachFlyers()).flyers;
    } catch {
      // The link then falls back to "Make a flyer" for this workshop.
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
      openWave = defaultOpenSection(v);
    }
    if (v.link_report) linkWarning = linkReportMessage(v.link_report);
  }
</script>

{#if notFound}
<Panel>
  <h2 class="not-found">Event not found</h2>
  <p>This event no longer exists.</p>
  <p><a href={adminOutreachHash('workshops')}>All outreach</a></p>
</Panel>
{:else}
<div class="row spread heading-row">
  {#if view}
    <h2 class="heading">{view.workshop.title}</h2>
  {:else}
    <h2 class="heading">Loading…</h2>
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
{/if}

<Panel pinnable>
  <OutreachPanel {workshopId} {reloadKey} {onLoaded} onNotFound={() => (notFound = true)} autoStart>
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

<style>
  .heading-row {
    margin-bottom: var(--space-2);
  }
  .heading {
    font-size: var(--fs-lg);
    margin: 0;
    overflow-wrap: anywhere;
  }
  .row-details {
    border-top: var(--border-w) solid var(--border);
    padding: var(--space-2) 0;
  }
  .links-row {
    gap: var(--space-3);
  }
</style>
