<!--
  The workshop editor's Outreach panel (#0544): a channels x waves grid of
  drafted social posts, with an inline composer below it for the selected
  cell. The server owns the channel and wave lists and every URL; every
  decision (labels, action availability) is a call into lib/outreach.ts, so
  this file is markup and wiring only.

  The composer opens inline under the grid, not as a modal, so it needs no
  focus trap or Escape handling.

  The panel reloads when `workshopUpdatedAt` changes, so the `stale` flag
  refreshes after the workshop above it is saved. Status text goes through
  always-rendered role="status" / role="alert" elements whose content
  changes, per the live-region convention WorkshopEditor.svelte describes.
-->
<script lang="ts">
  import { untrack } from 'svelte';
  import { getWorkshopOutreach, getWorkshopOutreachResults, startOutreach, createOutreachPost, ApiError } from '../../lib/api';
  import { adminOutreachResultsHash } from '../../lib/adminRouter';
  import { buildGrid, cellLabel, cellIsStale, shouldPoll, unavailableMessage } from '../../lib/outreach';
  import { cellClicksLabel, cellReactionsLabel, resultsStatusMessage } from '../../lib/outreachResults';
  import type { OutreachPost, OutreachResults, WorkshopOutreach } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import OutreachComposer from './OutreachComposer.svelte';

  interface Props {
    workshopId: number;
    workshopUpdatedAt: string;
  }
  let { workshopId, workshopUpdatedAt }: Props = $props();

  let view = $state<WorkshopOutreach | null>(null);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let actionError = $state<string | null>(null);
  let busy = $state(false);
  let selectedPostId = $state<number | null>(null);
  // #0546: click results load after the grid, separately, so a ShortLinks or
  // network problem only sets a status line and never hides the grid.
  let results = $state<OutreachResults | null>(null);
  let resultsError = $state<string | null>(null);

  const resultsLine = $derived(
    resultsError ?? (results && results.status !== 'not_started' ? (resultsStatusMessage(results.status) ?? '') : ''),
  );
  const grid = $derived(view ? buildGrid(view) : []);
  const selected = $derived(view?.posts.find((p) => p.id === selectedPostId) ?? null);
  const selectedChannel = $derived(selected && view ? (view.channels.find((c) => c.name === selected.channel) ?? null) : null);
  const selectedWave = $derived(selected && view ? (view.waves.find((w) => w.name === selected.wave) ?? null) : null);

  function message(err: unknown, fallback: string): string {
    return err instanceof ApiError ? err.message : fallback;
  }

  async function loadResults(): Promise<void> {
    resultsError = null;
    try {
      results = await getWorkshopOutreachResults(workshopId);
    } catch {
      results = null;
      resultsError = 'Click counts could not be loaded.';
    }
  }

  async function load(): Promise<void> {
    loadError = null;
    try {
      view = await getWorkshopOutreach(workshopId);
    } catch (err) {
      loadError = message(err, 'Could not load outreach. Please try again.');
    } finally {
      loading = false;
    }
    if (view?.outreach) await loadResults();
  }

  $effect(() => {
    // Re-run on a different workshop or a saved workshop edit.
    void workshopId;
    void workshopUpdatedAt;
    untrack(() => void load());
  });

  // #0545: while a post is queued the publisher is working on it, so refresh
  // the view quietly until it has posted or failed. The interval is cleared on
  // unmount and whenever nothing is queued; a failed refresh is ignored (the
  // next tick tries again) rather than replacing the grid with an error.
  const POLL_MS = 10_000;
  async function refreshQuietly(): Promise<void> {
    try {
      view = await getWorkshopOutreach(workshopId);
    } catch {
      /* keep what is on screen */
    }
  }
  $effect(() => {
    if (!view || !shouldPoll(view)) return;
    const timer = setInterval(() => void refreshQuietly(), POLL_MS);
    return () => clearInterval(timer);
  });

  async function start(): Promise<void> {
    busy = true;
    actionError = null;
    try {
      view = await startOutreach(workshopId);
    } catch (err) {
      actionError = message(err, 'Could not start outreach. Please try again.');
    } finally {
      busy = false;
    }
  }

  async function openCell(channel: string, wave: string, post: OutreachPost | null): Promise<void> {
    actionError = null;
    if (post) {
      selectedPostId = post.id;
      return;
    }
    if (!view?.outreach) return;
    busy = true;
    try {
      const created = await createOutreachPost(view.outreach.id, channel, wave);
      await load();
      selectedPostId = created.id;
    } catch (err) {
      actionError = message(err, 'Could not create the draft. Please try again.');
    } finally {
      busy = false;
    }
  }

  function onChange(updated: OutreachPost): void {
    if (!view) return;
    view = { ...view, posts: view.posts.map((p) => (p.id === updated.id ? updated : p)) };
  }

  async function onDeleted(): Promise<void> {
    selectedPostId = null;
    await load();
  }
