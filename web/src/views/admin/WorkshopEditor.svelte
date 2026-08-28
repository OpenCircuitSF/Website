<!--
  The workshop create/edit form (#0052, PRD §5.2): every field, Markdown
  editing with a preview for the body, interest tagging, the
  publish/unpublish/cancel actions, and delete.

  Explicit Save, not autosave: unlike CampaignEditor.svelte's ~2s-idle
  autosave, a workshop has no send-time preview to protect and a plain
  "Save" button is the simpler, sufficient shape here.

  Body preview is server-rendered, not "live" while typing (#0136): POST
  /admin/workshops/{id}/preview always renders the STORED row through
  goldmark (internal/handlers/admin_workshop_preview.go), the same "preview
  can never show something publish wouldn't produce" contract campaigns'
  preview already enforces -- see refreshPreview below, called after load()
  and after every successful save(). #0167: that contract means the shown
  HTML can lag the textarea whenever the admin has typed since the last
  save, and the original affordance for that (the "Save the body first"
  line) only ever appeared for a brand-new, never-saved workshop --
  previewStale (lib/workshopAdmin.ts's isPreviewStale) is the general check,
  compared against fields.bodyMd vs. workshop.body_md, and drives a
  role="status" cue shown ALONGSIDE the still-rendered last-saved HTML, not
  a replacement for it -- reintroducing client-side rendering to make the
  preview "live" would undo #0136's whole point. This replaced
  web/src/lib/markdown.ts's dependency-free client renderer (that module was
  renamed linkSafety.ts by #0157, since only its isSafeLinkHref survives),
  which shipped a live XSS hole in its first version (fixed in b562800, then
  replaced outright by #0136 rather than hardened a second time).

  Interest tagging: checking/unchecking a box only edits the LOCAL
  `interestIds` buffer -- it is written to the server on the next Save, same
  as every other field, not on each click (contrast CampaignEditor.svelte's
  audience checkboxes, which save immediately because they also have to
  refresh a live audience count). Saved interest_ids drive both the public
  workshop page's pre-checked subscribe prompt and campaign targeting
  downstream (#0053/#0056) -- this screen's only job is letting an admin set
  them.

  Slug: always shown read-only, by design (#0137). #0051's API accepts no
  client-supplied slug on create or patch, so #0052's original "editable
  before first publish" criterion could not be built; #0137 decided the API
  is right and struck that criterion rather than adding a slug-editing
  route. See lib/workshopAdmin.ts's header comment for the full reasoning.

  Cover image: a path/URL text field, not an upload control -- #0051's API
  has no upload endpoint. Also flagged as a discrepancy in lib/workshopAdmin.ts
  and this issue's Gotchas.

  Delete's 409: workshops.ErrHasCampaigns (an email campaign still
  references this workshop) is a real, expected outcome, not a generic error
  toast -- the delete-confirm modal below renders it as its own state with a
  clear explanation and a shortcut into Cancel instead of leaving the admin
  to read a raw error string.

  Modal Escape handling (both modals below -- publish/unpublish/cancel
  confirm, and delete confirm): each modal's own `onkeydown` closes on
  Escape directly. #0120 (still open) is the same bug reproduced elsewhere in
  this codebase: a modal's keydown guard does
  `onkeydown={(e) => e.stopPropagation()}` unconditionally, which eats Escape
  before it can bubble to the backdrop's own handler, so Escape silently does
  nothing. Do not copy that shape.

  #0052 review (bounced 2026-08-22, finding 2): a correct Escape handler on
  the panel is not enough by itself -- nothing was moving focus INTO either
  panel on open, so it stayed on the trigger button that opened it, outside
  the backdrop's subtree, and the keydown never fired. Fixed with
  `bind:this={...ModalEl}` plus a `$effect` per modal that calls
  `tick().then(() => el?.focus())` once its open-state goes true -- the same
  shape already working in `CampaignSendDialog.svelte`. This also closes the
  `aria-modal` initial-focus gap for both modals.
-->
<script lang="ts">
  import { onMount, tick } from 'svelte';
  import {
    getAdminWorkshop,
    updateWorkshop,
    deleteWorkshop,
    announceWorkshop,
    previewWorkshop,
    listInterests,
    ApiError,
  } from '../../lib/api';
  import {
    workshopStatusLabel,
    workshopStatusBadgeClass,
    canPublish,
    canUnpublish,
    canCancel,
    publishConfirmMessage,
    announceTargetingDescription,
    announceTargetingClass,
    announceUnsavedInterestsHint,
    unpublishConfirmMessage,
    cancelConfirmMessage,
    deleteConfirmMessage,
    toggleInterestId,
    validateWorkshopForm,
    isDeleteConflict,
    workshopToFormFields,
    isPreviewStale,
    type WorkshopFormFields,
  } from '../../lib/workshopAdmin';
  import { sortedInterests } from '../../lib/admin';
  import type { AdminWorkshop, Interest } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import Panel from '../../lib/Panel.svelte';

  interface Props {
    workshopId: number;
    onBack: () => void;
    // #0056: called with the newly-created draft campaign's id once
    // Announce succeeds, so the caller (Workshops.svelte -> Admin.svelte)
    // can switch to the Campaigns tab with that draft already open --
    // "the workshop screen links to the created draft" per this issue's
    // acceptance criteria.
    onAnnounced: (campaignId: number) => void;
  }

  let { workshopId, onBack, onAnnounced }: Props = $props();

  // ── Load state ──────────────────────────────────────────────────────────
  let workshop = $state<AdminWorkshop | null>(null);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let taxonomyInterests = $state<Interest[]>([]);

  // ── Editable buffer ─────────────────────────────────────────────────────
  let fields = $state<WorkshopFormFields>({
    title: '',
    summary: '',
    bodyMd: '',
    startsAtLocal: '',
    endsAtLocal: '',
    locationName: '',
    locationAddress: '',
    locationNote: '',
    capacity: '',
    signupUrl: '',
    coverImage: '',
    interestIds: [],
  });

  // ── Save state ──────────────────────────────────────────────────────────
  let saving = $state(false);
  let saveError = $state<string | null>(null);
  let saveNotice = $state<string | null>(null);

  // ── Body preview (#0136) ─────────────────────────────────────────────────
  // Server-rendered, not a client-side re-derivation of the unsaved buffer:
  // POST .../preview (previewWorkshop) always renders the STORED row, the
  // same "never preview one thing and publish another" contract
  // CampaignEditor.svelte's autosave-then-preview flow already established
  // for campaigns (see admin_workshop_preview.go's package doc comment).
  // This editor has no autosave (explicit Save, per this file's own header
  // comment), so the preview simply reflects whatever was last saved --
  // refreshed after load() and after every successful save().
  let previewOpen = $state(false);
  let previewHtml = $state('');
  let previewLoading = $state(false);
  let previewError = $state<string | null>(null);
  let hasPreviewContent = $derived(previewHtml.trim() !== '');
  // #0167: hasPreviewContent (above) gates whether there's anything
  // rendered to show at all -- true only once *something* has ever been
  // saved. This is the orthogonal check: whether the currently-rendered
  // HTML (always the last-SAVED body, #0136) still matches what's in the
  // textarea right now. See isPreviewStale's doc comment.
  let previewStale = $derived(isPreviewStale(fields.bodyMd, workshop?.body_md));

  async function refreshPreview(): Promise<void> {
    previewLoading = true;
    previewError = null;
    try {
      const resp = await previewWorkshop(workshopId);
      previewHtml = resp.html;
    } catch (err) {
      previewHtml = '';
      previewError = err instanceof ApiError ? err.message : 'Could not render a preview.';
    } finally {
      previewLoading = false;
    }
  }

  // ── Status transitions ──────────────────────────────────────────────────
  type TransitionKind = 'publish' | 'unpublish' | 'cancel';
  let transitionOpen = $state<TransitionKind | null>(null);
  let transitioning = $state(false);
  let transitionError = $state<string | null>(null);

  // ── Delete ──────────────────────────────────────────────────────────────
  let deleteOpen = $state(false);
  let deleting = $state(false);
  let deleteError = $state<string | null>(null);
  let deleteConflict = $state(false);

  // ── Announce-to-list shortcut (#0056) ─────────────────────────────────────
  // Repeated clicks are allowed to create additional drafts (the API's own
  // contract -- issues/0056.md's Notes: overwriting a partly-edited draft
  // would lose work), so this state only tracks the MOST RECENTLY created
  // draft's id for the "open it" link below, never a "already announced"
  // guard.
  let announcing = $state(false);
  let announceError = $state<string | null>(null);
  let announcedCampaignId = $state<number | null>(null);

  // ── Modal focus (#0052 review, finding 2) ───────────────────────────────
  let transitionModalEl = $state<HTMLDivElement | null>(null);
  let deleteModalEl = $state<HTMLDivElement | null>(null);

  $effect(() => {
    if (transitionOpen) void tick().then(() => transitionModalEl?.focus());
  });

  $effect(() => {
    if (deleteOpen) void tick().then(() => deleteModalEl?.focus());
  });

  let interestOptions = $derived(
    sortedInterests(taxonomyInterests).map((it) => ({
      ...it,
      selected: fields.interestIds.includes(it.id),
    })),
  );

  let statusLabel = $derived(workshop ? workshopStatusLabel(workshop.status) : '');
  let statusBadgeClass = $derived(workshop ? workshopStatusBadgeClass(workshop.status) : '');
  let publishOffered = $derived(workshop ? canPublish(workshop.status) : false);
  let unpublishOffered = $derived(workshop ? canUnpublish(workshop.status) : false);
  let cancelOffered = $derived(workshop ? canCancel(workshop.status) : false);

  let transitionMessage = $derived.by(() => {
    if (!workshop || !transitionOpen) return '';
    switch (transitionOpen) {
      case 'publish':
        return publishConfirmMessage(workshop.title);
      case 'unpublish':
        return unpublishConfirmMessage(workshop.title);
      case 'cancel':
        // #0177: derived from status AND published_at together (the
        // matrix), not a `!!workshop.published_at` boolean alone -- that
        // collapse was the defect (a draft with a leftover published_at
        // read as "was published" and got told canceling wouldn't change
        // its visibility, when canceling actually publishes it).
        return cancelConfirmMessage(workshop.title, workshop.status, workshop.published_at);
    }
  });
  let transitionHeading = $derived(
    transitionOpen === 'publish'
      ? 'Publish this workshop?'
      : transitionOpen === 'unpublish'
        ? 'Unpublish this workshop?'
        : 'Cancel this workshop?',
  );

  let deleteMessage = $derived(workshop ? deleteConfirmMessage(workshop.title) : '');

  // #0162: computed once here rather than called twice in the template (the
  // conditional gating the paragraph, and the paragraph's own text).
  let unsavedInterestsHint = $derived(
    workshop ? announceUnsavedInterestsHint(fields.interestIds, workshop.interest_ids) : '',
  );

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      const [w, interestsResp] = await Promise.all([getAdminWorkshop(workshopId), listInterests()]);
      workshop = w;
      fields = workshopToFormFields(w);
      taxonomyInterests = interestsResp.interests;
      await refreshPreview();
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : 'Could not load this workshop.';
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load();
  });

  function toggleInterest(id: number): void {
    fields = { ...fields, interestIds: toggleInterestId(fields.interestIds, id) };
  }

  async function save(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    const validated = validateWorkshopForm(fields);
    if ('error' in validated) {
      saveError = validated.error;
      saveNotice = null;
      return;
    }
    saving = true;
    saveError = null;
    saveNotice = null;
    try {
      const { title, ...rest } = validated;
      workshop = await updateWorkshop(workshopId, { title, ...rest });
      fields = workshopToFormFields(workshop);
      saveNotice = 'Saved.';
      await refreshPreview();
    } catch (err) {
      saveError = err instanceof ApiError ? err.message : 'Could not save this workshop.';
    } finally {
      saving = false;
    }
  }

  // ── Status transitions ──────────────────────────────────────────────────

  function openTransition(kind: TransitionKind): void {
    transitionError = null;
    transitionOpen = kind;
  }

  function closeTransition(): void {
    if (transitioning) return;
    transitionOpen = null;
  }

  async function confirmTransition(): Promise<void> {
    if (!transitionOpen) return;
    const status =
      transitionOpen === 'publish' ? 'published' : transitionOpen === 'unpublish' ? 'draft' : 'canceled';
    transitioning = true;
    transitionError = null;
    try {
      workshop = await updateWorkshop(workshopId, { status });
      transitionOpen = null;
    } catch (err) {
      transitionError = err instanceof ApiError ? err.message : 'Could not update this workshop.';
    } finally {
      transitioning = false;
    }
  }

  // ── Delete ──────────────────────────────────────────────────────────────

  function openDelete(): void {
    deleteError = null;
    deleteConflict = false;
    deleteOpen = true;
  }

  function closeDelete(): void {
    if (deleting) return;
    deleteOpen = false;
  }

  async function confirmDelete(): Promise<void> {
    deleting = true;
    deleteError = null;
    deleteConflict = false;
    try {
      await deleteWorkshop(workshopId);
      deleteOpen = false;
      onBack();
    } catch (err) {
      if (err instanceof ApiError && isDeleteConflict(err.status)) {
        deleteConflict = true;
        deleteError = err.message;
      } else {
        deleteError = err instanceof ApiError ? err.message : 'Could not delete this workshop.';
      }
    } finally {
      deleting = false;
    }
  }

  // Shortcut offered from the delete-conflict state: close the delete
  // dialog and open the cancel-workshop confirm instead, since cancel is
  // the sanctioned alternative when a campaign still references this
  // workshop.
  function switchToCancel(): void {
    deleteOpen = false;
    openTransition('cancel');
  }

  // ── Announce-to-list shortcut ─────────────────────────────────────────────

  async function announce(): Promise<void> {
    announcing = true;
    announceError = null;
    try {
      const campaign = await announceWorkshop(workshopId);
      announcedCampaignId = campaign.id;
    } catch (err) {
      announceError = err instanceof ApiError ? err.message : 'Could not create a draft campaign.';
    } finally {
      announcing = false;
    }
  }

  function openAnnouncedCampaign(): void {
    if (announcedCampaignId !== null) onAnnounced(announcedCampaignId);
  }
</script>

<Button onclick={onBack}>&larr; Back to workshops</Button>

{#if loading}
  <p class="text-muted" role="status">Loading workshop…</p>
{:else if loadError}
  <p class="text-error" role="alert">{loadError}</p>
  <Button variant="primary" onclick={load}>Retry</Button>
{:else if workshop}
  <div class="row spread editor-heading-row">
    <h2 class="editor-heading">{workshop.title}</h2>
    <span class="badge {statusBadgeClass}">{statusLabel}</span>
  </div>

  <form onsubmit={save}>
    <Panel title="Content">
      <div class="field">
        <label for="workshop-title">Title</label>
        <input id="workshop-title" type="text" bind:value={fields.title} disabled={saving} />
      </div>

      <div class="field">
        <span class="field-label">Slug</span>
        <p class="slug-value mono">{workshop.slug}</p>
        <p class="text-muted slug-hint">
          Auto-generated from the title and set once at creation — the server does not accept
          edits to it through this screen.
        </p>
      </div>

      <div class="field">
        <label for="workshop-summary">Summary</label>
        <textarea id="workshop-summary" rows="2" bind:value={fields.summary} disabled={saving}
        ></textarea>
      </div>

      <div class="field">
        <div class="row spread body-label-row">
          <label for="workshop-body">Body (Markdown)</label>
          <button type="button" class="link-button" onclick={() => (previewOpen = !previewOpen)}>
            {previewOpen ? 'Hide preview' : 'Show preview'}
          </button>
        </div>
        <textarea
          id="workshop-body"
          rows="10"
          class="mono"
          bind:value={fields.bodyMd}
          disabled={saving}
        ></textarea>
        <!-- #0063/#0306: unconditionally rendered (empty when nothing to
             announce), the same convention saveNotice/unsavedInterestsHint
             already use below -- the SAME live-region node stays mounted for
             as long as the main form itself is (governingBranch ==
             {:else if workshop}), not created fresh by an inner
             {#if previewOpen}/{#if hasPreviewContent}. This used to sit
             INSIDE {:else if hasPreviewContent}, whose own branch held
             nothing else of substance -- #0306's structural check on
             KNOWN_STABLE_BRANCH_SITES measured that branch at a single
             element and correctly refused to accept "large, stable,
             multi-purpose branch" as a description of it (see that entry's
             history in liveRegionGuard.structuralGuard.test.ts). Hoisted
             here so the persistence claim is genuinely true of this site's
             own nearest governing branch, not just of some branch further
             out that the old comment described but this element didn't
             actually sit in. -->
        <p class="text-warn" role="status">
          {previewOpen && hasPreviewContent && previewStale
            ? "Showing the last saved version — your edits since then aren't included. Save to update the preview."
            : ''}
        </p>
        {#if previewOpen}
          <div class="body-preview" aria-label="Body preview">
            {#if previewLoading}
              <p class="text-muted" role="status">Rendering preview…</p>
            {:else if previewError}
              <p class="text-error" role="alert">{previewError}</p>
            {:else if hasPreviewContent}
              <!-- eslint-disable-next-line svelte/no-at-html-tags -->
              {@html previewHtml}
            {:else}
              <p class="text-muted">Nothing to preview yet. Save the body first — the preview shows the last saved version.</p>
            {/if}
          </div>
        {/if}
      </div>
    </Panel>

    <Panel title="Date and location">
      <div class="field">
        <label for="workshop-starts">Starts</label>
        <input
          id="workshop-starts"
          type="datetime-local"
          bind:value={fields.startsAtLocal}
          disabled={saving}
        />
      </div>
      <div class="field">
        <label for="workshop-ends">Ends</label>
        <input
          id="workshop-ends"
          type="datetime-local"
          bind:value={fields.endsAtLocal}
          disabled={saving}
        />
      </div>
      <div class="field">
        <label for="workshop-location-name">Location name</label>
        <input
          id="workshop-location-name"
          type="text"
          bind:value={fields.locationName}
          disabled={saving}
        />
      </div>
      <div class="field">
        <label for="workshop-location-address">Location address</label>
        <input
          id="workshop-location-address"
          type="text"
          bind:value={fields.locationAddress}
          disabled={saving}
        />
      </div>
      <div class="field">
        <label for="workshop-location-note">Location note</label>
        <input
          id="workshop-location-note"
          type="text"
          bind:value={fields.locationNote}
          disabled={saving}
        />
      </div>
    </Panel>

    <Panel title="Signup and cover">
      <div class="field">
        <label for="workshop-capacity">Capacity</label>
        <input
          id="workshop-capacity"
          type="text"
          inputmode="numeric"
          bind:value={fields.capacity}
          disabled={saving}
        />
      </div>
      <div class="field">
        <label for="workshop-signup-url">Signup URL</label>
        <input
          id="workshop-signup-url"
          type="text"
          bind:value={fields.signupUrl}
          disabled={saving}
        />
      </div>
      <div class="field">
        <label for="workshop-cover-image">Cover image (site-relative path)</label>
        <input
          id="workshop-cover-image"
          type="text"
          bind:value={fields.coverImage}
          disabled={saving}
        />
        <!-- No upload endpoint exists, by design: #0138 decided cover images stay path entry
             for v1 and struck #0052's "upload or path entry" criterion, leaving the upload
             question to #0153 (PRD §5.2 asks for no upload). An external URL is rejected
             because the site hosts its own images (CLAUDE.md §9: no external CDNs). Both
             citations stay here, not in the admin-facing copy below (#0172). -->
        <p class="text-muted cover-hint">
          A site-relative path (e.g. "/assets/workshops/soldering.jpg") to an image already
          hosted on this site. There is no upload control here, and external image URLs are
          not accepted.
        </p>
      </div>
    </Panel>

    <Panel title="Interests">
      <p class="text-muted interests-hint">
        Drives the public page's pre-checked subscribe prompt and campaign targeting.
      </p>
      <div class="checkbox-group">
        {#each interestOptions as it (it.id)}
          <label class="checkbox-label">
            <input type="checkbox" checked={it.selected} onchange={() => toggleInterest(it.id)} />
            {it.name}
          </label>
        {/each}
      </div>
    </Panel>

    {#if saveError}
      <p class="text-error" role="alert">{saveError}</p>
    {/if}
    <!-- #0063: unconditionally rendered (empty when nothing to announce).
         Console-wide decision; see issues/0063.md. -->
    <p class="text-notice" role="status">{saveNotice ?? ''}</p>

    <div class="row save-row">
      <Button type="submit" variant="primary" disabled={saving}>
        {saving ? 'Saving…' : 'Save'}
      </Button>
    </div>
  </form>

  <Panel title="Status">
    <div class="row status-actions">
      {#if publishOffered}
        <Button variant="primary" onclick={() => openTransition('publish')}>Publish</Button>
      {/if}
      {#if unpublishOffered}
        <Button onclick={() => openTransition('unpublish')}>Unpublish</Button>
      {/if}
      {#if cancelOffered}
        <Button variant="danger" onclick={() => openTransition('cancel')}>Cancel workshop</Button>
      {/if}
      <Button variant="danger" onclick={openDelete}>Delete</Button>
    </div>
  </Panel>

  <!--
    #0162: the targeting sentence and the unsaved-interests hint used to be
    one muted paragraph, so the sentence most likely to change what an admin
    does next read as de-emphasised filler. Both are now split into their
    own elements styled with the console's existing `.text-warn` treatment
    when what they're saying is a warning (see announceTargetingClass's and
    announceUnsavedInterestsHint's doc comments in lib/workshopAdmin.ts for
    why only the hint also gets role="status").

    #0063: the hint <p> below is unconditionally rendered (unsavedInterestsHint
    is '' rather than the element being absent) so the SAME live-region node
    stays mounted for the whole time this panel is visible -- toggling an
    interest checkbox only mutates its text, which is what a screen reader
    reliably announces from a role="status" region. An {#if} that created this
    element fresh, with its text already in it, is not reliably announced.
    Console-wide decision. #0063's phase-3 review found six more sites with
    the identical defect that the first pass missed by scoping its sweep to
    the files it was already editing rather than the whole tree
    (`web/src/views/Admin.svelte` x4, this file's `saveNotice`,
    `CampaignEditor.svelte`'s `testSendMessage`) -- all now converted the
    same way. `Login.svelte`'s two panel-swap notices are the OTHER
    documented pattern (whole-panel replacement -> focus movement, per
    `Unsubscribe.svelte`), not this one. A further 17 `{#if loading}<p
    role="status">Loading…</p>{/if}` placeholders across the console were
    reported, not converted, in #0063.md's fix pass -- they announce an
    initial-load state rather than the result of a user action, a smaller
    instance of the same defect class. (#0244: this comment previously said
    "~10" -- stale even against #0063's own contemporaneous count of 14, and
    more stale now: `grep -rn 'role="status"' web/src | grep -v
    '\.test\.ts' | grep -i loading` finds 17 today, one more file
    (`Dashboard.svelte`'s "Loading overview…") having grown this exact shape
    since #0063 shipped. Re-derived from the tree rather than copied from
    #0244's own filing, which still said "roughly fifty" for the sibling
    `role="alert"` count -- see `liveRegionGuard.structuralGuard.test.ts` for
    the current exact figures on both.)
  -->
  <Panel title="Announce">
    <p class="text-muted">
      Create a draft campaign pre-filled from this workshop's title, summary, date, and location.
      Nothing is sent — review and send the draft from the Campaigns tab.
    </p>
    <p class={announceTargetingClass(workshop.interest_ids)}>
      {announceTargetingDescription(workshop.interest_ids)}
    </p>
    <p class="text-warn" role="status">{unsavedInterestsHint}</p>
    {#if announceError}
      <p class="text-error" role="alert">{announceError}</p>
    {/if}
    <div class="row">
      <Button onclick={announce} disabled={announcing}>
        {announcing ? 'Creating draft…' : 'Announce to list'}
      </Button>
      {#if announcedCampaignId !== null}
        <Button variant="primary" onclick={openAnnouncedCampaign}>Open draft campaign</Button>
      {/if}
    </div>
  </Panel>

  {#if transitionOpen}
    <div
      class="modal-backdrop"
      role="presentation"
      onclick={closeTransition}
      onkeydown={(e) => {
        if (e.key === 'Escape') closeTransition();
      }}
    >
      <div
        class="modal"
        role="dialog"
        aria-modal="true"
        aria-label={transitionHeading}
        tabindex="-1"
        bind:this={transitionModalEl}
        onclick={(e) => e.stopPropagation()}
        onkeydown={(e) => {
          if (e.key === 'Escape') closeTransition();
        }}
      >
        <h2 class="modal-title">{transitionHeading}</h2>
        <p>{transitionMessage}</p>
        {#if transitionError}
          <p class="text-error" role="alert">{transitionError}</p>
        {/if}
        <div class="row" style="margin-top: var(--space-3);">
          <Button
            variant={transitionOpen === 'cancel' ? 'danger' : 'primary'}
            disabled={transitioning}
            onclick={confirmTransition}
          >
            {transitioning ? 'Working…' : transitionHeading.replace('?', '')}
          </Button>
          <Button disabled={transitioning} onclick={closeTransition}>Keep as is</Button>
        </div>
      </div>
    </div>
  {/if}

  {#if deleteOpen}
    <div
      class="modal-backdrop"
      role="presentation"
      onclick={closeDelete}
      onkeydown={(e) => {
        if (e.key === 'Escape') closeDelete();
      }}
    >
      <div
        class="modal"
        role="dialog"
        aria-modal="true"
        aria-label="Delete workshop"
        tabindex="-1"
        bind:this={deleteModalEl}
        onclick={(e) => e.stopPropagation()}
        onkeydown={(e) => {
          if (e.key === 'Escape') closeDelete();
        }}
      >
        {#if deleteConflict}
          <h2 class="modal-title">Can't delete this workshop</h2>
          <p class="text-error" role="alert">{deleteError}</p>
          <p>
            An email campaign already announces this workshop, and deleting it would break that
            campaign's record. Cancel the workshop instead — it stays visible on the public site,
            marked canceled.
          </p>
          <div class="row" style="margin-top: var(--space-3);">
            <Button variant="primary" onclick={switchToCancel}>Cancel workshop instead</Button>
            <Button onclick={closeDelete}>Close</Button>
          </div>
        {:else}
          <h2 class="modal-title">Delete this workshop?</h2>
          <p>{deleteMessage}</p>
          {#if deleteError}
            <p class="text-error" role="alert">{deleteError}</p>
          {/if}
          <div class="row" style="margin-top: var(--space-3);">
            <Button variant="danger" disabled={deleting} onclick={confirmDelete}>
              {deleting ? 'Deleting…' : 'Delete'}
            </Button>
            <Button disabled={deleting} onclick={closeDelete}>Keep it</Button>
          </div>
        {/if}
      </div>
    </div>
  {/if}
{/if}

<style>
  .editor-heading-row {
    margin: var(--space-4) 0 var(--space-3);
  }
  .editor-heading {
    font-size: var(--fs-xl);
    margin: 0;
  }
  .mono {
    font-family: var(--font-mono);
  }
  .field-label {
    display: block;
    margin-bottom: var(--space-1);
    font-weight: 600;
  }
  .slug-value {
    margin: 0;
  }
  .slug-hint {
    margin: var(--space-1) 0 0;
  }
  .body-label-row {
    margin-bottom: var(--space-1);
  }
  .link-button {
    background: none;
    border: none;
    color: var(--accent);
    cursor: pointer;
    text-decoration: underline;
    font-family: var(--font);
    font-size: var(--fs-sm);
    padding: 0;
  }
  .body-preview {
    margin-top: var(--space-2);
    padding: var(--space-3);
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
    background: var(--bg-subtle);
  }
  .body-preview :global(h1),
  .body-preview :global(h2),
  .body-preview :global(h3),
  .body-preview :global(h4),
  .body-preview :global(h5),
  .body-preview :global(h6) {
    margin-top: 0;
  }
  .body-preview :global(p:last-child),
  .body-preview :global(ul:last-child),
  .body-preview :global(ol:last-child) {
    margin-bottom: 0;
  }
  .cover-hint,
  .interests-hint {
    margin: var(--space-1) 0 var(--space-2);
  }
  .checkbox-group {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2) var(--space-4);
  }
  .checkbox-label {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    cursor: pointer;
  }
  .save-row {
    margin: var(--space-3) 0 var(--space-4);
  }
  .status-actions {
    flex-wrap: wrap;
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
    max-width: 480px;
  }
  .modal-title {
    font-size: var(--fs-lg);
    font-weight: 600;
    margin: 0 0 var(--space-4);
  }
</style>
