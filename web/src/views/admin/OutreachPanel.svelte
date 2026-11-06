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

  A failed reload of the workshop already on screen keeps the body mounted, so
  typed text survives (#0625). The banner above it stays pinned to the window
  and stays up until a load succeeds, so the page never passes for current: the
  button that caused the reload can be a long way below the top of the panel.
  While it shows, the section tabs pin below it through --reload-banner-h, and a
  Retry that brings the page back calls onRecovered so focus has somewhere to
  go (#0633). An answer for a workshop no longer on screen is dropped (#0633).
-->
<script lang="ts">
  import { tick, untrack, type Snippet } from 'svelte';
  import { ensureOutreachHandLinks, getWorkshopOutreach, getWorkshopOutreachResults, startOutreach, ApiError } from '../../lib/api';
  import { adminSettingsHash, adminWorkshopHash } from '../../lib/adminRouter';
  import { asSentence, shouldPoll, unavailableMessage } from '../../lib/outreach';
  import { resultsStatusMessage } from '../../lib/outreachResults';
  import { pollDelay, reloadBannerId } from '../../lib/outreachCards';
  import { handLinkReportMessage } from '../../lib/outreachLinks';
  import { eventStarted } from '../../lib/outreachList';
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
    /** Called after Retry brings the page back, since the Retry button has just gone (#0633). */
    onRecovered?: () => void;
  }
  let { workshopId, workshopUpdatedAt = '', reloadKey = 0, onLoaded, onNotFound, autoStart = false, body, onRecovered }: Props = $props();

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

  // #0625: only when the body below is this workshop's, shown from an earlier load.
  const reloadError = $derived(
    loadError && view?.workshop.id === workshopId
      ? `${asSentence(loadError)} What is below may be out of date. Anything you typed is still here.`
      : '',
  );

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

  // #0623: once per workshop per mount, so an outage does not loop; opening the
  // page again tries again. Not awaited by load(): the page and results never
  // wait on ShortLinks. Never for a workshop that has started (#0638); the
  // server refuses too.
  let handLinksTried: number | null = null;
  let handLinkNote = $state('');

  async function ensureHandLinks(id: number): Promise<void> {
    try {
      const report = await ensureOutreachHandLinks(id);
      if (id !== workshopId) return;
      handLinkNote = handLinkReportMessage(report) ?? '';
      if (report.created > 0 || (report.drafted ?? 0) > 0) {
        await refreshQuietly();
        void loadResults();
      }
    } catch {
      if (id !== workshopId) return;
      handLinkNote = 'ShortLinks for hand-posted channels could not be made. Everything else here still works, and opening this page again tries again.';
    }
  }

  async function load(): Promise<void> {
    const id = workshopId;
    notFound = false;
    try {
      const v = await getWorkshopOutreach(id);
      if (id !== workshopId) return;
      view = v;
      // Cleared only now, so a Retry in flight does not pass the old page off as current.
      loadError = null;
      onLoaded?.(view);
    } catch (err) {
      if (id !== workshopId) return;
      if (err instanceof ApiError && err.status === 404) {
        loadError = null;
        notFound = true;
        onNotFound?.();
      } else {
        loadError = message(err, 'Could not load outreach. Please try again.');
      }
    } finally {
      if (id === workshopId) loading = false;
    }
    if (id !== workshopId) return;
    // Only this workshop's own view may start outreach or make links for it (#0633).
    const current = view?.workshop.id === id ? view : null;
    if (autoStart && current && current.available && current.outreach === null && autoStartTried !== id) {
      autoStartTried = id;
      await start();
      return;
    }
    if (autoStart && current?.hand_links_pending && !eventStarted(current.workshop.starts_at) && handLinksTried !== id) {
      handLinksTried = id;
      void ensureHandLinks(id);
    }
    if (current?.outreach) await loadResults();
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
    const id = workshopId;
    try {
      const v = await getWorkshopOutreach(id);
      if (id !== workshopId) return;
      view = v;
      loadError = null;
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
      loadError = null;
      onLoaded?.(view);
      if (view.outreach) await loadResults();
    } catch (err) {
      startError = message(err, 'Could not set up the links. Please try again.');
    } finally {
      starting = false;
    }
  }

  async function retry(): Promise<void> {
    await load();
    if (!loadError) onRecovered?.();
  }

  let bannerEl = $state<HTMLElement | undefined>();
  let bannerH = $state(0);
  // The section tabs pin below the failure banner while it shows (#0633).
  $effect(() => {
    const up = reloadError !== '';
    void tick().then(() => (bannerH = up && bannerEl ? Math.ceil(bannerEl.getBoundingClientRect().height) : 0));
  });

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

<div class="outreach" style:--reload-banner-h="{bannerH}px">
  <div class="reload-banner" id={reloadBannerId} bind:this={bannerEl}>
    <p class="text-error reload-error" role="alert">{reloadError}</p>
    {#if reloadError}<p class="reload-retry"><Button onclick={retry}>Retry</Button></p>{/if}
  </div>
  {#if loading}
    <p class="text-muted">Loading outreach…</p>
  {:else if notFound}
    <!-- The page around this panel says so when it passes onNotFound; there is nothing to retry. -->
    {#if !onNotFound}<p class="text-muted">This event no longer exists.</p>{/if}
  {:else if loadError && view?.workshop.id !== workshopId}
    <p class="text-error">{loadError}</p>
    <Button onclick={retry}>Retry</Button>
  {:else if view}
    {#if !view.available}
      <p class="text-muted">
        {unavailableMessage(view.unavailable_reason)}
        {#if view.unavailable_reason === 'shortlinks_not_configured'}
          <a href={adminSettingsHash('outreach')}>Open Settings</a>
        {:else if view.unavailable_reason === 'workshop_not_published'}
          <a href={adminWorkshopHash(workshopId)}>Open the event editor</a>
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
  <p class="text-warn hand-link-status" role="status">{handLinkNote}</p>
</div>

<style>
  /* Above .wave-pin, which pins to the same edge of the window. Empty, it has no height. */
  .reload-banner {
    position: sticky;
    top: 0;
    z-index: 3;
    background: var(--bg-panel);
  }
  .reload-error {
    margin: 0;
    padding-top: var(--space-2);
  }
  .reload-error:empty {
    display: none;
  }
  .reload-retry {
    margin: 0;
    padding: var(--space-1) 0 var(--space-2);
    border-bottom: var(--border-w) solid var(--border);
  }
  .results-status:empty,
  .hand-link-status:empty {
    display: none;
  }
</style>