</script>

<div class="outreach">
  {#if loading}
    <p class="text-muted">Loading outreach…</p>
  {:else if loadError}
    <p class="text-error">{loadError}</p>
    <Button onclick={load}>Retry</Button>
  {:else if view}
    {#if !view.available}
      <p class="text-muted">{unavailableMessage(view.unavailable_reason)}</p>
    {/if}

    {#if view.outreach === null}
      {#if view.available}
        <p class="text-muted">
          Start outreach to draft a post per channel and wave. This creates a ShortLinks campaign for this workshop.
        </p>
        <div class="row">
          <Button variant="primary" onclick={start} disabled={busy}>
            {busy ? 'Starting…' : 'Start outreach'}
          </Button>
        </div>
      {/if}
    {:else}
      <p class="text-muted">
        Campaign <span class="mono">{view.outreach.campaign_slug}</span>. Pick a cell to draft or open a post.
      </p>
      <div class="table-scroll">
        <table>
          <thead>
            <tr>
              <th>Channel</th>
              {#each view.waves as wave (wave.name)}
                <th>{wave.label}</th>
              {/each}
            </tr>
          </thead>
          <tbody>
            {#each grid as row (row.channel.name)}
              <tr>
                <th scope="row">{row.channel.label}</th>
                {#each row.cells as cell (cell.wave.name)}
                  <td>
                    <button
                      type="button"
                      class="cell"
                      class:active={cell.post !== null && cell.post.id === selectedPostId}
                      disabled={busy}
                      onclick={() => openCell(row.channel.name, cell.wave.name, cell.post)}
                    >
                      {cellLabel(cell.post)}{cellIsStale(cell.post) ? ' (stale)' : ''}
                      {#if cell.post && cellClicksLabel(cell.post, results)}
                        <span class="clicks">{cellClicksLabel(cell.post, results)}</span>
                      {/if}
                      {#if cell.post && cellReactionsLabel(cell.post, results)}
                        <span class="clicks">{cellReactionsLabel(cell.post, results)}</span>
                      {/if}
                    </button>
                  </td>
                {/each}
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      <p><a href={adminOutreachResultsHash(workshopId)}>View results</a></p>
    {/if}

    {#if selected && selectedChannel && selectedWave}
      {#key selected.id}
        <OutreachComposer
          post={selected}
          channel={selectedChannel}
          wave={selectedWave}
          available={view.available}
          {onChange}
          {onDeleted}
        />
      {/key}
    {/if}

    {#if actionError}
      <p class="text-error" role="alert">{actionError}</p>
    {/if}
  {/if}
  <!-- Always rendered, so a late results message is announced. -->
  <p class="text-muted results-status" role="status">{resultsLine}</p>
</div>

<style>
  .mono {
    font-family: var(--font-mono);
  }
  .cell {
    font-family: var(--font);
    font-size: var(--fs-base);
    background: none;
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    padding: var(--space-1) var(--space-2);
    cursor: pointer;
    width: 100%;
    text-align: left;
  }
  .cell:hover:not(:disabled) {
    background: var(--bg-header);
  }
  .cell.active {
    border-color: var(--accent);
  }
  .results-status:empty {
    display: none;
  }
  .clicks {
    display: block;
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  .cell:disabled {
    opacity: 0.5;
    cursor: default;
  }
</style>
