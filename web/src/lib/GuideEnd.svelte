<script lang="ts">
  // GuideEnd — the end of every guide page (#0662): the next few events
  // (related ones first) and the newsletter signup, attributed to this guide.
  // The heading, intro and empty-state copy are written literally in the
  // markup below: internal/seo's TestGuideEndFallbackCopyMatchesSvelte reads
  // this file's source and checks the server fallback against it.
  //
  // The form mounts only after the workshops fetch settles, because
  // SubscribeForm seeds its selection from `preselect` once, at mount.
  import { onMount } from 'svelte';
  import { listPublicWorkshops } from './api';
  import type { GuideKind } from './guides';
  import { guidePreselectInterests, guideUpcomingEvents } from './guideEnd';
  import type { PublicWorkshop } from './types';
  import SubscribeForm from './SubscribeForm.svelte';
  import WorkshopCard from './WorkshopCard.svelte';

  interface Props {
    kind: GuideKind;
    slug: string;
  }

  let { kind, slug }: Props = $props();

  let status = $state<'loading' | 'ok' | 'error'>('loading');
  let events = $state<PublicWorkshop[]>([]);
  let preselect = $state<string[]>([]);
  let workshopsSettled = $state(false);

  onMount(() => {
    listPublicWorkshops()
      .then((res) => {
        events = guideUpcomingEvents(res, kind, slug);
        preselect = guidePreselectInterests(res, kind, slug);
        status = 'ok';
      })
      .catch(() => {
        preselect = [];
        status = 'error';
      })
      .finally(() => {
        workshopsSettled = true;
      });
  });
</script>

<section class="guide-events" aria-labelledby="guide-events-h">
  <h2 id="guide-events-h">Upcoming events</h2>
  {#if status === 'ok'}
    {#if events.length === 0}
      <p class="text-muted">Nothing scheduled right now. Join the newsletter below and we'll tell you when the next one is announced.</p>
    {:else}
      <div class="workshop-cards">
        {#each events as w (w.slug)}
          <WorkshopCard workshop={w} />
        {/each}
      </div>
    {/if}
  {/if}
  <p class="guide-events-link"><a href="/events">See all events →</a></p>
</section>

<section class="guide-signup" aria-labelledby="guide-signup-h">
  <h2 id="guide-signup-h">Get the newsletter</h2>
  <p class="text-muted">New guides and upcoming workshops by email. Pick the topics you want, and unsubscribe any time.</p>
  {#if workshopsSettled}
    <SubscribeForm guide={slug} {preselect} />
  {/if}
</section>

<style>
  .guide-events,
  .guide-signup {
    margin: var(--space-6) 0;
  }

  .guide-events h2,
  .guide-signup h2 {
    font-size: var(--fs-lg);
    margin: 0 0 var(--space-3);
  }

  .guide-signup p {
    max-width: var(--measure);
    margin: 0 0 var(--space-4);
  }

  .workshop-cards {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
    gap: var(--space-4);
  }

  .guide-events-link {
    margin: var(--space-3) 0 0;
  }
</style>
