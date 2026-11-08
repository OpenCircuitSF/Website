<!--
  The top-level Outreach admin tab (#0544, reshaped by #0586): every published
  upcoming workshop with a status chip and one Promote/Open button, whether or
  not outreach exists yet. Loads when the Workshops sub-view shows; the
  Locations sub-view (#0548) is OutreachLocations.svelte and the Results
  sub-view (#0546) is OutreachResults.svelte.
-->
<script lang="ts">
  import SubNav from '../../lib/SubNav.svelte';
  import { untrack } from 'svelte';
  import { listOutreach, listAdminWorkshops, getOutreachTimeline, getOutreachLuma, ApiError } from '../../lib/api';
  import {
    adminOutreachWorkshopHash,
    adminOutreachHash,
    adminOutreachResultsHash,
    adminOutreachFlyersHash,
    outreachViewFromQuery,
    outreachWorkshopFromQuery,
    outreachFlyerFromQuery,
    outreachLayoutFromQuery,
    outreachSectionFromQuery,
  } from '../../lib/adminRouter';
  import { formatDateTime } from '../../lib/admin';
  import { upcomingPublished, summariesByWorkshop, outreachChip, outreachRowButtonLabel, outreachRowAriaLabel, pastWithOutreach } from '../../lib/outreachList';
  import { lumaByWorkshop, lumaChip, lumaDiffText, lumaTitleNote, lumaStatusLine, OPEN_ON_LUMA } from '../../lib/outreachLuma';
  import type { OutreachSummary, AdminWorkshop, OutreachTimeline as Timeline, OutreachLuma } from '../../lib/types';
  import OutreachTimeline from './OutreachTimeline.svelte';
  import Button from '../../lib/Button.svelte';
  import Panel from '../../lib/Panel.svelte';
  import OutreachLocations from './OutreachLocations.svelte';
  import OutreachResults from './OutreachResults.svelte';
  import OutreachWorkshop from './OutreachWorkshop.svelte';
  import OutreachFlyers from './OutreachFlyers.svelte';
  import OutreachFlyerEditor from './OutreachFlyerEditor.svelte';
  import FlyerPrint from './FlyerPrint.svelte';

  // #0548: the sub-view lives in the hash query (#/outreach?view=locations).
  let { query = new URLSearchParams() }: { query?: URLSearchParams } = $props();
  const view = $derived(outreachViewFromQuery(query));
  const flyerId = $derived(outreachFlyerFromQuery(query));

  let rows = $state<OutreachSummary[]>([]);
  let workshops = $state<AdminWorkshop[]>([]);
  // #0646: the next 30 days on a timeline, above the unchanged list.
  let timeline = $state<Timeline | null>(null);
  const upcoming = $derived(upcomingPublished(workshops));
  const byWorkshop = $derived(summariesByWorkshop(rows));
  const past = $derived(pastWithOutreach(workshops, rows));
  // #0648: each event against the Luma calendar, loaded apart from the list so
  // a slow or failed Luma read never delays or breaks it.
  let luma = $state<OutreachLuma | null>(null);
  let lumaFailed = $state(false);
  const lumaBy = $derived(lumaByWorkshop(luma));
  let loading = $state(true);
  let loadError = $state<string | null>(null);

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      const [o, w, t] = await Promise.all([listOutreach(), listAdminWorkshops(), getOutreachTimeline()]);
      rows = o.outreach;
      workshops = w.workshops;
      timeline = t;
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : 'Could not load outreach. Please try again.';
    } finally {
      loading = false;
    }
  }

  async function loadLuma(): Promise<void> {
    luma = null;
    lumaFailed = false;
    try {
      luma = await getOutreachLuma();
    } catch {
      lumaFailed = true;
    }
  }

  // The workshops list loads whenever it becomes the visible sub-view.
  $effect(() => {
    if (view === 'workshops')
      untrack(() => {
        void load();
        void loadLuma();
      });
  });
</script>

<!-- The print view is chrome-less: it takes over the page and hides this nav. -->
{#if view === 'print' && flyerId !== null}
  <FlyerPrint {flyerId} layout={outreachLayoutFromQuery(query)} />
{:else}
<SubNav label="Outreach sections" items={[
  { href: adminOutreachHash('workshops'), label: 'Posts', current: view === 'workshops' || view === 'workshop' },
  { href: adminOutreachHash('locations'), label: 'Flyer spots', current: view === 'locations' },
  { href: adminOutreachFlyersHash(), label: 'Flyers', current: view === 'flyers' || view === 'print' },
  { href: adminOutreachResultsHash(), label: 'Results', current: view === 'results' }
]} />

{#if view === 'flyers' && flyerId !== null}
  <OutreachFlyerEditor {flyerId} />
{:else if view === 'flyers' || view === 'print'}
  <OutreachFlyers workshopId={outreachWorkshopFromQuery(query)} />
{:else if view === 'workshop' && outreachWorkshopFromQuery(query) !== null}
  <!-- A new workshop is a new page: nothing typed for one may reach another (#0633). -->
  {#key outreachWorkshopFromQuery(query)}
    <OutreachWorkshop workshopId={outreachWorkshopFromQuery(query) ?? 0} section={outreachSectionFromQuery(query)} />
  {/key}
{:else if view === 'locations'}
  <OutreachLocations />
{:else if view === 'results'}
  <OutreachResults workshopId={outreachWorkshopFromQuery(query)} />
{:else}
{#if !loading && !loadError && timeline && timeline.events.length > 0}
  <Panel title="Next 30 days">
    <OutreachTimeline {timeline} />
  </Panel>
{/if}
<Panel title="Outreach">
  {#if loading}
    <p class="text-muted">Loading outreach…</p>
  {:else if loadError}
    <p class="text-error">{loadError}</p>
    <Button variant="primary" onclick={load}>Retry</Button>
  {:else if upcoming.length === 0}
    <p class="text-muted">No upcoming published events. Publish an event with a future date under Events to promote it here.</p>
  {:else}
    <div class="table-scroll">
      <table>
        <thead>
          <tr>
            <th>Event</th>
            <th>Date</th>
            <th>Outreach</th>
            <th>Luma</th>
            <th><span class="sr-only">Action</span></th>
          </tr>
        </thead>
        <tbody>
          {#each upcoming as w (w.id)}
            {@const s = byWorkshop.get(w.id)}
            {@const chip = outreachChip(s)}
            {@const lev = lumaBy.get(w.id)}
            {@const lchip = lumaChip(lev)}
            {@const tnote = lumaTitleNote(lev, w.title)}
            <tr>
              <td>{w.title}</td>
              <td>{w.starts_at ? formatDateTime(w.starts_at) : 'Date TBA'}</td>
              <td><span class="badge badge-{chip.tone}">{chip.label}</span></td>
              <td>
                {#if lev && lchip}
                  <span class="badge badge-{lchip.tone}">{lchip.label}</span>
                  {#if lev.state === 'differs'}
                    {#each lev.diffs as d (d.field)}
                      <div class="luma-note text-muted">{lumaDiffText(d)}</div>
                    {/each}
                  {/if}
                  {#if tnote}<div class="luma-note text-muted">{tnote}</div>{/if}
                  {#if lev.luma_url}
                    <div class="luma-note"><a href={lev.luma_url} target="_blank" rel="noopener noreferrer">{OPEN_ON_LUMA}</a></div>
                  {/if}
                {/if}
              </td>
              <td><a class="row-button" href={adminOutreachWorkshopHash(w.id)} aria-label={outreachRowAriaLabel(s, w.title)}>{outreachRowButtonLabel(s)}</a></td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
  {#if !loading && !loadError}
    <p class="text-muted">{lumaStatusLine(luma, lumaFailed)}</p>
    {#if luma?.leftovers.length}
      <p><strong>On Luma only</strong></p>
      <ul class="luma-leftovers">
        {#each luma.leftovers as l (l.starts_at + l.title)}
          <li>
            {formatDateTime(l.starts_at)} · {l.title}{#if l.luma_url}{' · '}<a href={l.luma_url} target="_blank" rel="noopener noreferrer">{OPEN_ON_LUMA}</a>{/if}
          </li>
        {/each}
      </ul>
    {/if}
  {/if}
</Panel>
{#if !loading && !loadError && past.length > 0}
  <Panel title="Past">
    <p class="text-muted">Events that have happened and had outreach. Open one for its posts, or see how it did under Results.</p>
    <div class="table-scroll">
      <table>
        <thead>
          <tr>
            <th>Event</th>
            <th>Date</th>
            <th>Outreach</th>
            <th><span class="sr-only">Action</span></th>
          </tr>
        </thead>
        <tbody>
          {#each past as w (w.id)}
            {@const s = byWorkshop.get(w.id)}
            {@const chip = outreachChip(s)}
            <tr>
              <td>{w.title}</td>
              <td>{w.starts_at ? formatDateTime(w.starts_at) : 'Date TBA'}</td>
              <td><span class="badge badge-{chip.tone}">{chip.label}</span></td>
              <td>
                <a class="row-button" href={adminOutreachWorkshopHash(w.id)} aria-label={outreachRowAriaLabel(s, w.title)}>{outreachRowButtonLabel(s)}</a>
                <a href={adminOutreachResultsHash(w.id)} aria-label="Results for {w.title}">Results</a>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  </Panel>
{/if}
{/if}
{/if}

<style>
  .luma-note {
    font-size: var(--fs-sm);
    margin-top: var(--space-1);
  }
  .luma-leftovers {
    list-style: none;
    padding-left: 0;
  }
  .row-button {
    display: inline-block;
    padding: var(--space-1) var(--space-3);
    border: var(--border-w) solid var(--accent);
    border-radius: var(--radius);
    color: var(--accent);
    text-decoration: none;
  }
</style>
