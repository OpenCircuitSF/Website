<script lang="ts">
  import { onMount } from 'svelte';
  import { get } from 'svelte/store';
  import { currentUser, pendingVerifyToken } from './lib/stores';
  import { currentRoute, initRouter, navigate } from './lib/router';
  import { getMe } from './lib/api';
  import Login from './views/Login.svelte';
  import Account from './views/Account.svelte';
  import Admin from './views/Admin.svelte';
  import RegisterVerify from './views/RegisterVerify.svelte';
  import RecoverVerify from './views/RecoverVerify.svelte';
  import Header from './lib/Header.svelte';
  import Footer from './lib/Footer.svelte';
  import Home from './views/Home.svelte';
  import About from './views/About.svelte';
  import PrivacyPolicy from './views/PrivacyPolicy.svelte';
  import Subscribe from './views/Subscribe.svelte';
  import SubscribeThanks from './views/SubscribeThanks.svelte';
  import NotFound from './views/NotFound.svelte';

  let sessionChecked = $state(false);

  // On mount, the History API router (#0014) has already parsed the current
  // URL into $currentRoute (router.ts's module-level default). Two things
  // still need to happen before the first real render:
  //
  //   1. Wire up click interception and popstate handling (initRouter).
  //   2. Resolve session state: GET /api/me decides currentUser, which gates
  //      the Admin nav link and the account/admin views.
  //
  // Magic-link landings (/register/verify, /recover/verify) are the one
  // exception to step 2 -- a user following an email link has no session yet,
  // so calling /api/me first would just fail and add nothing. Instead we pull
  // ?token= off the already-parsed route, stash it in pendingVerifyToken, and
  // strip it from the URL with a replace-navigation (a one-time token left
  // sitting in the address bar/history is unnecessary exposure). This is the
  // same behavior the old App.svelte implemented directly against
  // window.location before #0014 existed -- ported here so it survives the
  // rewrite intact.
  onMount(() => {
    initRouter();

    const route = get(currentRoute);

    if (route.name === 'register-verify' || route.name === 'recover-verify') {
      pendingVerifyToken.set(route.query.get('token'));
      navigate(route.path, { replace: true });
      sessionChecked = true;
      return;
    }

    getMe()
      .then((user) => currentUser.set(user))
      .catch(() => currentUser.set(null))
      .finally(() => {
        sessionChecked = true;
      });
  });
</script>

{#if !sessionChecked}
  <div class="app-shell">
    <p class="text-muted">Loading…</p>
  </div>
{:else if $currentRoute.name === 'login'}
  <Login />
{:else if $currentRoute.name === 'account'}
  <Account />
{:else if $currentRoute.name === 'admin'}
  <Admin />
{:else if $currentRoute.name === 'register-verify'}
  <RegisterVerify />
{:else if $currentRoute.name === 'recover-verify'}
  <RecoverVerify />
{:else}
  <!-- Every other route is a public marketing page (PRD §5.1), and shares
       the site header shell (#0017). This placeholder covers the remaining
       public routes (workshops, confirm, preferences, unsubscribe) until
       their own later-phase issues (#0030-0031, #0036, #0053-0054) build
       them; not-found is a real 404, not a placeholder. -->
  <Header />
  {#if $currentRoute.name === 'home'}
    <Home />
  {:else if $currentRoute.name === 'about'}
    <About />
  {:else if $currentRoute.name === 'privacy'}
    <PrivacyPolicy />
  {:else if $currentRoute.name === 'subscribe'}
    <Subscribe />
  {:else if $currentRoute.name === 'subscribe-thanks'}
    <SubscribeThanks />
  {:else if $currentRoute.name === 'not-found'}
    <NotFound />
  {:else}
    <main id="main-content" class="app-shell">
      <p class="text-muted">{$currentRoute.name} — this page is on the way.</p>
    </main>
  {/if}
{/if}

<Footer />
