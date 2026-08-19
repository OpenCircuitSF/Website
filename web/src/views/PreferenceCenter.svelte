<script lang="ts">
  // PreferenceCenter — the token-authenticated preference center (#0031, PRD
  // §6.4). Two entry points:
  //
  //   1. Standalone, at /preferences?token=... (every email footer link):
  //      fetches GET /api/preferences on mount. Email comes back masked
  //      ("b•••••n@gmail.com") -- the manage_token is long-lived and this
  //      route has no way to tell "just confirmed" from "three-month-old
  //      bookmark", so it always masks (see preferences.go's package doc
  //      comment).
  //   2. Embedded, from ConfirmSubscription.svelte right after a successful
  //      POST /api/subscribe/confirm: the parent already holds the
  //      unmasked email (proof of fresh mailbox control) and the full
  //      interest state from that same response, so it's passed in via
  //      props and no second fetch happens. showHeading is false in this
  //      mode -- ConfirmSubscription owns the page's single <h1>, this
  //      component only renders <h2> subsections, so heading order stays
  //      correct regardless of which entry point rendered it.
  //
  // An empty interest selection is a first-class, explicitly-explained
  // state (#0031's notes: "uncheck everything" is a common way people try
  // to unsubscribe without meaning to fully leave) -- see the persistent
  // helper text under the checkbox grid and the save-confirmation message.
  // "Unsubscribe from everything" is a separate, equally prominent action
  // that PATCHes a distinct payload (unsubscribe: true) rather than being
  // inferred from zero interests.
  import { onMount } from 'svelte';
  import { currentRoute, navigate } from '../lib/router';
  import { getPreferences, patchPreferences, ApiError } from '../lib/api';
  import {
    buildSaveInterestsPatch,
    buildUnsubscribeEverythingPatch,
    toggleSlug,
    inactiveStatusMessage,
    showSubscribeAgainAffordance,
    COMPLAINED_CONTACT_EMAIL,
  } from '../lib/subscribe';
  import type { PublicInterest } from '../lib/types';
  import Panel from '../lib/Panel.svelte';
  import Button from '../lib/Button.svelte';

  interface Props {
    showHeading?: boolean;
    /** Present only for the embedded (fresh-from-confirm) entry point --
     * see the component doc comment above. */
    initialToken?: string;
    initialEmail?: string;
    initialInterests?: string[];
    initialActiveInterests?: PublicInterest[];
  }

  let {
    showHeading = true,
    initialToken,
    initialEmail,
    initialInterests,
    initialActiveInterests,
  }: Props = $props();

  // svelte-ignore state_referenced_locally -- initialToken/initialEmail/
  // initialInterests/initialActiveInterests are a one-time seed from
  // ConfirmSubscription's fresh confirm response (embedded mode) or left
  // undefined (standalone mode, which fetches instead -- see onMount
  // below). None of these props ever change after this component mounts,
  // so capturing their initial value into local $state (rather than a
  // $derived that would re-run on every parent render) is the intended
  // behavior, not an oversight.
  const embedded = initialToken !== undefined;

  type LoadState = 'loading' | 'loaded' | 'invalid' | 'error';

  let loadState = $state<LoadState>(embedded ? 'loaded' : 'loading');
  // svelte-ignore state_referenced_locally -- see the note above `embedded`.
  let token = $state(initialToken ?? '');
  // svelte-ignore state_referenced_locally -- see the note above `embedded`.
  let email = $state(initialEmail ?? '');
  // Embedded mode always follows a successful POST /api/subscribe/confirm,
  // which only returns 200 after subscribers.Store.Confirm has transitioned
  // the row to active (it 400s on an expired token and refuses a complained
  // one via ErrComplainedLocked -- see that method's doc comment) -- so
  // 'active' here is a fact about how this component got embedded, not a
  // guess. Standalone mode starts '' and is set from GET's real status on
  // load (see onMount below); '' fails the `status === 'active'` check
  // everywhere it's used, matching loadState 'loading' rendering nothing yet.
  // svelte-ignore state_referenced_locally -- see the note above `embedded`.
  let status = $state(embedded ? 'active' : '');
  // svelte-ignore state_referenced_locally -- see the note above `embedded`.
  let selected = $state<Set<string>>(new Set(initialInterests ?? []));
  // svelte-ignore state_referenced_locally -- see the note above `embedded`.
  let activeInterests = $state<PublicInterest[]>(initialActiveInterests ?? []);

  // #0031 review finding 1: the editor + Save are only meaningful for an
  // active subscriber -- editing interests for a pending/unsubscribed/
  // bounced/complained row can't be honestly reported as "you're receiving
  // X emails" because they aren't receiving any. See the non-active Panel
  // branch below, which replaces the editor with a status explanation and a
  // resubscribe path instead of a Save button that can't do anything.
  let isActive = $derived(status === 'active');

  let saving = $state(false);
  let saveMessage = $state<string | null>(null);
  let saveError = $state<string | null>(null);

  let unsubscribing = $state(false);
  let unsubscribed = $state(false);
  let unsubscribeError = $state<string | null>(null);
  // #0031 review finding 2: Store.Unsubscribe silently no-ops on an
  // already-complained row. This is distinct from `unsubscribed` (a real
  // state change) -- shown as its own terminal panel rather than folded
  // into `unsubscribed` or lost when `status` flips away from 'active' and
  // the editor branch that held `unsubscribeError` unmounts.
  let unsubscribeNoOp = $state(false);
  let unsubscribeNoOpMessage = $state('');

  onMount(() => {
    if (embedded) return;

    const t = $currentRoute.query.get('token');
    if (!t) {
      loadState = 'invalid';
      return;
    }
    token = t;

    getPreferences(t)
      .then((res) => {
        email = res.email;
        status = res.status;
        selected = new Set(res.interests);
        activeInterests = res.active_interests;
        loadState = 'loaded';
      })
      .catch((err) => {
        if (err instanceof ApiError && err.status === 404) {
          loadState = 'invalid';
        } else {
          loadState = 'error';
        }
      });
  });

  function toggleInterest(slug: string): void {
    selected = toggleSlug(selected, slug);
  }

  async function onSave(): Promise<void> {
    if (saving) return;
    saving = true;
    saveMessage = null;
    saveError = null;
    try {
      const res = await patchPreferences(buildSaveInterestsPatch(token, selected));
      selected = new Set(res.interests);
      status = res.status;
      saveMessage = res.message ?? 'Preferences saved.';
    } catch (err) {
      saveError = err instanceof ApiError ? err.message : 'Could not reach the server. Please try again.';
    } finally {
      saving = false;
    }
  }

  async function onUnsubscribeEverything(): Promise<void> {
    if (unsubscribing) return;
    unsubscribing = true;
    unsubscribeError = null;
    try {
      const res = await patchPreferences(buildUnsubscribeEverythingPatch(token));
      status = res.status;
      if (res.unsubscribed) {
        unsubscribed = true;
        saveMessage = null;
      } else {
        // #0031 review finding 2: the store no-op'd (already complained) --
        // report that honestly instead of the "you've been unsubscribed"
        // success panel, which would be a false statement.
        unsubscribeNoOp = true;
        unsubscribeNoOpMessage = res.message ?? "This address couldn't be unsubscribed.";
      }
    } catch (err) {
      unsubscribeError =
        err instanceof ApiError ? err.message : 'Could not reach the server. Please try again.';
    } finally {
      unsubscribing = false;
    }
  }

  function goToSubscribe(): void {
    navigate('/subscribe');
  }
