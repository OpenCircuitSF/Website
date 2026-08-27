<script lang="ts">
  // WorkshopsIndex — /workshops (#0053, PRD §5.1/§7.3). Upcoming workshops in
  // chronological order, past workshops in reverse chronological order — both
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
  import { hasNoUpcoming } from '../lib/workshops';
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
</script>

<main id="main-content" class="app-shell workshops-shell">
  <TerminalPanel title="workshops // open_circuit_sf">
    <Prompt text="ls workshops/" />
    <h1 class="headline" tabindex="-1">Workshops</h1>
  </TerminalPanel>

  <TraceDivider />

  {#if status === 'loading'}
    <p class="text-muted">Loading workshops…</p>
  {:else if status === 'error'}
    <p class="text-error">Couldn't load workshops right now. Try refreshing the page.</p>
  {:else if list}
    <section class="upcoming" aria-labelledby="upcoming-h">
      <h2 id="upcoming-h">Upcoming</h2>
      {#if hasNoUpcoming(list)}
        <Panel>
          <p>Nothing scheduled yet — subscribe and we'll email you the moment a new workshop is announced.</p>
          <SubscribeForm />
        </Panel>
      {:else}
        <div class="workshop-cards">
          {#each list.upcoming as w (w.slug)}
            <WorkshopCard workshop={w} />
          {/each}
        </div>
      {/if}
    </section>

    <TraceDivider />

    <section class="past" aria-labelledby="past-h">
      <h2 id="past-h">Past workshops</h2>
      {#if list.past.length === 0}
        <p class="text-muted">No past workshops yet — this group is just getting started.</p>
      {:else}
        <div class="workshop-cards">
          {#each list.past as w (w.slug)}
            <WorkshopCard workshop={w} />
          {/each}
        </div>
      {/if}
    </section>
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
