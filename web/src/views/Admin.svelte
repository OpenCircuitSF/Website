<!--
  Admin view. Admin-only — rendered only when currentUser.is_admin; a
  non-admin who somehow reaches it sees an access-denied notice rather than any
  admin data. It ties together three backend areas behind a single view with a
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
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { currentUser } from '../lib/stores';
  import { navigate } from '../lib/router';
  import {
    getSettings,
    updateSetting,
    listUsers,
    deactivateUser,
    reactivateUser,
    listAudit,
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
    registrationsEnabled,
  } from '../lib/admin';
  import type { Setting, AdminUser, AuditEntry } from '../lib/types';
  import Button from '../lib/Button.svelte';
  import Panel from '../lib/Panel.svelte';
  import { APP_NAME } from '../lib/branding';

  type Section = 'settings' | 'users' | 'audit';
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
  let auditFilterError = $state<string | null>(null);
  const auditPaging = $derived(pageInfo(auditTotal, auditPage, AUDIT_PER_PAGE));

  async function loadAudit() {
    auditLoading = true;
    auditError = null;
    try {
      const res = await listAudit(auditPage, AUDIT_PER_PAGE, auditUserIdFilter ?? undefined);
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
    const parsed = parseUserIdFilter(auditUserIdRaw);
    if ('error' in parsed) {
      auditFilterError = parsed.error;
      return;
    }
    auditFilterError = null;
    auditUserIdFilter = parsed.userId;
    auditPage = 1;
    void loadAudit();
  }

  function clearAuditFilter() {
    auditUserIdRaw = '';
    auditUserIdFilter = null;
    auditFilterError = null;
    auditPage = 1;
    void loadAudit();
  }

  function auditGoTo(page: number) {
    auditPage = page;
    void loadAudit();
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
                every commercial message — the send worker refuses to start a campaign while this
                is empty.
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
            if (e.key === 'Escape') closeDeactivate();
          }}
        >
          <div
            class="modal"
            role="dialog"
            tabindex="-1"
            aria-modal="true"
            aria-label="Deactivate user"
            onclick={(e) => e.stopPropagation()}
            onkeydown={(e) => e.stopPropagation()}
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
          <Button type="submit" variant="primary">Apply</Button>
          {#if auditUserIdFilter !== null}
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
</style>
