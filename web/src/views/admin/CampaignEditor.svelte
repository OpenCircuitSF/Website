<!--
  The campaign compose editor (#0047): fields, live preview, audience panel,
  pre-send checks, test-send control, and the guarded send/cancel actions.

  Markup and wiring only. Every decision — what the editor may offer, what
  the pre-send panel says, whether the Send control is blocked, what the
  audience count reads — is a call into lib/campaigns.ts, lib/preflight.ts,
  lib/audience.ts, or lib/sendConfirm.ts, or a value taken straight off a
  server response. No comparison operator, `.trim()`, `.length >`, status
  literal, or `||` fallback chain is written directly in this file's markup
  — see #0047's plan §2 for why (#0094: nothing in a .svelte file is
  covered by a test).

  Autosave, not a live unsaved preview: POST .../preview and GET
  .../audience both read the STORED campaign row (#0046/#0044's own
  contracts), so a live side-by-side preview of unsaved text is impossible
  without letting an operator preview one thing and send another — the
  single trap this screen exists to prevent. The editor PATCHes when the
  buffer is dirty and idle ~2s, on blur, and on an explicit "Save draft"
  button; a successful save refreshes the preview and pre-send panel. Mode
  and interest changes save IMMEDIATELY (discrete clicks, not typing) and
  additionally refresh the audience count.
