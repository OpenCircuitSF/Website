<script lang="ts">
  // WorkshopsIndex — /events (#0053, PRD §5.1/§7.3). Upcoming events in
  // chronological order, past events in reverse chronological order — both
  // orderings come straight from GET /api/workshops
  // (workshops.Store.ListVisible, #0051/#0135); this view does no re-sorting
  // of its own. The index includes canceled workshops alongside published
  // ones (#0135) — WorkshopCard/lib/workshops.ts's isCanceled/
  // workshopBadgeLabel render the "Canceled" badge for them.
  //
  // Past workshops are shown deliberately (see this issue's Notes): they are
  // the evidence that this is a real, recurring group, which is exactly what
  // a first-time visitor arriving from a social link is trying to assess.
  //
  // Loading/empty/error follow the same shape as SubscribeForm.svelte's
  // interest-fetch handling: a plain loading line, a friendly no-retry-button
  // error message, and (for the "nothing scheduled" case) an explicit empty
  // state with a subscribe prompt so a visitor with no workshop to look at
  // still has something to do on the page.
  import { onMount } from 'svelte';
  import TerminalPanel from '../lib/TerminalPanel.svelte';
  import Prompt from '../lib/Prompt.svelte';
  import Panel from '../lib/Panel.svelte';
  import TraceDivider from '../lib/TraceDivider.svelte';
  import WorkshopCard from '../lib/WorkshopCard.svelte';
  import SubscribeForm from '../lib/SubscribeForm.svelte';
  import { listPublicWorkshops } from '../lib/api';
  import { currentRoute } from '../lib/router';
  import { hasNoUpcoming, typeFilterOptions, selectedTypeSlug, filterListByType, isUnknownType } from '../lib/workshops';
  import type { WorkshopsListResponse } from '../lib/types';

  type Status = 'loading' | 'loaded' | 'error';

  let status = $state<Status>('loading');
  let list = $state<WorkshopsListResponse | null>(null);

  onMount(() => {
    listPublicWorkshops()
      .then((res) => {
        list = res;
        status = 'loaded';
      })
      .catch(() => {
        status = 'error';
      });
  });

  // #0614: the filter is read from the URL, so it survives a reload, and a
  // change to it re-derives from the list already fetched.
  const options = $derived(list ? typeFilterOptions(list) : []);
  const selected = $derived(selectedTypeSlug($currentRoute.query));
  const unknownType = $derived(isUnknownType(options, selected));
  const visible = $derived(list ? filterListByType(list, selected) : null);
</script>

<main id="main-content" class="app-shell workshops-shell">
  <TerminalPanel title="events // open_circuit_sf">
    <Prompt text="ls events/" />
    <h1 class="headline" tabindex="-1">Events</h1>
  </TerminalPanel>

  <TraceDivider />

  {#if status === 'loading'}
    <p class="text-muted">Loading events…</p>
  {:else if status === 'error'}
    <p class="text-error">Couldn't load events right now. Try refreshing the page.</p>
  {:else if visible}
    {#if options.length > 0}
      <nav class="type-filter" aria-label="Filter by type">
        <a href="/events" aria-current={selected === null ? 'true' : undefined}>All</a>
        {#each options as option (option.slug)}
          <a
            href="/events?type={encodeURIComponent(option.slug)}"
            aria-current={selected === option.slug ? 'true' : undefined}
          >{option.name}</a>
        {/each}
      </nav>
    {/if}

    {#if unknownType}
    <p>No events of that type. <a href="/events">See all events</a></p>
    {:else}
    <section class="upcoming" aria-labelledby="upcoming-h">
      <h2 id="upcoming-h">Upcoming</h2>
      {#if hasNoUpcoming(visible)}
        <Panel>
          <p>Nothing scheduled yet — subscribe and we'll email you the moment a new event is announced.</p>
          <SubscribeForm />
        </Panel>
      {:else}
        <div class="workshop-cards">
          {#each visible.upcoming as w (w.slug)}
            <WorkshopCard workshop={w} />
          {/each}
        </div>
      {/if}
    </section>

    <TraceDivider />

    <section class="past" aria-labelledby="past-h">
      <h2 id="past-h">Past events</h2>
      {#if visible.past.length === 0}
        <p class="text-muted">{selected === null ? 'No past events yet — this group is just getting started.' : 'No past events of this type.'}</p>
      {:else}
        <div class="workshop-cards">
          {#each visible.past as w (w.slug)}
            <WorkshopCard workshop={w} />
          {/each}
        </div>
      {/if}
    </section>
    {/if}
  {/if}
</main>

<style>
  .workshops-shell {
    display: flex;
    flex-direction: column;
    gap: var(--space-6);
  }

  .headline {
    margin: var(--space-3) 0 0;
    font-family: var(--font);
    font-weight: 800;
    text-transform: uppercase;
    letter-spacing: 0.01em;
    font-size: clamp(24px, 6vw, 36px);
    color: var(--text);
  }

  .type-filter {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
  }
  .type-filter a[aria-current='true'] {
    font-weight: 700;
    color: var(--text);
  }

  .upcoming h2,
  .past h2 {
    font-size: var(--fs-lg);
    margin: 0 0 var(--space-3);
  }

  .workshop-cards {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
    gap: var(--space-4);
  }
</style>
