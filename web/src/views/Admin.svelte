<!--
  Admin view. Admin-only — rendered only when currentUser.is_admin; a
  non-admin who somehow reaches it sees an access-denied notice rather than any
  admin data. It ties together four backend areas behind a single view with a
  tab bar:

  - Settings   — GET/PATCH /admin/settings: a registrations_enabled toggle and
    a physical_address text field. physical_address is edited here as a
    setting (not an env var) because #0045's send worker reads it fresh from
    the settings table at send time and refuses to start a campaign while it
    is empty (CAN-SPAM requires a physical postal address in every commercial
    message).
  - Users      — GET /admin/users plus POST .../{id}/deactivate|reactivate:
    list with status, deactivate a non-admin (reason dropdown + note, note
    required for "other"), reactivate with an optional note.
  - Audit log  — GET /admin/audit?page=&per_page=&user_id=: a paginated,
    newest-first table with a per-user filter.
  - Interests  — GET/POST/PATCH/DELETE /admin/interests[/{id}] (#0024): the
    workshop interest taxonomy, editable without a deploy (PRD §6.1). Each
    row shows its subscriber_count — the number that tells the operator
    whether a segment is worth a campaign. Deactivating (PATCH active:false)
    hides an interest from the public signup form (#0026 validates new
    signups' interest slugs against active ones only) while preserving every
    subscriber_interests row that already references it; the edit modal
    says so inline before an admin flips the toggle. Delete is a hard
    removal the server refuses with 409 whenever any subscriber is
    associated — the Delete button is disabled with an explanation in that
    case rather than letting the admin discover the refusal after a failed
    request. Slugs are immutable once created (no field for it anywhere in
    this section) — see #0024's Gotchas for why.
-->
<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { currentUser } from '../lib/stores';
  import { navigate } from '../lib/router';
  import { isModalEscape } from '../lib/modalKeydown';
  import {
    getSettings,
    updateSetting,
    listUsers,
    deactivateUser,
    reactivateUser,
    listAudit,
    listInterests,
    createInterest,
    updateInterest,
    deleteInterest,
    listSubscribers,
    getSubscriber,
    suppressSubscriber,
    clearSubscriberComplaint,
    createSubscriber,
    listSuppressions,
    removeSuppression,
    logout,
    ApiError,
  } from '../lib/api';
  import {
    DEACTIVATION_REASONS,
    deactivationReasonLabel,
    validateDeactivation,
    canDeactivate,
    actorLabel,
    targetLabel,
    formatMetadata,
    formatDateTime,
    pageInfo,
    parseUserIdFilter,
    parseTargetFilter,
    campaignAuditFilter,
    registrationsEnabled,
    isValidInterestSlug,
    validateNewInterest,
    sortedInterests,
    reorderSwap,
    hasSubscribers,
    SUBSCRIBER_STATUSES,
    subscriberStatusLabel,
    subscriberStatusBadgeClass,
    canClearComplaint,
    validateManualAddEmail,
    validateSuppressNote,
    signupEvidenceSummary,
    softBounceSummary,
    SUPPRESSION_REASONS,
    suppressionReasonLabel,
    validateSuppressionNote,
    suppressionRemovalBlocked,
  } from '../lib/admin';
  import type {
    Setting,
    AdminUser,
    AuditEntry,
    Interest,
    Subscriber,
    SubscriberStatusCounts,
    Suppression,
  } from '../lib/types';
  import Button from '../lib/Button.svelte';
  import Panel from '../lib/Panel.svelte';
  import { APP_NAME } from '../lib/branding';
  import Campaigns from './admin/Campaigns.svelte';
  import Workshops from './admin/Workshops.svelte';

  type Section =
    | 'settings'
    | 'users'
    | 'audit'
    | 'interests'
    | 'subscribers'
    | 'suppressions'
    | 'campaigns'
    | 'workshops';
  let section = $state<Section>('settings');

  // ── Settings ──────────────────────────────────────────────────────────────
  let settings = $state<Setting[]>([]);
  let settingsLoading = $state(true);
  let settingsError = $state<string | null>(null);
  let settingsNotice = $state<string | null>(null);
  let togglingRegistrations = $state(false);
  const regEnabled = $derived(
    registrationsEnabled(settings.find((s) => s.key === 'registrations_enabled')?.value),
  );

  // ── Settings: physical mailing address ──────────────────────────────────
  // Editable free text (CAN-SPAM requires a physical postal address in every
  // commercial email; #0045's send worker refuses to start a campaign while
  // this is empty). Tracks the last-saved value separately from the input's
  // live value so "Save" is disabled once the two match and a failed save
  // doesn't silently discard what the admin typed.
  const savedAddress = $derived(settings.find((s) => s.key === 'physical_address')?.value ?? '');
  let addressInput = $state('');
  let addressInitialized = $state(false);
  let savingAddress = $state(false);
  let addressError = $state<string | null>(null);
  let addressNotice = $state<string | null>(null);

  $effect(() => {
    // Seed the editable input from the loaded value exactly once (on first
    // successful load), so a later re-render after saving doesn't clobber
    // in-progress edits.
    if (!addressInitialized && !settingsLoading && !settingsError) {
      addressInput = savedAddress;
      addressInitialized = true;
    }
  });

  const addressDirty = $derived(addressInput !== savedAddress);

  async function saveAddress() {
    savingAddress = true;
    addressError = null;
    addressNotice = null;
    try {
      const res = (await updateSetting('physical_address', addressInput)) as {
        settings: Setting[];
      };
      if (res && Array.isArray(res.settings)) settings = res.settings;
      else await loadSettings();
      addressNotice = 'Physical address saved.';
    } catch (err) {
      if (handleAuthError(err)) return;
      addressError = 'Could not save the physical address. Please try again.';
    } finally {
      savingAddress = false;
    }
  }

  async function loadSettings() {
    settingsLoading = true;
    settingsError = null;
    try {
      settings = (await getSettings()).settings;
    } catch (err) {
      if (handleAuthError(err)) return;
      settingsError = 'Could not load settings. Please try again.';
    } finally {
      settingsLoading = false;
    }
  }

  async function toggleRegistrations() {
    togglingRegistrations = true;
    settingsNotice = null;
    settingsError = null;
    const next = regEnabled ? 'false' : 'true';
    try {
      const res = (await updateSetting('registrations_enabled', next)) as { settings: Setting[] };
      if (res && Array.isArray(res.settings)) settings = res.settings;
      else await loadSettings();
      settingsNotice = `Registrations ${next === 'true' ? 'enabled' : 'disabled'}.`;
    } catch (err) {
      if (handleAuthError(err)) return;
      settingsError = 'Could not update the setting. Please try again.';
    } finally {
      togglingRegistrations = false;
    }
  }

  // ── Users ─────────────────────────────────────────────────────────────────
  let users = $state<AdminUser[]>([]);
  let usersLoading = $state(true);
  let usersError = $state<string | null>(null);

  let deactivatingUser = $state<AdminUser | null>(null);
  let deactReason = $state<string>(DEACTIVATION_REASONS[0].value);
  let deactNote = $state('');
  let deactError = $state<string | null>(null);
  let deactSubmitting = $state(false);
  const deactNoteRequired = $derived(deactReason === 'other');
  let deactivateModalEl = $state<HTMLDivElement | null>(null);

  $effect(() => {
    // #0120: move focus into the modal panel itself once it opens, so the
    // panel's own Escape handler (below) is reachable and so the dialog
    // meets the aria-modal="true" contract of taking focus on open --
    // mirrors the shape #0052 established in WorkshopEditor.svelte /
    // Workshops.svelte. This modal had no focus management at all before
    // this fix: without it, focus stays on the "Deactivate" trigger button
    // (still present in the users table behind the modal), so neither this
    // panel's keydown nor the backdrop's ever fires on Escape regardless of
    // what either handler does.
    if (deactivatingUser) void tick().then(() => deactivateModalEl?.focus());
  });

  let userRowError = $state<Record<number, string>>({});
  let reactivatingId = $state<number | null>(null);

  async function loadUsers() {
    usersLoading = true;
    usersError = null;
    try {
      users = (await listUsers()).users;
    } catch (err) {
      if (handleAuthError(err)) return;
      usersError = 'Could not load users. Please try again.';
    } finally {
      usersLoading = false;
    }
  }

  function openDeactivate(user: AdminUser) {
    deactivatingUser = user;
    deactReason = DEACTIVATION_REASONS[0].value;
    deactNote = '';
    deactError = null;
  }

  function closeDeactivate() {
    deactivatingUser = null;
  }

  async function submitDeactivate(e: SubmitEvent) {
    e.preventDefault();
    if (!deactivatingUser) return;
    const result = validateDeactivation(deactReason, deactNote);
    if ('error' in result) {
      deactError = result.error;
      return;
    }
    deactSubmitting = true;
    deactError = null;
    const target = deactivatingUser;
    try {
      const updated = await deactivateUser(target.id, deactReason, result.note);
      users = users.map((u) => (u.id === updated.id ? updated : u));
      deactivatingUser = null;
    } catch (err) {
      if (handleAuthError(err)) return;
      deactError =
        err instanceof ApiError && err.message
          ? err.message
          : 'Could not deactivate the user. Please try again.';
    } finally {
      deactSubmitting = false;
    }
  }

  async function handleReactivate(user: AdminUser) {
    const note = prompt('Optional note for reactivation (leave blank to skip):') ?? '';
    reactivatingId = user.id;
    clearUserRowError(user.id);
    try {
      const updated = await reactivateUser(user.id, note.trim());
      users = users.map((u) => (u.id === updated.id ? updated : u));
    } catch (err) {
      if (handleAuthError(err)) return;
      userRowError = {
        ...userRowError,
        [user.id]: 'Could not reactivate the user. Please try again.',
      };
    } finally {
      reactivatingId = null;
    }
  }

  function clearUserRowError(id: number) {
    if (id in userRowError) {
      const { [id]: _removed, ...rest } = userRowError;
      userRowError = rest;
    }
  }

  // ── Audit log ───────────────────────────────────────────────────────────────
  const AUDIT_PER_PAGE = 50;
  let auditEntries = $state<AuditEntry[]>([]);
  let auditTotal = $state(0);
  let auditPage = $state(1);
  let auditLoading = $state(true);
  let auditError = $state<string | null>(null);
  let auditUserIdRaw = $state('');
  let auditUserIdFilter = $state<number | null>(null);
  // target_type/target_id filter (#0114): reaches rows a user_id filter
  // can't, e.g. #0045's send worker writes email_campaign.send_refused with
  // a NULL actor_id — the worker is not a user.
  let auditTargetTypeRaw = $state('');
  let auditTargetIdRaw = $state('');
  let auditTargetTypeFilter = $state<string | null>(null);
  let auditTargetIdFilter = $state<number | null>(null);
  let auditFilterError = $state<string | null>(null);
  const auditPaging = $derived(pageInfo(auditTotal, auditPage, AUDIT_PER_PAGE));
  const auditHasFilter = $derived(
    auditUserIdFilter !== null || auditTargetTypeFilter !== null || auditTargetIdFilter !== null,
  );

  async function loadAudit() {
    auditLoading = true;
    auditError = null;
    try {
      const res = await listAudit(
        auditPage,
        AUDIT_PER_PAGE,
        auditUserIdFilter ?? undefined,
        auditTargetTypeFilter ?? undefined,
        auditTargetIdFilter ?? undefined,
      );
      auditEntries = res.audit_log;
      auditTotal = res.total;
      auditPage = res.page;
    } catch (err) {
      if (handleAuthError(err)) return;
      auditError = 'Could not load the audit log. Please try again.';
    } finally {
      auditLoading = false;
    }
  }

  function applyAuditFilter(e: SubmitEvent) {
    e.preventDefault();
    const userParsed = parseUserIdFilter(auditUserIdRaw);
    if ('error' in userParsed) {
      auditFilterError = userParsed.error;
      return;
    }
    const targetParsed = parseTargetFilter(auditTargetTypeRaw, auditTargetIdRaw);
    if ('error' in targetParsed) {
      auditFilterError = targetParsed.error;
      return;
    }
    auditFilterError = null;
    auditUserIdFilter = userParsed.userId;
    auditTargetTypeFilter = targetParsed.targetType;
    auditTargetIdFilter = targetParsed.targetId;
    auditPage = 1;
    void loadAudit();
  }

  function clearAuditFilter() {
    auditUserIdRaw = '';
    auditUserIdFilter = null;
    auditTargetTypeRaw = '';
    auditTargetIdRaw = '';
    auditTargetTypeFilter = null;
    auditTargetIdFilter = null;
    auditFilterError = null;
    auditPage = 1;
    void loadAudit();
  }

  function auditGoTo(page: number) {
    auditPage = page;
    void loadAudit();
  }

  /**
   * #0114: navigate to the audit tab pre-filtered to one campaign's history
   * — the deep link CampaignEditor's demoted banner and CampaignStats offer,
   * replacing the old unfiltered "switch to the audit tab" workaround.
   */
  function goToCampaignAudit(campaignId: number) {
    const filter = campaignAuditFilter(campaignId);
    auditUserIdRaw = '';
    auditUserIdFilter = null;
    auditTargetTypeRaw = filter.targetType;
    auditTargetIdRaw = String(filter.targetId);
    auditTargetTypeFilter = filter.targetType;
    auditTargetIdFilter = filter.targetId;
    auditFilterError = null;
    auditPage = 1;
    section = 'audit';
    void loadAudit();
  }

  // #0056: the workshops screen's announce shortcut hands back the id of
  // the draft campaign it just created; jump to the Campaigns tab with
  // that draft already open, mirroring goToCampaignAudit's cross-tab
  // deep-link shape. pendingOpenCampaignId is passed to <Campaigns> below
  // and cleared once it consumes it (see that component's onOpenedCampaign)
  // so returning to this tab later doesn't reopen a stale draft.
  let pendingOpenCampaignId = $state<number | null>(null);

  function goToAnnouncedCampaign(campaignId: number) {
    pendingOpenCampaignId = campaignId;
    section = 'campaigns';
  }

  // ── Interests ─────────────────────────────────────────────────────────────
  let interests = $state<Interest[]>([]);
  let interestsLoading = $state(true);
  let interestsError = $state<string | null>(null);
  const interestsSorted = $derived(sortedInterests(interests));

  async function loadInterests() {
    interestsLoading = true;
    interestsError = null;
    try {
      interests = (await listInterests()).interests;
    } catch (err) {
      if (handleAuthError(err)) return;
      interestsError = 'Could not load interests. Please try again.';
    } finally {
      interestsLoading = false;
    }
  }

  // Create form. There is deliberately no sort_order field here — a new
  // interest is always appended after the current highest sort_order, and
  // the operator uses the ↑/↓ controls to place it. Slug is immutable once
  // created (#0024's Gotchas), so there is no rename path either.
  let newSlug = $state('');
  let newName = $state('');
  let newDescription = $state('');
  let createError = $state<string | null>(null);
  let creating = $state(false);
  const newSlugInvalid = $derived(newSlug.trim() !== '' && !isValidInterestSlug(newSlug.trim()));

  async function submitCreateInterest(e: SubmitEvent) {
    e.preventDefault();
    const result = validateNewInterest(newSlug, newName);
    if ('error' in result) {
      createError = result.error;
      return;
    }
    creating = true;
    createError = null;
    try {
      const sortOrder =
        interests.length === 0 ? 0 : Math.max(...interests.map((i) => i.sort_order)) + 10;
      const created = await createInterest(result.slug, result.name, newDescription, sortOrder);
      interests = [...interests, created];
      newSlug = '';
      newName = '';
      newDescription = '';
    } catch (err) {
      if (handleAuthError(err)) return;
      createError =
        err instanceof ApiError && err.message
          ? err.message
          : 'Could not create the interest. Please try again.';
    } finally {
      creating = false;
    }
  }

  // Edit modal: name, description, and the active toggle. Deactivating warns
  // inline (#0026 validates a new signup's interest slugs against ACTIVE
  // interests only, so this is the moment that consequence is visible)
  // before the admin confirms; reactivating has no such warning.
  let editingInterest = $state<Interest | null>(null);
  let editName = $state('');
  let editDescription = $state('');
  let editActive = $state(true);
  let editError = $state<string | null>(null);
  let editSubmitting = $state(false);
  const editDeactivating = $derived(editingInterest !== null && editingInterest.active && !editActive);
  let editInterestModalEl = $state<HTMLDivElement | null>(null);

  $effect(() => {
    // #0120: see the deactivate modal's identical $effect above for why.
    if (editingInterest) void tick().then(() => editInterestModalEl?.focus());
  });

  function openEditInterest(it: Interest) {
    editingInterest = it;
    editName = it.name;
    editDescription = it.description ?? '';
    editActive = it.active;
    editError = null;
  }

  function closeEditInterest() {
    editingInterest = null;
  }

  async function submitEditInterest(e: SubmitEvent) {
    e.preventDefault();
    if (!editingInterest) return;
    const trimmedName = editName.trim();
    if (trimmedName === '') {
      editError = 'Name is required.';
      return;
    }
    editSubmitting = true;
    editError = null;
    const targetId = editingInterest.id;
    try {
      const updated = await updateInterest(targetId, {
        name: trimmedName,
        description: editDescription.trim(),
        active: editActive,
      });
      interests = interests.map((i) => (i.id === updated.id ? updated : i));
      editingInterest = null;
    } catch (err) {
      if (handleAuthError(err)) return;
      editError =
        err instanceof ApiError && err.message
          ? err.message
          : 'Could not save the interest. Please try again.';
    } finally {
      editSubmitting = false;
    }
  }

  // Reordering: swap sort_order with the adjacent interest in display order
  // via two PATCH calls (reorderSwap computes which two ids/values).
  let reorderingId = $state<number | null>(null);

  async function moveInterest(it: Interest, direction: 'up' | 'down') {
    const swap = reorderSwap(interestsSorted, it.id, direction);
    if (!swap) return;
    reorderingId = it.id;
    interestsError = null;
    try {
      const [a, b] = await Promise.all([
        updateInterest(swap.moved.id, { sort_order: swap.moved.sortOrder }),
        updateInterest(swap.other.id, { sort_order: swap.other.sortOrder }),
      ]);
      interests = interests.map((i) => {
        if (i.id === a.id) return a;
        if (i.id === b.id) return b;
        return i;
      });
    } catch (err) {
      if (handleAuthError(err)) return;
      interestsError = 'Could not reorder interests. Please try again.';
    } finally {
      reorderingId = null;
    }
  }

  // Delete: a hard removal the server refuses (409) whenever any subscriber
  // is associated. hasSubscribers() disables the control and shows why
  // BEFORE the admin clicks, rather than surfacing the refusal only after a
  // failed request; the row error below is the belt-and-suspenders path for
  // the (racy) case a subscriber is added between page load and the click.
  let deletingId = $state<number | null>(null);
  let deleteRowError = $state<Record<number, string>>({});

  function clearDeleteRowError(id: number) {
    if (id in deleteRowError) {
      const { [id]: _removed, ...rest } = deleteRowError;
      deleteRowError = rest;
    }
  }

  async function handleDeleteInterest(it: Interest) {
    if (hasSubscribers(it)) return;
    if (!confirm(`Delete "${it.name}"? This cannot be undone.`)) return;
    deletingId = it.id;
    clearDeleteRowError(it.id);
    try {
      await deleteInterest(it.id);
      interests = interests.filter((i) => i.id !== it.id);
    } catch (err) {
      if (handleAuthError(err)) return;
      deleteRowError = {
        ...deleteRowError,
        [it.id]:
          err instanceof ApiError && err.message
            ? err.message
            : 'Could not delete the interest. Please try again.',
      };
    } finally {
      deletingId = null;
    }
  }

  // ── Subscribers (#0032) ──────────────────────────────────────────────────────
  const SUBS_PER_PAGE = 25;
  let subs = $state<Subscriber[]>([]);
  let subsTotal = $state(0);
  let subsPage = $state(1);
  let subsCounts = $state<SubscriberStatusCounts>({
    pending: 0,
    active: 0,
    unsubscribed: 0,
    bounced: 0,
    complained: 0,
  });
  let subsLoading = $state(true);
  let subsError = $state<string | null>(null);
  let subsStatusFilter = $state('');
  let subsInterestFilter = $state('');
  let subsQueryRaw = $state('');
  let subsQueryApplied = $state('');
  const subsPaging = $derived(pageInfo(subsTotal, subsPage, SUBS_PER_PAGE));

  async function loadSubscribers() {
    subsLoading = true;
    subsError = null;
    try {
      const res = await listSubscribers({
        status: subsStatusFilter || undefined,
        interestId: subsInterestFilter ? Number(subsInterestFilter) : undefined,
        q: subsQueryApplied || undefined,
        page: subsPage,
        perPage: SUBS_PER_PAGE,
      });
      subs = res.subscribers;
      subsTotal = res.total;
      subsPage = res.page;
      subsCounts = res.counts;
    } catch (err) {
      if (handleAuthError(err)) return;
      subsError = 'Could not load subscribers. Please try again.';
    } finally {
      subsLoading = false;
    }
  }

  function applySubsFilters(e?: SubmitEvent) {
    e?.preventDefault();
    subsQueryApplied = subsQueryRaw.trim();
    subsPage = 1;
    void loadSubscribers();
  }

  function clearSubsFilters() {
    subsStatusFilter = '';
    subsInterestFilter = '';
    subsQueryRaw = '';
    subsQueryApplied = '';
    subsPage = 1;
    void loadSubscribers();
  }

  function subsGoTo(page: number) {
    subsPage = page;
    void loadSubscribers();
  }

  // Detail drawer.
  let viewingSubscriber = $state<Subscriber | null>(null);
  let viewingLoading = $state(false);
  let viewingError = $state<string | null>(null);
  let subscriberDetailModalEl = $state<HTMLDivElement | null>(null);

  $effect(() => {
    // #0120: see the deactivate modal's identical $effect above for why.
    // The modal itself is open whenever any of these three is truthy (see
    // its {#if} below, which shows a loading or error state before the
    // subscriber arrives), so this effect re-runs (and re-focuses) on each
    // of those transitions too, not just the initial open.
    if (viewingSubscriber || viewingLoading || viewingError) {
      void tick().then(() => subscriberDetailModalEl?.focus());
    }
  });

  async function openSubscriberDetail(id: number) {
    viewingSubscriber = null;
    viewingLoading = true;
    viewingError = null;
    try {
      viewingSubscriber = await getSubscriber(id);
    } catch (err) {
      if (handleAuthError(err)) return;
      viewingError = 'Could not load this subscriber. Please try again.';
    } finally {
      viewingLoading = false;
    }
  }

  function closeSubscriberDetail() {
    viewingSubscriber = null;
    viewingError = null;
  }

  function replaceSubInList(updated: Subscriber) {
    subs = subs.map((s) => (s.id === updated.id ? { ...s, ...updated } : s));
    if (viewingSubscriber?.id === updated.id) {
      viewingSubscriber = { ...viewingSubscriber, ...updated };
    }
  }

  // Suppress modal — note is required (server 400s on a blank one).
  let suppressingSubscriber = $state<Subscriber | null>(null);
  let suppressNote = $state('');
  let suppressError = $state<string | null>(null);
  let suppressNotice = $state<string | null>(null);
  let suppressSubmitting = $state(false);
  let suppressModalEl = $state<HTMLDivElement | null>(null);

  $effect(() => {
    // #0120: see the deactivate modal's identical $effect above for why.
    if (suppressingSubscriber) void tick().then(() => suppressModalEl?.focus());
  });

  function openSuppress(sub: Subscriber) {
    suppressingSubscriber = sub;
    suppressNote = '';
    suppressError = null;
    suppressNotice = null;
  }

  function closeSuppress() {
    suppressingSubscriber = null;
  }

  async function submitSuppress(e: SubmitEvent) {
    e.preventDefault();
    if (!suppressingSubscriber) return;
    const result = validateSuppressNote(suppressNote);
    if ('error' in result) {
      suppressError = result.error;
      return;
    }
    suppressSubmitting = true;
    suppressError = null;
    const target = suppressingSubscriber;
    try {
      const res = await suppressSubscriber(target.id, result.note);
      replaceSubInList(res.subscriber);
      if (res.no_op) {
        // Leave the modal open with the server's own explanation — the
        // carried-in review finding requires surfacing this rather than
        // implying the action took effect (Unsubscribe silently no-ops on
        // an already-complained row, CLAUDE.md §9).
        suppressError = res.message;
      } else {
        suppressingSubscriber = null;
      }
    } catch (err) {
      if (handleAuthError(err)) return;
      suppressError =
        err instanceof ApiError && err.message
          ? err.message
          : 'Could not suppress this subscriber. Please try again.';
    } finally {
      suppressSubmitting = false;
    }
  }

  // Clear complaint — only offered on complained rows (canClearComplaint);
  // the server 409s otherwise.
  let clearingComplaintId = $state<number | null>(null);
  let clearComplaintRowError = $state<Record<number, string>>({});

  async function handleClearComplaint(sub: Subscriber) {
    if (
      !confirm(
        `Clear the complaint for ${sub.email}? This unsubscribes them (not re-activates) and ` +
          'removes only the `complaint` suppression-list entry; any other suppression (for example a ' +
          'hard bounce) stays in force. ' +
          'It also overwrites their unsubscribe evidence (timestamp and source) with this action — the original unsubscribe evidence, if any, will be discarded.',
      )
    ) {
      return;
    }
    clearingComplaintId = sub.id;
    const { [sub.id]: _removed, ...rest } = clearComplaintRowError;
    clearComplaintRowError = rest;
    try {
      const res = await clearSubscriberComplaint(sub.id);
      replaceSubInList(res.subscriber);
    } catch (err) {
      if (handleAuthError(err)) return;
      clearComplaintRowError = {
        ...clearComplaintRowError,
        [sub.id]:
          err instanceof ApiError && err.message
            ? err.message
            : 'Could not clear the complaint. Please try again.',
      };
    } finally {
      clearingComplaintId = null;
    }
  }

  // Manual add — still requires double opt-in; see createSubscriber's doc
  // comment. Interests are offered as checkboxes over the same taxonomy the
  // Interests tab manages.
  let newSubEmail = $state('');
  let newSubInterestSlugs = $state<string[]>([]);
  let newSubNote = $state('');
  let createSubError = $state<string | null>(null);
  let createSubNotice = $state<string | null>(null);
  let creatingSub = $state(false);

  function toggleNewSubInterest(slug: string) {
    newSubInterestSlugs = newSubInterestSlugs.includes(slug)
      ? newSubInterestSlugs.filter((s) => s !== slug)
      : [...newSubInterestSlugs, slug];
  }

  async function submitCreateSubscriber(e: SubmitEvent) {
    e.preventDefault();
    const result = validateManualAddEmail(newSubEmail);
    if ('error' in result) {
      createSubError = result.error;
      return;
    }
    creatingSub = true;
    createSubError = null;
    createSubNotice = null;
    try {
      const res = await createSubscriber(result.email, newSubInterestSlugs, newSubNote);
      createSubNotice = res.message;
      newSubEmail = '';
      newSubInterestSlugs = [];
      newSubNote = '';
      subsPage = 1;
      void loadSubscribers();
    } catch (err) {
      if (handleAuthError(err)) return;
      createSubError =
        err instanceof ApiError && err.message
          ? err.message
          : 'Could not add this subscriber. Please try again.';
    } finally {
      creatingSub = false;
    }
  }

  // ── Suppressions (#0100) ─────────────────────────────────────────────────────
  let suppressions = $state<Suppression[]>([]);
  let suppressionsLoading = $state(true);
  let suppressionsError = $state<string | null>(null);

  async function loadSuppressions() {
    suppressionsLoading = true;
    suppressionsError = null;
    try {
      const res = await listSuppressions();
      suppressions = res.suppressions;
    } catch (err) {
      if (handleAuthError(err)) return;
      suppressionsError = 'Could not load the suppression list. Please try again.';
    } finally {
      suppressionsLoading = false;
    }
  }

  // Remove modal — note required, mirroring the Suppress modal.
  let removingSuppression = $state<Suppression | null>(null);
  let removeSuppressionNote = $state('');
  let removeSuppressionError = $state<string | null>(null);
  let removeSuppressionSubmitting = $state(false);
  let removeSuppressionModalEl = $state<HTMLDivElement | null>(null);

  $effect(() => {
    // #0120: see the deactivate modal's identical $effect above for why.
    if (removingSuppression) void tick().then(() => removeSuppressionModalEl?.focus());
  });

  function openRemoveSuppression(sup: Suppression) {
    removingSuppression = sup;
    removeSuppressionNote = '';
    removeSuppressionError = null;
  }

  function closeRemoveSuppression() {
    removingSuppression = null;
  }

  async function submitRemoveSuppression(e: SubmitEvent) {
    e.preventDefault();
    if (!removingSuppression) return;
    const result = validateSuppressionNote(removeSuppressionNote);
    if ('error' in result) {
      removeSuppressionError = result.error;
      return;
    }
    const target = removingSuppression;
    removeSuppressionSubmitting = true;
    removeSuppressionError = null;
    try {
      await removeSuppression(target.email, target.reason, result.note);
      suppressions = suppressions.filter(
        (s) => !(s.email === target.email && s.reason === target.reason),
      );
      removingSuppression = null;
    } catch (err) {
      if (handleAuthError(err)) return;
      removeSuppressionError =
        err instanceof ApiError && err.message
          ? err.message
          : 'Could not remove this suppression. Please try again.';
    } finally {
      removeSuppressionSubmitting = false;
    }
  }

  // ── Shared ──────────────────────────────────────────────────────────────────
  function handleAuthError(err: unknown): boolean {
    if (err instanceof ApiError && err.status === 401) {
      currentUser.set(null);
      navigate('/login');
      return true;
    }
    return false;
  }

  function go(view: 'account') {
    navigate(`/${view}`);
  }

  async function handleSignOut() {
    try {
      await logout();
    } catch {
      // Drop local state regardless of the server result.
    }
    currentUser.set(null);
    navigate('/login');
  }

  onMount(() => {
    void loadSettings();
    void loadUsers();
    void loadAudit();
    void loadInterests();
    void loadSubscribers();
    void loadSuppressions();
  });
</script>

<div class="app-shell">
  <header class="app-header">
    <h1 class="app-title">{APP_NAME}</h1>
    <nav class="nav-tabs" aria-label="Primary">
      <button type="button" class="nav-tab" onclick={() => go('account')}>Account</button>
      <button type="button" class="nav-tab active" aria-current="page">Admin</button>
    </nav>
    <Button variant="default" onclick={handleSignOut}>Sign out</Button>
  </header>

  {#if !$currentUser?.is_admin}
    <Panel title="Admin">
      <p class="text-error" role="alert">You do not have access to this area.</p>
    </Panel>
  {:else}
    <nav class="subtabs" aria-label="Admin sections">
      <button
        type="button"
        class="subtab"
        class:active={section === 'settings'}
        aria-current={section === 'settings' ? 'page' : undefined}
        onclick={() => (section = 'settings')}
      >Settings</button>
      <button
        type="button"
        class="subtab"
        class:active={section === 'users'}
        aria-current={section === 'users' ? 'page' : undefined}
        onclick={() => (section = 'users')}
      >Users</button>
      <button
        type="button"
        class="subtab"
        class:active={section === 'audit'}
        aria-current={section === 'audit' ? 'page' : undefined}
        onclick={() => (section = 'audit')}
      >Audit log</button>
      <button
        type="button"
        class="subtab"
        class:active={section === 'interests'}
        aria-current={section === 'interests' ? 'page' : undefined}
        onclick={() => (section = 'interests')}
      >Interests</button>
      <button
        type="button"
        class="subtab"
        class:active={section === 'subscribers'}
        aria-current={section === 'subscribers' ? 'page' : undefined}
        onclick={() => (section = 'subscribers')}
      >Subscribers</button>
      <button
        type="button"
        class="subtab"
        class:active={section === 'suppressions'}
        aria-current={section === 'suppressions' ? 'page' : undefined}
        onclick={() => (section = 'suppressions')}
      >Suppressions</button>
      <button
        type="button"
        class="subtab"
        class:active={section === 'campaigns'}
        aria-current={section === 'campaigns' ? 'page' : undefined}
        onclick={() => (section = 'campaigns')}
      >Campaigns</button>
      <button
        type="button"
        class="subtab"
        class:active={section === 'workshops'}
        aria-current={section === 'workshops' ? 'page' : undefined}
        onclick={() => (section = 'workshops')}
      >Workshops</button>
    </nav>

    {#if section === 'settings'}
      <Panel title="Settings">
        {#if settingsLoading}
          <p class="text-muted" role="status">Loading settings…</p>
        {:else if settingsError}
          <p class="text-error" role="alert">{settingsError}</p>
          <Button variant="primary" onclick={loadSettings}>Retry</Button>
        {:else}
          <div class="setting-row">
            <div class="setting-info">
              <span class="setting-name">Registrations enabled</span>
              <p class="text-muted setting-desc">
                When on, the registration form accepts new users. When off, the server is locked to
                existing users.
              </p>
            </div>
            <button
              type="button"
              class="toggle"
              class:on={regEnabled}
              role="switch"
              aria-checked={regEnabled}
              disabled={togglingRegistrations}
              onclick={toggleRegistrations}
            >
              {togglingRegistrations ? 'Saving…' : regEnabled ? 'On' : 'Off'}
            </button>
          </div>
          {#if settingsNotice}
            <p class="text-notice" role="status">{settingsNotice}</p>
          {/if}

          <div class="setting-row address-row">
            <div class="setting-info">
              <label class="setting-name" for="physical-address">Physical mailing address</label>
              <p class="text-muted setting-desc">
                Included in every campaign email. CAN-SPAM requires a physical postal address in
                every commercial message — campaigns cannot be sent until an address is set here.
              </p>
              <textarea
                id="physical-address"
                rows="3"
                bind:value={addressInput}
                disabled={savingAddress}
                oninput={() => {
                  addressError = null;
                  addressNotice = null;
                }}
                placeholder="Open Circuit SF, PO Box 1234, San Francisco, CA 94104"
              ></textarea>
              {#if !addressInput.trim()}
                <p class="text-error" role="alert">
                  Empty — campaigns cannot be sent until this is set.
                </p>
              {/if}
              {#if addressError}
                <p class="text-error" role="alert">{addressError}</p>
              {/if}
              {#if addressNotice}
                <p class="text-notice" role="status">{addressNotice}</p>
              {/if}
              <Button
                variant="primary"
                disabled={savingAddress || !addressDirty}
                onclick={saveAddress}
              >
                {savingAddress ? 'Saving…' : 'Save address'}
              </Button>
            </div>
          </div>
        {/if}
      </Panel>
    {/if}

    {#if section === 'users'}
      <Panel title="Users" noPadding={users.length > 0 && !usersLoading && !usersError}>
        {#if usersLoading}
          <p class="text-muted" role="status">Loading users…</p>
        {:else if usersError}
          <p class="text-error" role="alert">{usersError}</p>
          <Button variant="primary" onclick={loadUsers}>Retry</Button>
        {:else if users.length === 0}
          <p class="text-muted">No users found.</p>
        {:else}
          <div class="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>Email</th>
                  <th>Admin</th>
                  <th>Status</th>
                  <th>Last login</th>
                  <th class="actions-col">Actions</th>
                </tr>
              </thead>
              <tbody>
                {#each users as user (user.id)}
                  <tr>
                    <td>{user.email}</td>
                    <td>{user.is_admin ? 'Admin' : '—'}</td>
                    <td>
                      <span
                        class="badge"
                        class:badge-success={user.active}
                        class:badge-danger={!user.active}
                      >
                        {user.active ? 'Active' : 'Inactive'}
                      </span>
                    </td>
                    <td>{user.last_login_at ? formatDateTime(user.last_login_at) : 'Never'}</td>
                    <td class="actions-col">
                      {#if canDeactivate(user, $currentUser?.id ?? -1)}
                        <Button variant="danger" onclick={() => openDeactivate(user)}>Deactivate</Button>
                      {:else if !user.active}
                        <Button
                          disabled={reactivatingId === user.id}
                          onclick={() => handleReactivate(user)}
                        >
                          {reactivatingId === user.id ? 'Reactivating…' : 'Reactivate'}
                        </Button>
                      {:else}
                        <span class="text-muted">—</span>
                      {/if}
                    </td>
                  </tr>
                  {#if userRowError[user.id]}
                    <tr>
                      <td colspan="5">
                        <p class="text-error" role="alert">{userRowError[user.id]}</p>
                      </td>
                    </tr>
                  {/if}
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </Panel>

      {#if deactivatingUser}
        <div
          class="modal-backdrop"
          role="presentation"
          onclick={closeDeactivate}
          onkeydown={(e) => {
            if (isModalEscape(e)) closeDeactivate();
          }}
        >
          <div
            class="modal"
            role="dialog"
            tabindex="-1"
            aria-modal="true"
            aria-label="Deactivate user"
            bind:this={deactivateModalEl}
            onclick={(e) => e.stopPropagation()}
            onkeydown={(e) => {
              if (isModalEscape(e)) closeDeactivate();
            }}
          >
            <h2 class="modal-title">Deactivate {deactivatingUser.email}</h2>
            <form onsubmit={submitDeactivate}>
              <div class="field">
                <label for="deact-reason">Reason</label>
                <select
                  id="deact-reason"
                  bind:value={deactReason}
                  disabled={deactSubmitting}
                  onchange={() => (deactError = null)}
                >
                  {#each DEACTIVATION_REASONS as r (r.value)}
                    <option value={r.value}>{r.label}</option>
                  {/each}
                </select>
              </div>
              <div class="field">
                <label for="deact-note">
                  Note {deactNoteRequired ? '(required)' : '(optional)'}
                </label>
                <textarea
                  id="deact-note"
                  rows="3"
                  bind:value={deactNote}
                  disabled={deactSubmitting}
                  oninput={() => (deactError = null)}
                ></textarea>
              </div>
              {#if deactError}
                <p class="text-error" role="alert">{deactError}</p>
              {/if}
              <div class="row" style="margin-top: var(--space-3);">
                <Button type="submit" variant="danger" disabled={deactSubmitting}>
                  {deactSubmitting ? 'Deactivating…' : 'Deactivate'}
                </Button>
                <Button disabled={deactSubmitting} onclick={closeDeactivate}>Cancel</Button>
              </div>
            </form>
          </div>
        </div>
      {/if}
    {/if}

    {#if section === 'audit'}
      <Panel title="Audit log">
        <form class="inline-form" onsubmit={applyAuditFilter} style="margin-bottom: var(--space-3);">
          <label for="audit-user" style="white-space: nowrap;">Filter by user id</label>
          <input
            id="audit-user"
            type="text"
            inputmode="numeric"
            placeholder="e.g. 5"
            bind:value={auditUserIdRaw}
            style="width: 8rem;"
          />
          <label for="audit-target-type" style="white-space: nowrap;">Target type</label>
          <input
            id="audit-target-type"
            type="text"
            placeholder="e.g. email_campaign"
            bind:value={auditTargetTypeRaw}
            style="width: 11rem;"
          />
          <label for="audit-target-id" style="white-space: nowrap;">Target id</label>
          <input
            id="audit-target-id"
            type="text"
            inputmode="numeric"
            placeholder="e.g. 42"
            bind:value={auditTargetIdRaw}
            style="width: 8rem;"
          />
          <Button type="submit" variant="primary">Apply</Button>
          {#if auditHasFilter}
            <Button onclick={clearAuditFilter}>Clear</Button>
          {/if}
        </form>
        {#if auditFilterError}
          <p class="text-error" role="alert">{auditFilterError}</p>
        {/if}

        {#if auditLoading}
          <p class="text-muted" role="status">Loading audit log…</p>
        {:else if auditError}
          <p class="text-error" role="alert">{auditError}</p>
          <Button variant="primary" onclick={loadAudit}>Retry</Button>
        {:else if auditEntries.length === 0}
          <p class="text-muted">No audit entries.</p>
        {:else}
          <div class="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>When</th>
                  <th>Action</th>
                  <th>Actor</th>
                  <th>Target</th>
                  <th>Metadata</th>
                  <th>IP</th>
                </tr>
              </thead>
              <tbody>
                {#each auditEntries as entry (entry.id)}
                  <tr>
                    <td>{formatDateTime(entry.created_at)}</td>
                    <td class="mono">{entry.action}</td>
                    <td>{actorLabel(entry)}</td>
                    <td>{targetLabel(entry)}</td>
                    <td class="meta-cell mono">{formatMetadata(entry.metadata)}</td>
                    <td>{entry.ip_address ?? '—'}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>

          <div class="pager">
            <Button
              disabled={!auditPaging.hasPrev}
              onclick={() => auditGoTo(auditPaging.page - 1)}
            >Previous</Button>
            <span class="text-muted">
              {auditPaging.firstItem}–{auditPaging.lastItem} of {auditPaging.total} · page
              {auditPaging.page} of {auditPaging.totalPages}
            </span>
            <Button
              disabled={!auditPaging.hasNext}
              onclick={() => auditGoTo(auditPaging.page + 1)}
            >Next</Button>
          </div>
        {/if}
      </Panel>
    {/if}

    {#if section === 'interests'}
      <Panel title="Add an interest">
        <form class="interest-create-form" onsubmit={submitCreateInterest}>
          <div class="field">
            <label for="new-interest-slug">Slug</label>
            <input
              id="new-interest-slug"
              type="text"
              bind:value={newSlug}
              disabled={creating}
              placeholder="home-automation"
              oninput={() => (createError = null)}
            />
            {#if newSlugInvalid}
              <p class="text-warn" role="status">
                Lowercase letters, numbers, and single hyphens only (e.g. "home-automation").
              </p>
            {/if}
          </div>
          <div class="field">
            <label for="new-interest-name">Name</label>
            <input
              id="new-interest-name"
              type="text"
              bind:value={newName}
              disabled={creating}
              placeholder="Home Automation"
              oninput={() => (createError = null)}
            />
          </div>
          <div class="field">
            <label for="new-interest-description">Description (optional)</label>
            <input
              id="new-interest-description"
              type="text"
              bind:value={newDescription}
              disabled={creating}
            />
          </div>
          {#if createError}
            <p class="text-error" role="alert">{createError}</p>
          {/if}
          <Button type="submit" variant="primary" disabled={creating}>
            {creating ? 'Adding…' : 'Add interest'}
          </Button>
        </form>
      </Panel>

      <Panel
        title="Interests"
        noPadding={interestsSorted.length > 0 && !interestsLoading && !interestsError}
      >
        {#if interestsLoading}
          <p class="text-muted" role="status">Loading interests…</p>
        {:else if interestsError}
          <p class="text-error" role="alert">{interestsError}</p>
          <Button variant="primary" onclick={loadInterests}>Retry</Button>
        {:else if interestsSorted.length === 0}
          <p class="text-muted">No interests yet.</p>
        {:else}
          <div class="table-scroll">
            <table>
              <thead>
                <tr>
                  <th class="actions-col">Order</th>
                  <th>Slug</th>
                  <th>Name</th>
                  <th>Subscribers</th>
                  <th>Status</th>
                  <th class="actions-col">Actions</th>
                </tr>
              </thead>
              <tbody>
                {#each interestsSorted as it, i (it.id)}
                  <tr>
                    <td class="actions-col">
                      <button
                        type="button"
                        class="order-btn"
                        disabled={i === 0 || reorderingId !== null}
                        onclick={() => moveInterest(it, 'up')}
                        aria-label={`Move ${it.name} up`}
                      >↑</button>
                      <button
                        type="button"
                        class="order-btn"
                        disabled={i === interestsSorted.length - 1 || reorderingId !== null}
                        onclick={() => moveInterest(it, 'down')}
                        aria-label={`Move ${it.name} down`}
                      >↓</button>
                    </td>
                    <td class="mono">{it.slug}</td>
                    <td>{it.name}</td>
                    <td>{it.subscriber_count}</td>
                    <td>
                      <span
                        class="badge"
                        class:badge-success={it.active}
                        class:badge-danger={!it.active}
                      >
                        {it.active ? 'Active' : 'Inactive'}
                      </span>
                    </td>
                    <td class="actions-col">
                      <Button onclick={() => openEditInterest(it)}>Edit</Button>
                      <Button
                        variant="danger"
                        disabled={hasSubscribers(it) || deletingId === it.id}
                        onclick={() => handleDeleteInterest(it)}
                      >
                        {deletingId === it.id ? 'Deleting…' : 'Delete'}
                      </Button>
                      {#if hasSubscribers(it)}
                        <p class="text-muted interest-delete-hint">
                          {it.subscriber_count} subscriber{it.subscriber_count === 1 ? '' : 's'} —
                          deactivate instead
                        </p>
                      {/if}
                    </td>
                  </tr>
                  {#if deleteRowError[it.id]}
                    <tr>
                      <td colspan="6">
                        <p class="text-error" role="alert">{deleteRowError[it.id]}</p>
                      </td>
                    </tr>
                  {/if}
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </Panel>

      {#if editingInterest}
        <div
          class="modal-backdrop"
          role="presentation"
          onclick={closeEditInterest}
          onkeydown={(e) => {
            if (isModalEscape(e)) closeEditInterest();
          }}
        >
          <div
            class="modal"
            role="dialog"
            tabindex="-1"
            aria-modal="true"
            aria-label="Edit interest"
            bind:this={editInterestModalEl}
            onclick={(e) => e.stopPropagation()}
            onkeydown={(e) => {
              if (isModalEscape(e)) closeEditInterest();
            }}
          >
            <h2 class="modal-title">Edit {editingInterest.name}</h2>
            <p class="text-muted mono">slug: {editingInterest.slug} (fixed — cannot be renamed)</p>
            <form onsubmit={submitEditInterest}>
              <div class="field">
                <label for="edit-interest-name">Name</label>
                <input
                  id="edit-interest-name"
                  type="text"
                  bind:value={editName}
                  disabled={editSubmitting}
                  oninput={() => (editError = null)}
                />
              </div>
              <div class="field">
                <label for="edit-interest-description">Description</label>
                <textarea
                  id="edit-interest-description"
                  rows="2"
                  bind:value={editDescription}
                  disabled={editSubmitting}
                  oninput={() => (editError = null)}
                ></textarea>
              </div>
              <div class="field">
                <label class="checkbox-label">
                  <input type="checkbox" bind:checked={editActive} disabled={editSubmitting} />
                  Active (visible on the public signup form)
                </label>
              </div>
              {#if editDeactivating}
                <p class="text-warn" role="status">
                  Deactivating removes this interest from the public signup form. Subscribers who
                  already selected it keep their selection and history.
                </p>
              {/if}
              {#if editError}
                <p class="text-error" role="alert">{editError}</p>
              {/if}
              <div class="row" style="margin-top: var(--space-3);">
                <Button type="submit" variant="primary" disabled={editSubmitting}>
                  {editSubmitting ? 'Saving…' : 'Save'}
                </Button>
                <Button disabled={editSubmitting} onclick={closeEditInterest}>Cancel</Button>
              </div>
            </form>
          </div>
        </div>
      {/if}
    {/if}

    {#if section === 'subscribers'}
      <Panel title="Subscribers by status">
        <div class="status-counts">
          {#each SUBSCRIBER_STATUSES as st (st)}
            <div class="status-count">
              <span class="badge {subscriberStatusBadgeClass(st)}">{subscriberStatusLabel(st)}</span>
              <span class="status-count-num">{subsCounts[st as keyof SubscriberStatusCounts]}</span>
            </div>
          {/each}
        </div>
      </Panel>

      <Panel title="Add a subscriber">
        <p class="text-muted">
          Still requires double opt-in — a brand-new address is added as
          <strong>pending</strong> with a confirmation email queued, never active directly.
        </p>
        <form class="interest-create-form" onsubmit={submitCreateSubscriber}>
          <div class="field">
            <label for="new-sub-email">Email</label>
            <input
              id="new-sub-email"
              type="text"
              bind:value={newSubEmail}
              disabled={creatingSub}
              placeholder="person@example.com"
              oninput={() => (createSubError = null)}
            />
          </div>
          {#if interests.length > 0}
            <div class="field">
              <span class="setting-name">Interests (optional)</span>
              <div class="checkbox-group">
                {#each sortedInterests(interests).filter((i) => i.active) as it (it.id)}
                  <label class="checkbox-label">
                    <input
                      type="checkbox"
                      checked={newSubInterestSlugs.includes(it.slug)}
                      disabled={creatingSub}
                      onchange={() => toggleNewSubInterest(it.slug)}
                    />
                    {it.name}
                  </label>
                {/each}
              </div>
            </div>
          {/if}
          <div class="field">
            <label for="new-sub-note">Note (optional, recorded in the audit log)</label>
            <input id="new-sub-note" type="text" bind:value={newSubNote} disabled={creatingSub} />
          </div>
          {#if createSubError}
            <p class="text-error" role="alert">{createSubError}</p>
          {/if}
          {#if createSubNotice}
            <p class="text-notice" role="status">{createSubNotice}</p>
          {/if}
          <Button type="submit" variant="primary" disabled={creatingSub}>
            {creatingSub ? 'Adding…' : 'Add subscriber'}
          </Button>
        </form>
      </Panel>

      <Panel title="Search &amp; filter">
        <form class="inline-form" onsubmit={applySubsFilters}>
          <label for="subs-status">Status</label>
          <select id="subs-status" bind:value={subsStatusFilter}>
            <option value="">Any</option>
            {#each SUBSCRIBER_STATUSES as st (st)}
              <option value={st}>{subscriberStatusLabel(st)}</option>
            {/each}
          </select>
          <label for="subs-interest">Interest</label>
          <select id="subs-interest" bind:value={subsInterestFilter}>
            <option value="">Any</option>
            {#each sortedInterests(interests) as it (it.id)}
              <option value={String(it.id)}>{it.name}</option>
            {/each}
          </select>
          <label for="subs-q">Email contains</label>
          <input id="subs-q" type="text" bind:value={subsQueryRaw} placeholder="e.g. example.com" />
          <Button type="submit" variant="primary">Apply</Button>
          {#if subsStatusFilter || subsInterestFilter || subsQueryApplied}
            <Button onclick={clearSubsFilters}>Clear</Button>
          {/if}
        </form>
      </Panel>

      <Panel title="Subscribers" noPadding={subs.length > 0 && !subsLoading && !subsError}>
        {#if subsLoading}
          <p class="text-muted" role="status">Loading subscribers…</p>
        {:else if subsError}
          <p class="text-error" role="alert">{subsError}</p>
          <Button variant="primary" onclick={loadSubscribers}>Retry</Button>
        {:else if subs.length === 0}
          <p class="text-muted">No subscribers match this filter.</p>
        {:else}
          <div class="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>Email</th>
                  <th>Status</th>
                  <th>Signed up</th>
                  <th class="actions-col">Actions</th>
                </tr>
              </thead>
              <tbody>
                {#each subs as sub (sub.id)}
                  <tr>
                    <td class="mono">{sub.email}</td>
                    <td>
                      <span class="badge {subscriberStatusBadgeClass(sub.status)}">
                        {subscriberStatusLabel(sub.status)}
                      </span>
                    </td>
                    <td>{formatDateTime(sub.created_at)}</td>
                    <td class="actions-col">
                      <Button onclick={() => openSubscriberDetail(sub.id)}>View</Button>
                      <Button onclick={() => openSuppress(sub)}>Suppress</Button>
                      {#if canClearComplaint(sub.status)}
                        <Button
                          variant="danger"
                          disabled={clearingComplaintId === sub.id}
                          onclick={() => handleClearComplaint(sub)}
                        >
                          {clearingComplaintId === sub.id ? 'Clearing…' : 'Clear complaint'}
                        </Button>
                      {/if}
                    </td>
                  </tr>
                  {#if clearComplaintRowError[sub.id]}
                    <tr>
                      <td colspan="4">
                        <p class="text-error" role="alert">{clearComplaintRowError[sub.id]}</p>
                      </td>
                    </tr>
                  {/if}
                {/each}
              </tbody>
            </table>
          </div>

          <div class="pager">
            <Button disabled={!subsPaging.hasPrev} onclick={() => subsGoTo(subsPaging.page - 1)}>Previous</Button>
            <span class="text-muted">
              {subsPaging.firstItem}–{subsPaging.lastItem} of {subsPaging.total} · page
              {subsPaging.page} of {subsPaging.totalPages}
            </span>
            <Button disabled={!subsPaging.hasNext} onclick={() => subsGoTo(subsPaging.page + 1)}>Next</Button>
          </div>
        {/if}
      </Panel>

      {#if viewingSubscriber || viewingLoading || viewingError}
        <div
          class="modal-backdrop"
          role="presentation"
          onclick={closeSubscriberDetail}
          onkeydown={(e) => {
            if (isModalEscape(e)) closeSubscriberDetail();
          }}
        >
          <div
            class="modal modal-wide"
            role="dialog"
            tabindex="-1"
            aria-modal="true"
            aria-label="Subscriber detail"
            bind:this={subscriberDetailModalEl}
            onclick={(e) => e.stopPropagation()}
            onkeydown={(e) => {
              if (isModalEscape(e)) closeSubscriberDetail();
            }}
          >
            {#if viewingLoading}
              <p class="text-muted" role="status">Loading…</p>
            {:else if viewingError}
              <p class="text-error" role="alert">{viewingError}</p>
            {:else if viewingSubscriber}
              <h2 class="modal-title">{viewingSubscriber.email}</h2>
              <p>
                <span class="badge {subscriberStatusBadgeClass(viewingSubscriber.status)}">
                  {subscriberStatusLabel(viewingSubscriber.status)}
                </span>
              </p>

              <h3 class="detail-heading">Consent evidence</h3>
              <p class="mono">{signupEvidenceSummary(viewingSubscriber)}</p>
              {#if viewingSubscriber.confirmed_at}
                <p>Confirmed: {formatDateTime(viewingSubscriber.confirmed_at)}</p>
              {:else}
                <p class="text-muted">Not yet confirmed.</p>
              {/if}
              {#if viewingSubscriber.utm_source || viewingSubscriber.utm_medium || viewingSubscriber.utm_campaign}
                <p class="text-muted">
                  UTM: {viewingSubscriber.utm_source ?? '—'} / {viewingSubscriber.utm_medium ?? '—'} /
                  {viewingSubscriber.utm_campaign ?? '—'}
                </p>
              {/if}
              {#if viewingSubscriber.unsubscribed_at}
                <p class="text-muted">
                  Unsubscribed {formatDateTime(viewingSubscriber.unsubscribed_at)}
                  (source: {viewingSubscriber.unsubscribe_source ?? 'unknown'})
                </p>
              {/if}

              <h3 class="detail-heading">Interests</h3>
              {#if viewingSubscriber.interests && viewingSubscriber.interests.length > 0}
                <ul class="detail-interest-list">
                  {#each viewingSubscriber.interests as it (it.id)}
                    <li>{it.name}</li>
                  {/each}
                </ul>
              {:else}
                <p class="text-muted">No interests selected — general announcements only.</p>
              {/if}

              <h3 class="detail-heading">Soft bounces</h3>
              <p class="text-muted">{softBounceSummary(viewingSubscriber)}</p>

              <h3 class="detail-heading">Email event history</h3>
              {#if viewingSubscriber.email_events.length === 0}
                <!-- #0038's SES bounce/complaint ingestion is built and wired; this is empty
                     because no SES account exists yet (CLAUDE.md §10 item 2), so nothing has
                     ever posted an event. The copy below says "once it's set up" for that
                     reason — the missing piece is configuration, not code (#0172). -->
                <p class="text-muted">
                  No events recorded yet. Bounce and complaint tracking will appear here once
                  it's set up.
                </p>
              {:else}
                <ul class="detail-interest-list">
                  {#each viewingSubscriber.email_events as _event, i (i)}
                    <li>{JSON.stringify(_event)}</li>
                  {/each}
                </ul>
              {/if}

              <div class="row" style="margin-top: var(--space-4);">
                <Button onclick={closeSubscriberDetail}>Close</Button>
              </div>
            {/if}
          </div>
        </div>
      {/if}

      {#if suppressingSubscriber}
        <div
          class="modal-backdrop"
          role="presentation"
          onclick={closeSuppress}
          onkeydown={(e) => {
            if (isModalEscape(e)) closeSuppress();
          }}
        >
          <div
            class="modal"
            role="dialog"
            tabindex="-1"
            aria-modal="true"
            aria-label="Suppress subscriber"
            bind:this={suppressModalEl}
            onclick={(e) => e.stopPropagation()}
            onkeydown={(e) => {
              if (isModalEscape(e)) closeSuppress();
            }}
          >
            <h2 class="modal-title">Suppress {suppressingSubscriber.email}</h2>
            <p>
              Unsubscribes this address and permanently adds it to the suppression list. A
              future resignup by this address will not be delivered mail — this is a
              stronger action than a plain unsubscribe, and can be removed later from
              Admin → Suppressions.
            </p>
            <form onsubmit={submitSuppress}>
              <div class="field">
                <label for="suppress-note">Note (required)</label>
                <textarea
                  id="suppress-note"
                  rows="3"
                  bind:value={suppressNote}
                  disabled={suppressSubmitting}
                  oninput={() => (suppressError = null)}
                ></textarea>
              </div>
              {#if suppressError}
                <p class="text-error" role="alert">{suppressError}</p>
              {/if}
              {#if suppressNotice}
                <p class="text-notice" role="status">{suppressNotice}</p>
              {/if}
              <div class="row" style="margin-top: var(--space-3);">
                <Button type="submit" variant="danger" disabled={suppressSubmitting}>
                  {suppressSubmitting ? 'Suppressing…' : 'Suppress'}
                </Button>
                <Button disabled={suppressSubmitting} onclick={closeSuppress}>Cancel</Button>
              </div>
            </form>
          </div>
        </div>
      {/if}
    {/if}

    {#if section === 'suppressions'}
      <Panel title="Suppressions" noPadding={suppressions.length > 0 && !suppressionsLoading && !suppressionsError}>
        {#if suppressionsLoading}
          <p class="text-muted" role="status">Loading suppressions…</p>
        {:else if suppressionsError}
          <p class="text-error" role="alert">{suppressionsError}</p>
          <Button variant="primary" onclick={loadSuppressions}>Retry</Button>
        {:else if suppressions.length === 0}
          <p class="text-muted">No addresses are suppressed.</p>
        {:else}
          <div class="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>Email</th>
                  <th>Reason</th>
                  <th>Note</th>
                  <th>Added</th>
                  <th>Subscriber status</th>
                  <th class="actions-col">Actions</th>
                </tr>
              </thead>
              <tbody>
                {#each suppressions as sup (`${sup.email}:${sup.reason}`)}
                  <tr>
                    <td class="mono">{sup.email}</td>
                    <td><span class="badge">{suppressionReasonLabel(sup.reason)}</span></td>
                    <td>{sup.note ?? '—'}</td>
                    <td>{formatDateTime(sup.created_at)}</td>
                    <td>
                      {#if sup.subscriber_status}
                        <span class="badge {subscriberStatusBadgeClass(sup.subscriber_status)}">
                          {subscriberStatusLabel(sup.subscriber_status)}
                        </span>
                      {:else}
                        <span class="text-muted">No subscriber record</span>
                      {/if}
                    </td>
                    <td class="actions-col">
                      {#if suppressionRemovalBlocked(sup.reason, sup.subscriber_status !== null)}
                        <Button variant="danger" disabled>Remove</Button>
                        <p class="text-muted interest-delete-hint">
                          {suppressionRemovalBlocked(sup.reason, sup.subscriber_status !== null)}
                        </p>
                      {:else}
                        <Button variant="danger" onclick={() => openRemoveSuppression(sup)}>Remove</Button>
                      {/if}
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </Panel>

      {#if removingSuppression}
        <div
          class="modal-backdrop"
          role="presentation"
          onclick={closeRemoveSuppression}
          onkeydown={(e) => {
            if (isModalEscape(e)) closeRemoveSuppression();
          }}
        >
          <div
            class="modal"
            role="dialog"
            tabindex="-1"
            aria-modal="true"
            aria-label="Remove suppression"
            bind:this={removeSuppressionModalEl}
            onclick={(e) => e.stopPropagation()}
            onkeydown={(e) => {
              if (isModalEscape(e)) closeRemoveSuppression();
            }}
          >
            <h2 class="modal-title">
              Remove {suppressionReasonLabel(removingSuppression.reason)} suppression for {removingSuppression.email}
            </h2>
            <p>
              This does not resubscribe the address; it must opt in again. It only removes this one
              reason — any other suppression for the same address (for example a hard bounce) stays in
              force.
            </p>
            <form onsubmit={submitRemoveSuppression}>
              <div class="field">
                <label for="remove-suppression-note">Note (required)</label>
                <textarea
                  id="remove-suppression-note"
                  rows="3"
                  bind:value={removeSuppressionNote}
                  disabled={removeSuppressionSubmitting}
                  oninput={() => (removeSuppressionError = null)}
                ></textarea>
              </div>
              {#if removeSuppressionError}
                <p class="text-error" role="alert">{removeSuppressionError}</p>
              {/if}
              <div class="row" style="margin-top: var(--space-3);">
                <Button type="submit" variant="danger" disabled={removeSuppressionSubmitting}>
                  {removeSuppressionSubmitting ? 'Removing…' : 'Remove'}
                </Button>
                <Button disabled={removeSuppressionSubmitting} onclick={closeRemoveSuppression}>Cancel</Button>
              </div>
            </form>
          </div>
        </div>
      {/if}
    {/if}

    {#if section === 'campaigns'}
      <Campaigns
        onGoToSettings={() => (section = 'settings')}
        onGoToAudit={goToCampaignAudit}
        openCampaignId={pendingOpenCampaignId}
        onOpenedCampaign={() => (pendingOpenCampaignId = null)}
      />
    {/if}

    {#if section === 'workshops'}
      <Workshops onAnnounced={goToAnnouncedCampaign} />
    {/if}
  {/if}
</div>

<style>
  .nav-tabs {
    display: flex;
    gap: var(--space-1);
    flex: 1;
    padding: 0 var(--space-2);
  }
  .nav-tab {
    background: none;
    border: none;
    padding: var(--space-1) var(--space-2);
    border-radius: var(--radius);
    cursor: pointer;
    color: var(--text-muted);
    font-size: var(--fs-md);
    font-family: var(--font);
    /* Comfortable tap target on mobile */
    min-height: 40px;
    display: inline-flex;
    align-items: center;
  }
  .nav-tab.active {
    background: var(--accent-subtle);
    color: var(--accent);
    font-weight: 600;
  }
  .nav-tab:hover:not(.active) {
    background: var(--bg-subtle);
    color: var(--text);
  }
  .subtabs {
    display: flex;
    gap: var(--space-2);
    margin-bottom: var(--space-4);
    flex-wrap: wrap;
  }
  .subtab {
    background: var(--bg-panel);
    border: var(--border-w) solid var(--border);
    border-bottom-width: 2px;
    border-radius: var(--radius);
    padding: var(--space-2) var(--space-4);
    cursor: pointer;
    font-size: var(--fs-md);
    font-family: var(--font);
    color: var(--text-muted);
    /* Comfortable tap target on mobile */
    min-height: 40px;
    display: inline-flex;
    align-items: center;
  }
  .subtab.active {
    border-bottom-color: var(--accent);
    color: var(--accent);
    font-weight: 600;
  }
  .subtab:hover:not(.active) {
    background: var(--bg-subtle);
    color: var(--text);
  }

  @media (max-width: 480px) {
    /* Stack nav-tabs below title on narrow screens */
    .nav-tabs {
      order: 3;
      flex: 0 0 100%;
      padding: 0;
      flex-wrap: wrap;
    }
    .nav-tab {
      font-size: var(--fs-base);
      padding: var(--space-1) var(--space-3);
    }
    /* Sub-tabs: already flex-wrap:wrap; reduce padding so 4 fit on 2 rows */
    .subtab {
      font-size: var(--fs-base);
      padding: var(--space-2) var(--space-3);
    }
    /* Pager: allow center + wrap */
    .pager {
      flex-wrap: wrap;
      justify-content: center;
    }
    /* Audit filter inline-form label + input on narrow width */
    .inline-form {
      gap: var(--space-2);
    }
  }
  .setting-row {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: var(--space-4);
  }
  .setting-info {
    flex: 1;
  }
  .setting-name {
    display: block;
    font-weight: 600;
    color: var(--text);
    font-size: var(--fs-base);
  }
  .setting-desc {
    margin: var(--space-1) 0 0;
    font-size: var(--fs-sm);
  }
  .address-row {
    margin-top: var(--space-4);
    padding-top: var(--space-4);
    border-top: var(--border-w) solid var(--border);
  }
  .address-row textarea {
    margin-top: var(--space-2);
    resize: vertical;
  }
  .address-row .text-error,
  .address-row .text-notice {
    margin: var(--space-2) 0 0;
  }
  .address-row :global(button) {
    margin-top: var(--space-3);
  }
  .toggle {
    flex-shrink: 0;
    min-width: 4rem;
    border: var(--border-w) solid var(--border-strong);
    border-radius: 10px;
    padding: var(--space-1) var(--space-3);
    background: var(--bg-subtle);
    cursor: pointer;
    font-size: var(--fs-sm);
    font-weight: 600;
    font-family: var(--font);
    color: var(--text-muted);
  }
  .toggle.on {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .toggle:disabled {
    opacity: 0.5;
    cursor: default;
  }
  .inline-form {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    flex-wrap: wrap;
  }
  .actions-col {
    white-space: nowrap;
  }
  .mono {
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    overflow-wrap: anywhere;
  }
  .meta-cell {
    max-width: 280px;
    overflow-wrap: anywhere;
  }
  .pager {
    display: flex;
    align-items: center;
    gap: var(--space-4);
    margin-top: var(--space-3);
    padding-top: var(--space-3);
    border-top: var(--border-w) solid var(--border);
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
    overflow-wrap: anywhere;
  }
  .interest-create-form {
    max-width: 32rem;
  }
  .order-btn {
    background: var(--bg-subtle);
    border: var(--border-w) solid var(--border-strong);
    border-radius: var(--radius);
    padding: 0 var(--space-2);
    cursor: pointer;
    font-family: var(--font);
    line-height: 1.8;
    color: var(--text);
  }
  .order-btn:disabled {
    opacity: 0.4;
    cursor: default;
  }
  .interest-delete-hint {
    margin: var(--space-1) 0 0;
    font-size: var(--fs-sm);
    white-space: nowrap;
  }
  .checkbox-label {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    cursor: pointer;
  }

  /* ── Subscribers (#0032) ──────────────────────────────────────────────────── */
  .status-counts {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-4);
  }
  .status-count {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }
  .status-count-num {
    font-weight: 600;
    font-size: var(--fs-lg);
  }
  .checkbox-group {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2) var(--space-4);
    margin-top: var(--space-1);
  }
  .modal-wide {
    max-width: 640px;
    max-height: 85vh;
    overflow-y: auto;
  }
  .detail-heading {
    font-size: var(--fs-base);
    font-weight: 600;
    margin: var(--space-4) 0 var(--space-1);
  }
  .detail-interest-list {
    margin: 0;
    padding-left: var(--space-4);
  }
</style>
