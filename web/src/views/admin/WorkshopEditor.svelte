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
  and after every successful save(). This replaced
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

  Slug: always shown read-only. See lib/workshopAdmin.ts's header comment
  for why -- #0051's committed API accepts no client-supplied slug on
  create or patch, so "editable before first publish" (this issue's own
  acceptance criterion) cannot be built without a backend change outside
  this issue's admin-UI-only scope. Flagged for the reviewer there and in
  this issue's Gotchas.

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
    announceUnsavedInterestsHint,
    unpublishConfirmMessage,
    cancelConfirmMessage,
    deleteConfirmMessage,
    isSlugEditable,
    toggleInterestId,
    validateWorkshopForm,
    isDeleteConflict,
    workshopToFormFields,
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
        return cancelConfirmMessage(workshop.title);
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
          {#if isSlugEditable()}
            Auto-generated from the title.
          {:else}
            Auto-generated from the title and set once at creation -- the server does not accept
            edits to it through this screen.
          {/if}
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
        <p class="text-muted cover-hint">
          A site-relative path (e.g. "/assets/workshops/soldering.jpg") -- there is no upload
          control here (#0138: no upload endpoint exists, by design -- see PRD §5.2) and an
          external URL is not accepted (CLAUDE.md §9: no external CDNs).
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
    {#if saveNotice}
      <p class="text-notice" role="status">{saveNotice}</p>
    {/if}

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

  <Panel title="Announce">
    <p class="text-muted">
      Create a draft campaign pre-filled from this workshop's title, summary, date, and location.
      {announceTargetingDescription(workshop.interest_ids)}{announceUnsavedInterestsHint(
        fields.interestIds,
        workshop.interest_ids,
      )} Nothing is sent — review and send the draft from the Campaigns tab.
    </p>
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
            campaign's record. Cancel the workshop instead -- it stays visible on the public site,
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
