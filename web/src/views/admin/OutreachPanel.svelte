<!--
  The workshop outreach page's loader (#0544, #0582): owns loading the view and
  the click results, polling while a post is queued, and the implicit start.
  The page's layout is the caller's `body` snippet (OutreachWorkshop); the
  channels x waves grid and its inline composer are gone, since every post now
  goes through the cards and the in-app dialog.

  With `autoStart`, opening the page for an available workshop that has no
  outreach yet creates the campaign and links once (Start is idempotent on the
  server); a failure shows a warning with Try again and never loops. GET stays
  read-only. Status text goes through always-rendered role="status" /
  role="alert" elements whose content changes, per the live-region convention
  WorkshopEditor.svelte describes.
-->
<script lang="ts">
  import { untrack, type Snippet } from 'svelte';
  import { getWorkshopOutreach, getWorkshopOutreachResults, startOutreach, ApiError } from '../../lib/api';
  import { adminSettingsHash, adminWorkshopHash } from '../../lib/adminRouter';
  import { shouldPoll, unavailableMessage } from '../../lib/outreach';
  import { resultsStatusMessage } from '../../lib/outreachResults';
  import { pollDelay } from '../../lib/outreachCards';
  import type { OutreachPost, OutreachResults, WorkshopOutreach } from '../../lib/types';
  import Button from '../../lib/Button.svelte';

  interface Props {
    workshopId: number;
    workshopUpdatedAt?: string;
    /** Bump to refetch (the cards and bulk actions change posts). */
    reloadKey?: number;
    /** Called with the view after every successful load, refresh, start or change. */
    onLoaded?: (v: WorkshopOutreach) => void;
    /** Called when the server says this workshop does not exist (404): the page shows its own not-found state and no Retry (#0591). */
    onNotFound?: () => void;
    /** Create the campaign and links once when an available workshop has none (#0582). */
    autoStart?: boolean;
    /** The page layout; the panel hands over the view, the results and the change callback. */
    body: Snippet<[{ view: WorkshopOutreach; results: OutreachResults | null; onChange: (p: OutreachPost) => void }]>;
  }
  let { workshopId, workshopUpdatedAt = '', reloadKey = 0, onLoaded, onNotFound, autoStart = false, body }: Props = $props();

  let view = $state<WorkshopOutreach | null>(null);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let notFound = $state(false);
  let startError = $state<string | null>(null);
  let starting = $state(false);
  // #0546: click results load separately, so a ShortLinks or network problem
  // only sets a status line and never hides the page.
  let results = $state<OutreachResults | null>(null);
  let resultsError = $state<string | null>(null);

  const resultsLine = $derived(
    resultsError ?? (results && results.status !== 'not_started' ? (resultsStatusMessage(results.status) ?? '') : ''),
  );

  function message(err: unknown, fallback: string): string {
    return err instanceof ApiError ? err.message : fallback;
  }

  // #0554: onChange can refetch while an earlier fetch is in flight, so only
  // the newest request may write its answer.
  let resultsSeq = 0;
  async function loadResults(): Promise<void> {
    const seq = ++resultsSeq;
    resultsError = null;
    try {
      const r = await getWorkshopOutreachResults(workshopId);
      if (seq !== resultsSeq) return;
      results = r;
    } catch {
      if (seq !== resultsSeq) return;
      results = null;
      resultsError = 'Click counts could not be loaded.';
    }
  }

  // Once per workshop: a failed start shows Try again instead of looping.
  let autoStartTried: number | null = null;

  async function load(): Promise<void> {
    loadError = null;
    notFound = false;
    try {
      view = await getWorkshopOutreach(workshopId);
      onLoaded?.(view);
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        notFound = true;
        onNotFound?.();
      } else {
        loadError = message(err, 'Could not load outreach. Please try again.');
      }
    } finally {
      loading = false;
    }
    if (autoStart && view && view.available && view.outreach === null && autoStartTried !== workshopId) {
      autoStartTried = workshopId;
      await start();
      return;
    }
    if (view?.outreach) await loadResults();
  }

  $effect(() => {
    // Re-run on a different workshop or a saved workshop edit.
    void workshopId;
    void workshopUpdatedAt;
    void reloadKey;
    untrack(() => void load());
  });

  // #0545, #0581: while a post is queued the publisher is working on it, so
  // refresh the view quietly until it has posted or failed, at 1 s, 2 s, 4 s
  // and then every 10 s (pollDelay). The timer is cleared on unmount and
  // whenever nothing is queued; a failed refresh is ignored (the next tick
  // tries again). pollAttempt is deliberately not reactive; pollTick is what
  // re-arms the timer after a refresh that changed nothing.
  let pollAttempt = 0;
  let pollTick = $state(0);
  async function refreshQuietly(): Promise<void> {
    try {
      view = await getWorkshopOutreach(workshopId);
      onLoaded?.(view);
    } catch {
      /* keep what is on screen */
    }
  }
  $effect(() => {
    void pollTick;
    if (!view || !shouldPoll(view)) {
      pollAttempt = 0;
      return;
    }
    const timer = setTimeout(async () => {
      pollAttempt++;
      await refreshQuietly();
      pollTick++;
    }, pollDelay(pollAttempt));
    return () => clearTimeout(timer);
  });

  async function start(): Promise<void> {
    starting = true;
    startError = null;
    try {
      view = await startOutreach(workshopId);
      onLoaded?.(view);
      if (view.outreach) await loadResults();
    } catch (err) {
      startError = message(err, 'Could not set up the links. Please try again.');
    } finally {
      starting = false;
    }
  }

  function onChange(updated: OutreachPost): void {
    if (!view) return;
    const before = view.posts.find((p) => p.id === updated.id);
    // A post that just became queued starts the fast end of the backoff again.
    if (updated.status === 'queued' && before?.status !== 'queued') pollAttempt = 0;
    view = { ...view, posts: view.posts.map((p) => (p.id === updated.id ? updated : p)) };
    onLoaded?.(view);
    // #0552: a prepare or mark-posted changes what the click line can show.
    if (!before || before.prepared !== updated.prepared || before.status !== updated.status) void loadResults();
  }
</script>

<div class="outreach">
  {#if loading}
    <p class="text-muted">Loading outreach…</p>
  {:else if notFound}
    <!-- The page around this panel says so; there is nothing to retry. -->
  {:else if loadError}
    <p class="text-error">{loadError}</p>
    <Button onclick={load}>Retry</Button>
  {:else if view}
    {#if !view.available}
      <p class="text-muted">
        {unavailableMessage(view.unavailable_reason)}
        {#if view.unavailable_reason === 'shortlinks_not_configured'}
          <a href={adminSettingsHash('outreach')}>Open Settings</a>
        {:else if view.unavailable_reason === 'workshop_not_published'}
          <a href={adminWorkshopHash(workshopId)}>Open the workshop editor</a>
        {/if}
      </p>
    {/if}

    {#if view.outreach === null}
      {#if starting}
        <p class="text-muted">Setting up links…</p>
      {:else if startError}
        <p class="text-warn">{startError}</p>
        <Button onclick={start}>Try again</Button>
      {:else if view.available && !autoStart}
        <p class="text-muted">Outreach has not started.</p>
      {/if}
    {:else}
      {@render body({ view, results, onChange })}
    {/if}
  {/if}
  <!-- Always rendered, so a late results message is announced. -->
  <p class="text-muted results-status" role="status">{resultsLine}</p>
</div>

<style>
  .results-status:empty {
    display: none;
  }
</style>
