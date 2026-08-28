<script lang="ts">
  // ArchiveIndex — /archive (#0123, PRD §6.8). Every SENT, published
  // campaign, reverse chronological — GET /api/archive
  // (mailing.CampaignStore.ListArchived's own ordering; this view does no
  // re-sorting of its own). This is the only recurring indexable content
  // the site has (no blog by design, PRD §2), and it doubles as proof for a
  // prospect that this is a real, ongoing group before they subscribe —
  // the same "past workshops are shown deliberately" reasoning
  // WorkshopsIndex.svelte's own doc comment gives, restated for campaigns.
  //
  // Loading/empty/error follow WorkshopsIndex.svelte's identical shape.
  import { onMount } from 'svelte';
  import TerminalPanel from '../lib/TerminalPanel.svelte';
  import Prompt from '../lib/Prompt.svelte';
  import Panel from '../lib/Panel.svelte';
  import TraceDivider from '../lib/TraceDivider.svelte';
  import SubscribeForm from '../lib/SubscribeForm.svelte';
  import { listArchive } from '../lib/api';
  import { formatArchivedDate, hasNoArchiveEntries } from '../lib/archive';
  import type { ArchiveListResponse } from '../lib/types';

  type Status = 'loading' | 'loaded' | 'error';

  let status = $state<Status>('loading');
  let list = $state<ArchiveListResponse | null>(null);

  onMount(() => {
    listArchive()
      .then((res) => {
        list = res;
        status = 'loaded';
      })
      .catch(() => {
        status = 'error';
      });
  });
</script>

<main id="main-content" class="app-shell archive-shell">
  <TerminalPanel title="archive // open_circuit_sf">
    <Prompt text="ls archive/" />
    <h1 class="headline" tabindex="-1">Archive</h1>
    <p class="text-muted lede">Past emails from Open Circuit SF.</p>
  </TerminalPanel>

  <TraceDivider />

  {#if status === 'loading'}
    <p class="text-muted">Loading archive…</p>
  {:else if status === 'error'}
    <p class="text-error">Couldn't load the archive right now. Try refreshing the page.</p>
  {:else if list}
    {#if hasNoArchiveEntries(list)}
      <Panel>
        <p>Nothing archived yet — subscribe and we'll email you the moment there's something to read.</p>
        <SubscribeForm />
      </Panel>
    {:else}
      <ul class="archive-list">
        {#each list.archive as entry (entry.slug)}
          <li class="archive-row">
            <a class="archive-link" href="/archive/{entry.slug}">
              <span class="archive-subject">{entry.subject}</span>
              {#if entry.archived_at}
                <span class="archive-date">{formatArchivedDate(entry.archived_at)}</span>
              {/if}
            </a>
            {#if entry.preheader}
              <p class="archive-preheader">{entry.preheader}</p>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  {/if}
</main>

<style>
  .archive-shell {
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

  .lede {
    margin: var(--space-2) 0 0;
  }

  .archive-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: var(--space-4);
  }

  .archive-row {
    border-bottom: var(--border-w) solid var(--border);
    padding-bottom: var(--space-4);
  }
  .archive-row:last-child {
    border-bottom: none;
  }

  .archive-link {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--space-2);
    color: var(--text);
    text-decoration: none;
  }
  .archive-link:hover .archive-subject {
    color: var(--accent);
    text-decoration: underline;
  }

  .archive-subject {
    font-weight: 700;
    font-size: var(--fs-lg);
  }

  .archive-date {
    color: var(--text-muted);
    font-size: var(--fs-sm);
  }

  .archive-preheader {
    margin: var(--space-1) 0 0;
    color: var(--text-muted);
  }
</style>