</script>

{#if loadState === 'loading'}
  <p class="text-muted">Loading your preferences…</p>
{:else if loadState === 'invalid'}
  <div class="pref-invalid">
    {#if showHeading}<h1>Manage your preferences</h1>{/if}
    <Panel>
      <p>This link is invalid or has expired.</p>
      <p class="text-muted">
        If you'd like to receive updates, you can
        <button type="button" class="link-button" onclick={goToSubscribe}>subscribe again</button>.
      </p>
    </Panel>
  </div>
{:else if loadState === 'error'}
  <p class="text-error" role="alert">
    Something went wrong loading your preferences. Please try again in a moment.
  </p>
{:else if unsubscribed}
  <div class="pref-content">
    {#if showHeading}<h1>Manage your preferences</h1>{/if}
    <Panel>
      <p role="status">You've been unsubscribed from everything. You can resubscribe anytime.</p>
      <button type="button" class="link-button" onclick={goToSubscribe}>Subscribe again</button>
    </Panel>
  </div>
{:else if unsubscribeNoOp}
  <div class="pref-content">
    {#if showHeading}<h1>Manage your preferences</h1>{/if}
    <Panel>
      <p role="status">{unsubscribeNoOpMessage}</p>
    </Panel>
  </div>
{:else if !isActive}
  <!-- #0031 review finding 1: not currently active (pending, unsubscribed,
       bounced, or complained) -- the interest editor and Save button can't
       honestly do anything useful here, so offer the resubscribe path
       instead of a Save button that can't do anything.

       #0090: the resubscribe path is a dead end for `complained` --
       goToSubscribe performs no mutation (the guard stays correctly in
       place) and the public form's uniform 202 (#0026) gives no sign that
       existingSignup's StatusComplained branch never sends a confirmation
       email. showSubscribeAgainAffordance suppresses the button for that
       one status; the other three (pending, unsubscribed, bounced) can
       genuinely leave their status this way and keep it.

       Bounced 2026-08-19: suppressing the button correctly leaves
       `complained` with no in-app path at all, and COMPLAINED_NO_RESUBSCRIBE_
       MESSAGE's "Contact us" names no address -- an inert dead end shaped
       exactly like the one this issue exists to close. Deliberately NOT
       fixed by baking the address into that constant (it's a word-for-word
       mirror of preferences.go's no-op message and must stay one); instead
       the mailto link renders here, beside the message, in the same slot
       the button used to fill. -->
  <div class="pref-content">
    {#if showHeading}<h1>Manage your preferences</h1>{/if}
    <Panel>
      <p role="status">{inactiveStatusMessage(status)}</p>
      {#if showSubscribeAgainAffordance(status)}
        <button type="button" class="link-button" onclick={goToSubscribe}>Subscribe again</button>
      {:else}
        <p class="text-muted">
          Reach us at <a href="mailto:{COMPLAINED_CONTACT_EMAIL}">{COMPLAINED_CONTACT_EMAIL}</a>.
        </p>
      {/if}
    </Panel>
  </div>
{:else}
  <div class="pref-content">
    {#if showHeading}<h1>Manage your preferences</h1>{/if}
    <p class="text-muted">Signed in as <strong>{email}</strong></p>

    <section aria-labelledby="pref-interests-h">
      <h2 id="pref-interests-h">Interests</h2>
      <fieldset class="interests-fieldset">
        <legend>Topics</legend>
        {#if activeInterests.length > 0}
          <div class="checkbox-group">
            {#each activeInterests as it (it.slug)}
              <label class="checkbox-label">
                <input
                  type="checkbox"
                  checked={selected.has(it.slug)}
                  disabled={saving}
                  onchange={() => toggleInterest(it.slug)}
                />
                {it.name}
              </label>
            {/each}
          </div>
        {:else}
          <p class="text-muted">No topics are available right now.</p>
        {/if}
      </fieldset>
      <p class="text-muted pref-empty-note">
        If you don't select any topics, you'll still receive general announcements only.
      </p>

      {#if saveError}
        <p class="text-error" aria-live="polite">{saveError}</p>
      {/if}
      {#if saveMessage}
        <p class="text-notice" role="status">{saveMessage}</p>
      {/if}
      <Button type="button" variant="primary" disabled={saving} onclick={onSave}>
        {saving ? 'Saving…' : 'Save preferences'}
      </Button>
    </section>

    <section aria-labelledby="pref-leave-h" class="pref-leave">
      <h2 id="pref-leave-h">Leave the list</h2>
      <p class="text-muted">
        This stops every email from us, including general announcements — different from
        unchecking topics above, which keeps you on the list for general announcements only.
      </p>
      {#if unsubscribeError}
        <p class="text-error" aria-live="polite">{unsubscribeError}</p>
      {/if}
      <Button type="button" variant="danger" disabled={unsubscribing} onclick={onUnsubscribeEverything}>
        {unsubscribing ? 'Unsubscribing…' : 'Unsubscribe from everything'}
      </Button>
    </section>
  </div>
{/if}

<style>
  .pref-content > h1,
  .pref-invalid > h1 {
    margin: 0 0 var(--space-3);
  }

  .pref-content > p {
    margin: 0 0 var(--space-4);
  }

  .pref-content section {
    margin-bottom: var(--space-5);
  }
  .pref-content section h2 {
    font-size: var(--fs-lg);
    margin: 0 0 var(--space-3);
  }

  .interests-fieldset {
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
    padding: var(--space-3);
    margin: 0;
  }
  .interests-fieldset legend {
    font-size: var(--fs-sm);
    font-weight: 600;
    color: var(--text-muted);
    padding: 0 var(--space-1);
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

  .pref-empty-note {
    margin: var(--space-2) 0 var(--space-3);
  }

  .pref-leave {
    border-top: var(--border-w) solid var(--border);
    padding-top: var(--space-4);
  }

  .link-button {
    background: none;
    border: none;
    padding: 0;
    color: var(--accent);
    text-decoration: underline;
    cursor: pointer;
    font: inherit;
  }
</style>