-->
<script lang="ts">
  import { onMount, tick } from 'svelte';
  import {
    getCampaign,
    updateCampaign,
    getCampaignAudience,
    getCampaignPreflight,
    previewCampaign,
    testSendCampaign,
    sendCampaign,
    cancelCampaign,
    listInterests,
    ApiError,
  } from '../../lib/api';
  import {
    canEditCampaign,
    canSendCampaign,
    canCancelCampaign,
    interestsApplyToMode,
    AUDIENCE_MODES,
    subjectLengthAdvice,
    preheaderLengthAdvice,
    wasDemotedAfterScheduling,
    demotionExplanation,
    cancelCopy,
  } from '../../lib/campaigns';
  import { parseUnmetFromError, fixLocation } from '../../lib/preflight';
  import {
    audienceRequestKey,
    isStaleAudience,
    describeAudience,
    sampleCaption,
    audienceErrorMessage,
  } from '../../lib/audience';
  import { sendGuardState } from '../../lib/sendConfirm';
  import { subscribeEvent } from '../../lib/events';
  import {
    CAMPAIGN_PROGRESS_EVENT,
    isProgressForCampaign,
    isTerminalSnapshot,
    createResyncSequencer,
    shouldShowProgress,
    progressHeading,
    progressPercent,
    formatProgressDetail,
    progressVerdict,
    remainingForCancel,
    type CampaignProgress,
  } from '../../lib/campaignProgress';
  import type { Campaign, PreflightResponse, AudiencePreviewResponse, CampaignPreviewResponse, UnmetRequirement, Interest } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import Panel from '../../lib/Panel.svelte';
  import CampaignSendDialog from './CampaignSendDialog.svelte';

  interface Props {
    campaignId: number;
    onBack: () => void;
    onGoToSettings: () => void;
    // #0114: takes the campaign id — the caller opens the audit tab
    // pre-filtered to this campaign's own history.
    onGoToAudit: (campaignId: number) => void;
  }

  let { campaignId, onBack, onGoToSettings, onGoToAudit }: Props = $props();

  // ── Load state ──────────────────────────────────────────────────────────
  let campaign = $state<Campaign | null>(null);
  let loading = $state(true);
  let loadError = $state<string | null>(null);

  // ── Editable buffer ─────────────────────────────────────────────────────
  let name = $state('');
  let subject = $state('');
  let preheader = $state('');
  let bodyMd = $state('');
  let mode = $state('all');
  let interestIds = $state<number[]>([]);
  let taxonomyInterests = $state<Interest[]>([]);

  // ── Save state ──────────────────────────────────────────────────────────
  let dirty = $state(false);
  let saveState = $state<'idle' | 'saving' | 'saved' | 'error'>('idle');
  let saveError = $state<string | null>(null);
  let savedAtLabel = $state('');
  let autosaveTimer: ReturnType<typeof setTimeout> | undefined;

  // ── Preview ─────────────────────────────────────────────────────────────
  let preview = $state<CampaignPreviewResponse | null>(null);
  let previewError = $state<string | null>(null);
  let previewTab = $state<'html' | 'text'>('html');

  // ── Pre-send checks ─────────────────────────────────────────────────────
  let unmet = $state<UnmetRequirement[] | null>(null);
  let preflightSummary = $state<PreflightResponse['summary'] | null>(null);
  let preflightError = $state<string | null>(null);

  // ── Audience ────────────────────────────────────────────────────────────
  let audiencePreview = $state<AudiencePreviewResponse | null>(null);
  let audienceError = $state<string | null>(null);

  // ── Test send ───────────────────────────────────────────────────────────
  let testSending = $state(false);
  let testSendMessage = $state<string | null>(null);

  // ── Send dialog ─────────────────────────────────────────────────────────
  let sendDialogOpen = $state(false);
  let sendInFlight = $state(false);
  let sendError = $state<string | null>(null);
  let sendUnmet = $state<UnmetRequirement[]>([]);

  // ── Cancel dialog ───────────────────────────────────────────────────────
  let cancelDialogOpen = $state(false);
  let canceling = $state(false);
  let cancelError = $state<string | null>(null);

  // ── Live send progress (#0048) ──────────────────────────────────────────
  // progress/progressUpdatedAt hold the latest "campaign.progress" frame for
  // THIS campaign (subscribeEvent delivers every connected admin's frames
  // for every campaign — filtering to this one happens in the handler
  // below via lib/campaignProgress's isProgressForCampaign, never inline
  // here). nowTick exists solely so progressVerdict's derived value stays
  // fresh with wall-clock time passing, not just on a new frame arriving —
  // ticking it is the only way a "stalled" verdict can appear without a
  // NEW event (the absence of one is exactly what "stalled" means).
  let progress = $state<CampaignProgress | null>(null);
  let progressUpdatedAt = $state<number | null>(null);
  let nowTick = $state(Date.now());
  let progressTicker: ReturnType<typeof setInterval> | undefined;
  // Orders the overlapping campaign re-reads onProgressEvent/onOpen can start
  // at once — see resyncCampaignStatus and lib/campaignProgress's
  // createResyncSequencer for why last-started must win.
  const resyncSeq = createResyncSequencer();

  // ── Focus targets ───────────────────────────────────────────────────────
  let headingEl = $state<HTMLHeadingElement | null>(null);
  let preflightHeadingEl = $state<HTMLHeadingElement | null>(null);
  let sendButtonEl = $state<HTMLButtonElement | null>(null);

  // ── Derived, template-safe values (every decision routes through lib/) ──
  let editable = $derived(campaign ? canEditCampaign(campaign.status) : false);
  let sendOffered = $derived(campaign ? canSendCampaign(campaign.status) : false);
  let cancelOffered = $derived(campaign ? canCancelCampaign(campaign.status) : false);
  let interestsEnabled = $derived(interestsApplyToMode(mode));
  let demoted = $derived(campaign ? wasDemotedAfterScheduling(campaign) : false);
  let demotionMessage = $derived(campaign ? demotionExplanation(campaign) : '');
  let subjectAdvice = $derived(subjectLengthAdvice(subject.length));
  let preheaderAdvice = $derived(preheaderLengthAdvice(preheader.length));
  let unmetDisplay = $derived(
    (unmet ?? []).map((item) => ({ ...item, fix: fixLocation(item.code) })),
  );
  let audienceDescription = $derived(audiencePreview ? describeAudience(audiencePreview) : '—');
  let audienceSampleCaption = $derived(audiencePreview ? sampleCaption(audiencePreview) : '');
  // #0048: the remaining-recipient count cancelCopy('sending') otherwise
  // omits, sourced from the live progress stream via remainingForCancel
  // (undefined — not 0 — when not sending or no matching snapshot yet, so
  // cancelCopy falls back to its original no-digits wording).
  let cancelRemaining = $derived(remainingForCancel(campaign?.status ?? '', campaignId, progress));
  let cancelMessage = $derived(campaign ? cancelCopy(campaign.status, cancelRemaining) : '');

  // ── Live send progress (#0048) ────────────────────────────────────────
  let progressForThisCampaign = $derived(
    progress !== null && isProgressForCampaign(progress, campaignId) ? progress : null,
  );
  let progressVisible = $derived(
    shouldShowProgress(campaign?.status ?? '', progressForThisCampaign !== null),
  );
  let progressHeadingText = $derived(progressHeading(campaign?.status ?? ''));
  let progressDetailText = $derived(progressForThisCampaign ? formatProgressDetail(progressForThisCampaign) : '');
  let progressPercentValue = $derived(progressForThisCampaign ? progressPercent(progressForThisCampaign) : 0);
  let progressVerdictValue = $derived(progressVerdict(campaign?.status ?? '', progressUpdatedAt, nowTick));
  let progressStalled = $derived(progressVerdictValue === 'stalled');
  let htmlTabSelected = $derived(previewTab === 'html');
  // A stable, template-safe stand-in confirmation string equal to the live
  // count itself — this editor-level gate answers "is everything besides
  // the operator's confirmation ready", never the real confirmation (that
  // lives inside CampaignSendDialog, which re-evaluates sendGuardState
  // against what the operator actually typed).
  let editorGuard = $derived(
    sendGuardState({
      status: campaign?.status ?? '',
      unmet: unmet ?? [],
      audienceCount: audiencePreview?.count ?? 0,
      confirmRaw: String(audiencePreview?.count ?? 0),
      inFlight: false,
    }),
  );
  let sendGateOpen = $derived(editorGuard.kind === 'ready');
  let isSaving = $derived(saveState === 'saving');
  let isSaved = $derived(saveState === 'saved');
  let modeOptions = $derived(AUDIENCE_MODES.map((opt) => ({ ...opt, selected: opt.value === mode })));
  let interestOptions = $derived(
    taxonomyInterests.map((it) => ({ ...it, selected: interestIds.includes(it.id) })),
  );
  let interestsFieldsetDisabled = $derived(!interestsEnabled || !editable);
  let audienceWarningsList = $derived(audiencePreview?.warnings ?? []);
  let audienceSampleList = $derived(audiencePreview?.sample ?? []);
  let hasAudienceWarnings = $derived(audienceWarningsList.length > 0);
  let hasAudienceSample = $derived(audienceSampleList.length > 0);
  let preflightUnknown = $derived(unmet === null);
  let preflightClear = $derived(unmet !== null && unmetDisplay.length === 0);

  // ── Loading ─────────────────────────────────────────────────────────────
  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      const c = await getCampaign(campaignId);
      campaign = c;
      name = c.name;
      subject = c.subject;
      preheader = c.preheader ?? '';
      bodyMd = c.body_md;
      mode = c.audience_mode;
      interestIds = c.interest_ids;
      dirty = false;
      await Promise.all([refreshPreview(), refreshPreflight(), refreshAudience()]);
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : 'Could not load this campaign.';
    } finally {
      loading = false;
    }
  }

  async function loadInterests(): Promise<void> {
    try {
      const resp = await listInterests();
      taxonomyInterests = resp.interests;
    } catch {
      taxonomyInterests = [];
    }
  }

  // ── Live send progress wiring (#0048) ──────────────────────────────────
  // onProgressEvent/resyncCampaignStatus are the only two things that touch
  // progress/progressUpdatedAt/campaign from the stream — every decision
  // about what to DO with a frame (does it belong to this campaign, does
  // the region show, what it says) is a lib/campaignProgress.ts call read
  // through the $derived values above, never decided here.
  //
  // #0048's review, point 1: campaign.status was previously refreshed only
  // by load(), a stream (re)connect (resyncCampaignStatus below), and
  // explicit mutations — never by a progress frame itself. On a healthy
  // connection (the normal case) that meant the closing frame updated the
  // counts but left campaign.status stuck on 'sending' forever:
  // progressHeading('sending') kept showing "Sending…" over a completed
  // send's own final numbers, and 30s later progressVerdict added a false
  // "stalled" warning. isTerminalSnapshot (lib/campaignProgress.ts) is the
  // fix: a frame that may be the last one for this send triggers the same
  // resync a reconnect does, so the heading/verdict move off 'sending' the
  // moment the send ends rather than waiting for a connection drop that, on a
  // healthy stream, never happens. That predicate reads the frame's own
  // campaign status rather than its counts (#0048's second review, point 3 —
  // a failed campaign stops with rows still queued, so `remaining` never
  // reaches 0 there); see its doc comment.
  function onProgressEvent(p: CampaignProgress): void {
    if (!isProgressForCampaign(p, campaignId)) {
      return; // broadcast to every admin, for every campaign — not ours.
    }
    progress = p;
    progressUpdatedAt = Date.now();
    if (isTerminalSnapshot(p)) {
      void resyncCampaignStatus();
    }
  }

  // Re-reads the campaign's status (not the editable buffer — see this
  // function's own scope) on the stream's initial connect, every automatic
  // reconnect, and — since #0048's review, point 1 — every progress frame
  // that isTerminalSnapshot reports as possibly closing (onProgressEvent
  // above). The database is the source of truth, not this stream (CLAUDE.md):
  // a campaign can finish/fail/be cancelled entirely during a connection gap
  // with no further batch to publish a closing frame, so relying solely on
  // reconnect-driven resyncs could leave this view showing "Sending…" for as
  // long as the connection happened to stay up — which, on a healthy
  // connection, is forever. (worker.go's failCampaign publishes a closing
  // snapshot itself, so the terminal-frame path covers that case too, not
  // only CompleteIfDone's.) Deliberately narrower than load(): it only
  // replaces `campaign`, never the name/subject/preheader/bodyMd/mode/
  // interestIds editable buffer, so an operator's in-progress (unsaved) edit
  // is never clobbered by a background resync. Best-effort: a failure here
  // just means the next progress frame or the operator's own next action
  // refreshes it instead.
  //
  // #0048's second review, point 5: two of these really are in flight at once
  // on the normal successful-send path, and if the earlier one's response
  // (which can predate CompleteIfDone's commit, and therefore still say
  // 'sending') were applied last, the view would revert to "Sending…" with no
  // further frames coming. resyncSeq makes the assignment monotonic in START
  // order — a superseded response is discarded, never applied late. The
  // request itself is still allowed to run: it is the LATER one that carries
  // the terminal status, so "skip if one is in flight" would drop the wrong
  // one.
  async function resyncCampaignStatus(): Promise<void> {
    const token = resyncSeq.begin();
    try {
      const fresh = await getCampaign(campaignId);
      if (!resyncSeq.isCurrent(token)) {
        return; // a newer resync started after this one — its answer wins.
      }
      campaign = fresh;
    } catch {
      // Best-effort — see this function's doc comment.
    }
  }

  onMount(() => {
    void load();
    void loadInterests();
    void tick().then(() => headingEl?.focus());

    const unsubscribeProgress = subscribeEvent<CampaignProgress>(
      CAMPAIGN_PROGRESS_EVENT,
      onProgressEvent,
      undefined,
      () => void resyncCampaignStatus(),
    );
    // Keeps progressVerdict's "stalled" reading current even when no new
    // frame ever arrives to trigger a re-render on its own — see nowTick's
    // own doc comment above.
    progressTicker = setInterval(() => {
      nowTick = Date.now();
    }, 5000);

    return () => {
      if (autosaveTimer) clearTimeout(autosaveTimer);
      unsubscribeProgress();
      if (progressTicker) clearInterval(progressTicker);
    };
  });

  // ── Save ────────────────────────────────────────────────────────────────
  function scheduleAutosave(): void {
    dirty = true;
    if (autosaveTimer) clearTimeout(autosaveTimer);
    autosaveTimer = setTimeout(() => {
      void saveDraft();
    }, 2000);
  }

  function handleBlur(): void {
    if (dirty) void saveDraft();
  }

  async function saveDraft(): Promise<void> {
    if (!campaign || !editable) return;
    saveState = 'saving';
    saveError = null;
    try {
      const updated = await updateCampaign(campaignId, {
        name,
        subject,
        preheader: preheader === '' ? undefined : preheader,
        body_md: bodyMd,
        audience_mode: mode,
        interest_ids: interestIds,
      });
      campaign = updated;
      dirty = false;
      saveState = 'saved';
      savedAtLabel = new Date().toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
      await Promise.all([refreshPreview(), refreshPreflight()]);
    } catch (err) {
      saveState = 'error';
      saveError = err instanceof ApiError ? err.message : 'Could not save this campaign.';
    }
  }

  async function saveNowAndRefreshAudience(): Promise<void> {
    if (autosaveTimer) clearTimeout(autosaveTimer);
    await saveDraft();
    await refreshAudience();
  }

  function onModeChange(value: string): void {
    mode = value;
    if (!interestsApplyToMode(value)) {
      interestIds = [];
    }
    void saveNowAndRefreshAudience();
  }

  function toggleInterest(id: number): void {
    interestIds = interestIds.includes(id) ? interestIds.filter((x) => x !== id) : [...interestIds, id];
    void saveNowAndRefreshAudience();
  }

  // ── Preview / pre-send / audience refresh ──────────────────────────────
  async function refreshPreview(): Promise<void> {
    try {
      preview = await previewCampaign(campaignId);
      previewError = null;
    } catch (err) {
      preview = null;
      previewError = err instanceof ApiError ? err.message : 'Could not render a preview.';
    }
  }

  async function refreshPreflight(): Promise<void> {
    try {
      const resp = await getCampaignPreflight(campaignId);
      unmet = resp.unmet;
      preflightSummary = resp.summary;
      preflightError = null;
    } catch (err) {
      unmet = null;
      preflightSummary = null;
      preflightError = err instanceof ApiError ? err.message : 'Could not check pre-send requirements.';
    }
  }

  async function refreshAudience(): Promise<void> {
    const requestedKey = audienceRequestKey(mode, interestIds);
    try {
      const resp = await getCampaignAudience(campaignId);
      const responseKey = audienceRequestKey(resp.mode, resp.interest_ids);
      if (isStaleAudience(responseKey, audienceRequestKey(mode, interestIds))) {
        return;
      }
      audiencePreview = resp;
      audienceError = null;
    } catch (err) {
      if (isStaleAudience(requestedKey, audienceRequestKey(mode, interestIds))) {
        return;
      }
      audiencePreview = null;
      audienceError = audienceErrorMessage(err);
    }
  }

  function reCheck(): void {
    void refreshPreflight();
    void refreshAudience();
  }

  // ── Test send ───────────────────────────────────────────────────────────
  async function onTestSend(): Promise<void> {
    testSending = true;
    testSendMessage = null;
    try {
      campaign = await testSendCampaign(campaignId);
      testSendMessage = 'Test message sent to your own inbox.';
      await refreshPreflight();
    } catch (err) {
      testSendMessage = err instanceof ApiError ? err.message : 'Could not send a test message.';
    } finally {
      testSending = false;
    }
  }

  // ── Send ────────────────────────────────────────────────────────────────
  function onOpenSend(): void {
    if (!sendGateOpen) {
      void tick().then(() => preflightHeadingEl?.focus());
      return;
    }
    sendError = null;
    sendUnmet = [];
    sendDialogOpen = true;
  }

  async function onConfirmSend(confirmCount: number): Promise<void> {
    sendInFlight = true;
    sendError = null;
    try {
      campaign = await sendCampaign(campaignId, confirmCount);
      sendDialogOpen = false;
      await tick();
      sendButtonEl?.focus();
    } catch (err) {
      const parsed = parseUnmetFromError(err);
      if (parsed !== null) {
        sendUnmet = parsed;
        unmet = parsed;
        sendError = null;
      } else {
        sendUnmet = [];
        sendError = err instanceof ApiError ? err.message : 'Could not send this campaign.';
      }
    } finally {
      sendInFlight = false;
    }
  }

  function onCloseSend(): void {
    if (sendInFlight) return;
    sendDialogOpen = false;
    void tick().then(() => sendButtonEl?.focus());
  }

  // ── Cancel ──────────────────────────────────────────────────────────────
  function openCancel(): void {
    cancelError = null;
    cancelDialogOpen = true;
  }

  function closeCancel(): void {
    if (canceling) return;
    cancelDialogOpen = false;
  }

  async function onConfirmCancel(): Promise<void> {
    canceling = true;
    cancelError = null;
    try {
      campaign = await cancelCampaign(campaignId);
      cancelDialogOpen = false;
    } catch (err) {
      cancelError = err instanceof ApiError ? err.message : 'Could not cancel this campaign.';
    } finally {
      canceling = false;
    }
  }

  function goToFix(section: string): void {
    if (section === 'settings') {
      onGoToSettings();
    }
  }
