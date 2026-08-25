<script lang="ts">
  // ConfirmSubscription — /confirm?token=... (#0030, PRD §6.3).
  //
  // The GET that loads this page has NO side effect: the browser navigation
  // to /confirm?token=... is served by the SPA's catch-all handler (plain
  // static HTML, no Go logic runs -- see confirm.go's package doc comment),
  // so a mail client or corporate security proxy prefetching the link never
  // triggers a confirmation. Only once this component has actually mounted
  // and run does it fire the real mutation, and that mutation is a POST
  // (internal/handlers/confirm.go's Confirm), never a GET -- prefetchers by
  // definition do not execute the JavaScript that would issue it.
  //
  // On success this rolls straight into the preference center inline
  // (embedded PreferenceCenter, showHeading=false so this view's own <h1>
  // stays the page's only one) using the manage_token, email, and interest
  // state the confirm response already carries -- no second confirmation
  // email needed.
  //
  // On failure: the store clears confirm_token on a successful confirm
  // (single-use by design), so a replay of an already-used token is
  // indistinguishable from one that never existed or expired -- the server
  // answers the same 400 for all three. The copy below is worded to cover
  // all three gracefully rather than asserting any one of them.
  import { onMount, tick } from 'svelte';
  import { currentRoute, navigate } from '../lib/router';
  import { confirmSubscription, ApiError } from '../lib/api';
  import type { ConfirmResponse } from '../lib/types';
  import Panel from '../lib/Panel.svelte';
  import PreferenceCenter from './PreferenceCenter.svelte';

  type Status = 'confirming' | 'success' | 'error';

  let status = $state<Status>('confirming');
  let result = $state<ConfirmResponse | null>(null);

  // #0063: confirming -> success/error happens automatically, seconds after
  // mount, with no user interaction to prime a screen reader for a change --
  // unlike Unsubscribe.svelte's/PreferenceCenter's analogous panel swaps,
  // which follow a button click. A visitor who lands here, has their screen
  // reader read "Confirming... One moment.", and is still on that text when
  // the fetch resolves needs to be told the page changed. Same fix as those
  // two: move focus to the new state's own <h1> (tabindex="-1") once it
  // exists, rather than leaving focus wherever it was (nowhere, on initial
  // load -- so this only bites a user who has started exploring by the time
  // the response lands, but it does bite them).
  let resultHeading = $state<HTMLHeadingElement | null>(null);

  onMount(() => {
    const token = $currentRoute.query.get('token');
    if (!token) {
      status = 'error';
      return;
    }

    confirmSubscription(token)
      .then(async (res) => {
        result = res;
        status = 'success';
        await tick();
        resultHeading?.focus();
      })
      .catch(async () => {
        // Deliberately does not distinguish ApiError branches: unknown,
        // expired, replayed, and complained-locked all render the same
        // friendly copy here, matching the server's own uniform 400 for
        // the first three (it cannot tell them apart) and treating the
        // fourth the same way rather than confirming a stranger's guess
        // about someone else's subscription state.
        status = 'error';
        await tick();
        resultHeading?.focus();
      });
  });

  function goToSubscribe(): void {
    navigate('/subscribe');
  }
</script>

<main id="main-content" class="app-shell confirm-shell">
  {#if status === 'confirming'}
    <h1>Confirming…</h1>
    <p class="text-muted">One moment.</p>
  {:else if status === 'error'}
    <h1 tabindex="-1" bind:this={resultHeading}>This link didn't work</h1>
    <Panel>
      <p>
        This confirmation link is invalid, has expired, or was already used. If you're already
        subscribed, there's nothing more to do — otherwise you can try again.
      </p>
      <button type="button" class="link-button" onclick={goToSubscribe}>Subscribe again</button>
    </Panel>
  {:else if status === 'success' && result}
    <h1 tabindex="-1" bind:this={resultHeading}>You're confirmed</h1>
    <p class="text-muted confirm-lede">
      Thanks — you're on the list. Pick your topics below, or leave the defaults.
    </p>
    <PreferenceCenter
      showHeading={false}
      initialToken={result.manage_token}
      initialEmail={result.email}
      initialInterests={result.interests}
      initialActiveInterests={result.active_interests}
    />
  {/if}
</main>

<style>
  .confirm-shell {
    max-width: 560px;
  }
  .confirm-shell h1 {
    margin: 0 0 var(--space-3);
  }
  /* #0063: matches Unsubscribe.svelte's .headline:focus -- resultHeading
     receives programmatic focus() once the confirming->success/error
     transition lands (see the script's doc comment). */
  .confirm-shell h1:focus {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
  .confirm-lede {
    margin: 0 0 var(--space-4);
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
