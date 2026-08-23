<script lang="ts">
  // One workshop card (#0053, PRD §7.3): title, date, location, summary, and
  // interest tags. Shared by WorkshopsIndex.svelte's two sections and
  // Home.svelte's "next up" section, so the card only exists once. Deliberately
  // thin -- every decision (date formatting, the canceled badge, the location
  // fallback) is a call into lib/workshops.ts, per CLAUDE.md's "SPA logic goes
  // in plain TypeScript modules ... Svelte components stay thin."
  //
  // The title links to /workshops/{slug} (#0054's future detail page). The
  // History API router (#0014) already resolves that path to the
  // 'workshop-detail' route name; until #0054 builds the view, App.svelte's
  // catch-all renders its generic "this page is on the way" placeholder for
  // it, which is an honest state for a link that exists ahead of its target.
  import Panel from './Panel.svelte';
  import { formatWorkshopDate, workshopLocationLabel, workshopBadgeLabel } from './workshops';
  import type { PublicWorkshop } from './types';

  interface Props {
    workshop: PublicWorkshop;
  }

  let { workshop }: Props = $props();

  const badge = $derived(workshopBadgeLabel(workshop));
  const dateLabel = $derived(formatWorkshopDate(workshop.starts_at, workshop.ends_at));
  const locationLabel = $derived(workshopLocationLabel(workshop));
</script>

<Panel noPadding>
  <article class="workshop-card">
    <header class="workshop-card-header">
      <h3 class="workshop-card-title">
        <a href="/workshops/{workshop.slug}">{workshop.title}</a>
      </h3>
      {#if badge}
        <span class="badge badge-danger">{badge}</span>
      {/if}
    </header>

    <p class="workshop-card-meta">
      {dateLabel}
      <span aria-hidden="true"> · </span>
      {locationLabel}
    </p>

    {#if workshop.summary}
      <p class="workshop-card-summary">{workshop.summary}</p>
    {/if}

    {#if workshop.interests.length > 0}
      <ul class="workshop-card-tags" aria-label="Topics">
        {#each workshop.interests as tag (tag.slug)}
          <li class="badge">{tag.name}</li>
        {/each}
      </ul>
    {/if}
  </article>
</Panel>

<style>
  .workshop-card {
    padding: var(--space-4);
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .workshop-card-header {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--space-2);
  }

  .workshop-card-title {
    margin: 0;
    font-size: var(--fs-md);
  }
  .workshop-card-title a {
    color: var(--text);
  }
  .workshop-card-title a:hover {
    color: var(--accent);
  }

  .workshop-card-meta {
    margin: 0;
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }

  .workshop-card-summary {
    margin: 0;
    color: var(--text-muted);
  }

  .workshop-card-tags {
    list-style: none;
    margin: var(--space-1) 0 0;
    padding: 0;
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }
</style>
