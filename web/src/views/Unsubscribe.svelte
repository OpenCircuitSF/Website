<script lang="ts">
  // Unsubscribe — /unsubscribe?token=... (#0036, PRD §6.5 path 1).
  //
  // The GET that lands here has no side effect of its own:
  // internal/handlers/unsubscribe.go's Get 302s a mail-client's prefetch of
  // the List-Unsubscribe link straight to this route without ever touching
  // the store (see that handler's package doc comment), so this component
  // only fires the real mutation once a person has actually clicked the
  // button below — a POST, never triggered by a prefetcher's GET. Same
  // GET-is-safe / POST-is-real split as ConfirmSubscription.svelte.
  //
  // There is no error state to render. #0034's POST /api/unsubscribe
  // answers 200 for every input — a fresh valid token, an unknown/expired/
  // replayed one, a missing token, and an already-complained row all reach
  // the exact same done branch below, worded from whatever `message` the
  // server returned (see lib/unsubscribe.ts's resolveDoneMessage, which has
  // no "invalid token" case on purpose). A request that never reaches the
  // server at all (offline) folds onto that same done state with a fixed
  // fallback message rather than a distinct error UI.
  //
  // No dark patterns (CLAUDE.md §9, PRD §6.5): the single button below IS
  // the unsubscribe action — there is no muted "actually, keep me
  // subscribed" link competing with it. The preference center is offered
  // once, plainly, in the done state, as the lighter alternative for next
  // time.
  //
  // The done state's preference-center link deliberately does NOT carry the
  // token forward: a real (non-no-op) unsubscribe rotates manage_token
  // server-side (internal/subscribers.RotateManageToken), so the token this
  // view arrived with is already stale by the time the done state renders.
  // A bare /preferences link is the one shape that is never wrong.
  import { tick } from 'svelte';
  import { currentRoute, navigate } from '../lib/router';
  import { unsubscribeOneClick } from '../lib/api';
  import { resolveDoneMessage, extractToken, type UnsubscribeResult } from '../lib/unsubscribe';
  import TerminalPanel from '../lib/TerminalPanel.svelte';
  import Prompt from '../lib/Prompt.svelte';
  import Panel from '../lib/Panel.svelte';
  import Button from '../lib/Button.svelte';
  import { APP_NAME } from '../lib/branding';

  type Status = 'confirm' | 'submitting' | 'done';

  let status = $state<Status>('confirm');
  let result = $state<UnsubscribeResult | null>(null);
  let doneHeading = $state<HTMLHeadingElement | null>(null);

  let token = $derived(extractToken($currentRoute.query));
  let doneMessage = $derived(resolveDoneMessage(result));

  async function onConfirm(): Promise<void> {
    if (status !== 'confirm') return;
    status = 'submitting';
    try {
      result = await unsubscribeOneClick(token);
    } catch {
      // The server never answers non-2xx for this route (see the component
      // doc comment) -- reaching this branch means the request itself never
      // got a response (offline, DNS, a dropped connection). Leaving
      // `result` null lets resolveDoneMessage fall back to its fixed
      // generic message rather than surfacing a distinct error UI.
      result = null;
    }
    status = 'done';
    // Move focus to the done-state heading so a screen-reader user is told
    // the page changed, rather than leaving focus on a button that just
    // disappeared. tick() first so the heading actually exists to focus.
    await tick();
    doneHeading?.focus();
  }

  function goToResubscribe(): void {
    navigate('/subscribe');
  }

  function goToPreferences(): void {
    // Deliberately no token -- see the component doc comment.
    navigate('/preferences');
  }
</script>

<main id="main-content" class="app-shell unsub-shell">
  <TerminalPanel title="unsubscribe // open_circuit_sf">
    <Prompt text="unsubscribe" />
    {#if status !== 'done'}
      <h1 class="headline">Leave the {APP_NAME} mailing list?</h1>
      <p>
        This stops every email from us, including general announcements. If you'd
        rather keep some topics and drop others, the preference center lets you
        manage that instead of leaving entirely.
      </p>
      <div class="actions">
        <Button
          type="button"
          variant="primary"
          disabled={status === 'submitting'}
          onclick={onConfirm}
        >
          {status === 'submitting' ? 'Unsubscribing…' : 'Unsubscribe'}
        </Button>
      </div>
    {:else}
      <h1 class="headline" tabindex="-1" bind:this={doneHeading}>You're done</h1>
      <Panel>
        <p role="status">{doneMessage}</p>
      </Panel>
      <p class="text-muted done-note">
        Changed your mind? You can
        <button type="button" class="link-button" onclick={goToResubscribe}>subscribe again</button>,
        or manage individual topics instead in the
        <button type="button" class="link-button" onclick={goToPreferences}>preference center</button>.
      </p>
    {/if}
  </TerminalPanel>
</main>

<style>
  .unsub-shell {
    max-width: 560px;
  }

  .headline {
    margin: var(--space-3) 0 var(--space-4);
    font-size: var(--fs-xl);
    font-weight: 700;
  }
  .headline:focus {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }

  .unsub-shell p {
    max-width: var(--measure);
    margin: 0 0 var(--space-4);
  }

  .actions {
    margin-top: var(--space-4);
  }

  .done-note {
    margin-top: var(--space-4);
    margin-bottom: 0;
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