</script>

<div class="campaign-editor">
  <div class="editor-toolbar">
    <Button onclick={onBack}>&larr; Back to campaigns</Button>
  </div>

  {#if loading}
    <p class="text-muted" role="status">Loading campaign…</p>
  {:else if loadError}
    <p class="text-error" role="alert">{loadError}</p>
    <Button variant="primary" onclick={load}>Retry</Button>
  {:else if campaign}
    <h2 class="editor-heading" tabindex="-1" bind:this={headingEl}>{campaign.name}</h2>

    {#if demoted}
      <div class="demoted-banner" role="status">
        <p>{demotionMessage}</p>
        <Button variant="subtle" onclick={() => onGoToAudit(campaignId)}>View in audit log</Button>
      </div>
    {/if}

    <Panel title="Content">
      <div class="field">
        <label for="campaign-name">Name (internal)</label>
        <input
          id="campaign-name"
          type="text"
          bind:value={name}
          disabled={!editable}
          oninput={scheduleAutosave}
          onblur={handleBlur}
        />
      </div>
      <div class="field">
        <label for="campaign-subject">Subject</label>
        <input
          id="campaign-subject"
          type="text"
          bind:value={subject}
          disabled={!editable}
          oninput={scheduleAutosave}
          onblur={handleBlur}
        />
        <p class="length-advice {subjectAdvice.tone}">{subjectAdvice.message}</p>
      </div>
      <div class="field">
        <label for="campaign-preheader">Preheader (optional)</label>
        <input
          id="campaign-preheader"
          type="text"
          bind:value={preheader}
          disabled={!editable}
          oninput={scheduleAutosave}
          onblur={handleBlur}
        />
        <p class="length-advice {preheaderAdvice.tone}">{preheaderAdvice.message}</p>
      </div>
      <div class="field">
        <label for="campaign-body">Body (Markdown)</label>
        <textarea
          id="campaign-body"
          class="mono body-textarea"
          rows="16"
          bind:value={bodyMd}
          disabled={!editable}
          oninput={scheduleAutosave}
          onblur={handleBlur}
        ></textarea>
      </div>
      <div class="row save-row">
        <Button variant="primary" disabled={!editable} onclick={saveDraft}>Save draft</Button>
        {#if isSaving}
          <span class="text-muted" aria-live="off">Saving…</span>
        {:else if isSaved}
          <span class="text-muted" aria-live="off">Saved {savedAtLabel}</span>
        {/if}
      </div>
      {#if saveError}
        <p class="text-error" role="alert">{saveError}</p>
      {/if}
    </Panel>

    <Panel title="Preview" noPadding>
      <div class="preview-tabs" role="tablist" aria-label="Preview format">
        <button type="button" role="tab" aria-selected={htmlTabSelected} onclick={() => (previewTab = 'html')}>
          HTML
        </button>
        <button type="button" role="tab" aria-selected={!htmlTabSelected} onclick={() => (previewTab = 'text')}>
          Plain text
        </button>
      </div>
      {#if previewError}
        <p class="text-error preview-pad" role="alert">{previewError}</p>
      {:else if preview}
        {#if htmlTabSelected}
          <iframe
            title="Campaign preview"
            class="preview-frame"
            sandbox=""
            referrerpolicy="no-referrer"
            srcdoc={preview.html}
          ></iframe>
        {:else}
          <pre class="preview-text">{preview.text}</pre>
        {/if}
      {:else}
        <p class="text-muted preview-pad" role="status">Rendering…</p>
      {/if}
    </Panel>

    <Panel title="Audience">
      <fieldset disabled={!editable}>
        <legend>Who receives this</legend>
        <div class="radio-group">
          {#each modeOptions as opt (opt.value)}
            <label class="radio-label">
              <input
                type="radio"
                name="audience-mode"
                value={opt.value}
                checked={opt.selected}
                onchange={() => onModeChange(opt.value)}
              />
              {opt.label}
            </label>
          {/each}
        </div>
      </fieldset>
      <fieldset disabled={interestsFieldsetDisabled} class="interests-fieldset">
        <legend>Interests</legend>
        <div class="checkbox-group">
          {#each interestOptions as it (it.id)}
            <label class="checkbox-label">
              <input
                type="checkbox"
                checked={it.selected}
                onchange={() => toggleInterest(it.id)}
              />
              {it.name}
            </label>
          {/each}
        </div>
      </fieldset>

      <div class="audience-count" role="status" aria-live="polite">
        {#if audienceError}
          <p class="text-error">{audienceError}</p>
        {:else}
          <p class="audience-headline">{audienceDescription}</p>
        {/if}
      </div>

      {#if hasAudienceWarnings}
        <ul class="audience-warnings">
          {#each audienceWarningsList as warning}
            <li class="text-notice">{warning}</li>
          {/each}
        </ul>
      {/if}

      {#if hasAudienceSample}
        <details>
          <summary>{audienceSampleCaption}</summary>
          <ul class="audience-sample">
            {#each audienceSampleList as email}
              <li class="mono">{email}</li>
            {/each}
          </ul>
        </details>
      {/if}
    </Panel>

    <Panel title="Pre-send checks">
      <h3 class="preflight-heading" tabindex="-1" bind:this={preflightHeadingEl}>Pre-send checks</h3>
      <div role="status" aria-live="polite">
        {#if preflightError}
          <p class="text-error">{preflightError}</p>
        {:else if preflightUnknown}
          <p class="text-muted">Checking…</p>
        {:else if preflightClear}
          <p class="text-notice">All pre-send checks pass.</p>
        {:else}
          <ol class="unmet-list">
            {#each unmetDisplay as item (item.code)}
              <li data-code={item.code}>
                <span aria-hidden="true">[ !! ]</span>
                {item.message}
                {#if item.fix}
                  <button type="button" class="link-button" onclick={() => goToFix(item.fix?.section ?? '')}>
                    {item.fix.label}
                  </button>
                {/if}
              </li>
            {/each}
          </ol>
        {/if}
      </div>
      <Button onclick={reCheck}>Re-check</Button>
    </Panel>

    <Panel title="Test send">
      <p class="text-muted">
        Sends one real message to your own inbox, with a working unsubscribe link tied to a dedicated test
        subscriber — clicking it is safe and never affects a real subscriber.
      </p>
      <Button disabled={testSending} onclick={onTestSend}>
        {testSending ? 'Sending…' : 'Send test to myself'}
      </Button>
      {#if testSendMessage}
        <p role="status">{testSendMessage}</p>
      {/if}
    </Panel>

    <div class="action-row" role="status" aria-live="polite">
      {#if sendOffered}
        <button
          type="button"
          class="btn btn-primary"
          bind:this={sendButtonEl}
          aria-disabled={!sendGateOpen}
          aria-describedby="preflight-panel-hint"
          onclick={onOpenSend}
        >
          Send
        </button>
      {/if}
      {#if cancelOffered}
        <Button variant="danger" onclick={openCancel}>Cancel campaign</Button>
      {/if}
    </div>

    <!-- #0048: live send progress over SSE. The outer div is #0047's named
         region, rendered UNCONDITIONALLY and never wrapped in an {#if} — an
         element inserted into the DOM together with its text is not
         reliably announced (the #0036 -> #0063 live-region finding), so
         only the CHILDREN below are conditional. Every value is a lib/
         campaignProgress.ts call read through a $derived above; nothing
         here recomputes a percentage, a status literal, or a comparison. -->
    <div id="campaign-send-progress" role="status" aria-live="polite">
      {#if progressVisible}
        <p class="progress-heading">{progressHeadingText}</p>
        <p class="progress-detail">{progressDetailText}</p>
        <div class="progress-bar" aria-hidden="true">
          <div class="progress-bar-fill" style="width: {progressPercentValue}%"></div>
        </div>
        {#if progressStalled}
          <p class="text-muted">No update in a while — the worker may be paused or waiting on send-rate limits.</p>
        {/if}
      {/if}
    </div>

    {#if sendDialogOpen && preflightSummary}
      <CampaignSendDialog
        summary={preflightSummary}
        unmet={sendUnmet}
        inFlight={sendInFlight}
        errorMessage={sendError}
        onConfirm={onConfirmSend}
        onClose={onCloseSend}
      />
    {/if}

    {#if cancelDialogOpen}
      <div
        class="modal-backdrop"
        role="presentation"
        onclick={closeCancel}
        onkeydown={(e) => {
          if (e.key === 'Escape') closeCancel();
        }}
      >
        <div
          class="modal"
          role="dialog"
          aria-modal="true"
          aria-label="Cancel campaign"
          tabindex="-1"
          onclick={(e) => e.stopPropagation()}
          onkeydown={(e) => e.stopPropagation()}
        >
          <h2 class="modal-title">Cancel this campaign?</h2>
          <p>{cancelMessage}</p>
          {#if cancelError}
            <p class="text-error" role="alert">{cancelError}</p>
          {/if}
          <div class="row" style="margin-top: var(--space-3);">
            <Button variant="danger" disabled={canceling} onclick={onConfirmCancel}>
              {canceling ? 'Cancelling…' : 'Cancel campaign'}
            </Button>
            <Button disabled={canceling} onclick={closeCancel}>Keep it</Button>
          </div>
        </div>
      </div>
    {/if}
  {/if}
</div>

<style>
  .mono {
    font-family: var(--font-mono);
  }
  .campaign-editor {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }
  .editor-toolbar {
    margin-bottom: var(--space-2);
  }
  .editor-heading {
    margin: 0;
  }
  .demoted-banner {
    padding: var(--space-3);
    border: var(--border-w) solid var(--border-strong);
    border-radius: var(--radius);
    background: var(--bg-subtle);
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    align-items: flex-start;
  }
  .body-textarea {
    font-family: var(--font-mono, monospace);
    resize: vertical;
  }
  .save-row {
    margin-top: var(--space-2);
  }
  .length-advice {
    margin: var(--space-1) 0 0;
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  .length-advice.warn {
    color: var(--text-notice, var(--success));
  }
  .length-advice.over {
    color: var(--danger);
  }
  .preview-tabs {
    display: flex;
    gap: var(--space-1);
    padding: var(--space-2) var(--space-3) 0;
  }
  .preview-tabs button {
    background: none;
    border: var(--border-w) solid var(--border);
    border-bottom: none;
    border-radius: var(--radius) var(--radius) 0 0;
    padding: var(--space-1) var(--space-3);
    cursor: pointer;
    font-family: var(--font);
    color: var(--text-muted);
  }
  .preview-tabs button[aria-selected='true'] {
    color: var(--text);
    background: var(--bg-panel);
    font-weight: 600;
  }
  .preview-pad {
    padding: var(--space-3);
  }
  .preview-frame {
    width: 100%;
    height: 480px;
    border: none;
    border-top: var(--border-w) solid var(--border);
  }
  .preview-text {
    margin: 0;
    padding: var(--space-3);
    white-space: pre-wrap;
    font-family: var(--font-mono, monospace);
    max-height: 480px;
    overflow: auto;
  }
  .radio-group,
  .checkbox-group {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2) var(--space-4);
    margin-top: var(--space-1);
  }
  .radio-label,
  .checkbox-label {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    cursor: pointer;
  }
  .interests-fieldset {
    margin-top: var(--space-3);
  }
  .audience-count {
    margin-top: var(--space-3);
  }
  .audience-headline {
    font-size: var(--fs-lg);
    font-weight: 700;
    margin: 0;
  }
  .audience-warnings {
    margin: var(--space-2) 0 0;
    padding-left: var(--space-4);
  }
  .audience-sample {
    margin: var(--space-2) 0 0;
    padding-left: var(--space-4);
  }
  .preflight-heading {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip: rect(0 0 0 0);
  }
  .unmet-list {
    margin: 0;
    padding-left: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }
  .link-button {
    background: none;
    border: none;
    color: var(--accent);
    cursor: pointer;
    text-decoration: underline;
    font-family: var(--font);
    padding: 0;
    margin-left: var(--space-2);
  }
  .action-row {
    display: flex;
    gap: var(--space-2);
    margin-top: var(--space-2);
  }
  #campaign-send-progress:not(:empty) {
    margin-top: var(--space-2);
  }
  .progress-heading {
    font-weight: 600;
    margin: 0;
  }
  .progress-detail {
    margin: var(--space-1) 0 0;
    color: var(--text-muted);
  }
  .progress-bar {
    margin-top: var(--space-2);
    height: 0.5rem;
    border-radius: var(--radius);
    background: var(--bg-subtle);
    border: var(--border-w) solid var(--border-strong);
    overflow: hidden;
  }
  .progress-bar-fill {
    height: 100%;
    background: var(--accent);
    transition: width 0.3s ease;
  }
  .btn {
    font-family: var(--font);
    font-size: var(--fs-base);
    line-height: 1;
    padding: var(--space-2) var(--space-3);
    border: var(--border-w) solid var(--border-strong);
    border-radius: var(--radius);
    background: var(--bg-subtle);
    color: var(--text);
    cursor: pointer;
  }
  .btn-primary {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .btn-primary[aria-disabled='true'] {
    opacity: 0.6;
  }
  .modal-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.4);
    display: flex;
    align-items: center;
    justify-content: center;
    padding: var(--space-4);
    z-index: 10;
  }
  .modal {
    background: var(--bg-panel);
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
    padding: var(--space-5);
    width: 100%;
    max-width: 420px;
  }
  .modal-title {
    font-size: var(--fs-lg);
    font-weight: 600;
    margin: 0 0 var(--space-4);
  }
</style>
