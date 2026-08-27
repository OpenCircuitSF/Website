<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { get } from 'svelte/store';
  import { currentUser, pendingVerifyToken } from './lib/stores';
  import { currentRoute, initRouter, navigate } from './lib/router';
  import { getMe } from './lib/api';
  import { titleForRoute, shouldAnnounceNavigation } from './lib/pageTitle';
  import Login from './views/Login.svelte';
  import Account from './views/Account.svelte';
  import Admin from './views/Admin.svelte';
  import RegisterVerify from './views/RegisterVerify.svelte';
  import RecoverVerify from './views/RecoverVerify.svelte';
  import Header from './lib/Header.svelte';
  import Footer from './lib/Footer.svelte';
  import Home from './views/Home.svelte';
  import WorkshopsIndex from './views/WorkshopsIndex.svelte';
  import WorkshopDetail from './views/WorkshopDetail.svelte';
  import About from './views/About.svelte';
  import PrivacyPolicy from './views/PrivacyPolicy.svelte';
  import Subscribe from './views/Subscribe.svelte';
  import SubscribeThanks from './views/SubscribeThanks.svelte';
  import ConfirmSubscription from './views/ConfirmSubscription.svelte';
  import PreferenceCenter from './views/PreferenceCenter.svelte';
  import Unsubscribe from './views/Unsubscribe.svelte';
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

  // #0238: client-side navigation used to change no more than the DOM --
  // document.title stayed whatever the page loaded with, and focus stayed
  // on <body> (or wherever a previous view left it), so a screen-reader
  // user was told nothing and a keyboard user's next Tab restarted from the
  // top of the document. This effect re-runs on every $currentRoute change
  // -- a link click, a programmatic navigate(), AND a popstate all funnel
  // through router.ts's currentRoute store the same way, so one effect
  // covers all three without special-casing any of them.
  //
  // shouldAnnounceNavigation() (pageTitle.ts) is what keeps the FIRST
  // render -- the route the document actually loaded on -- untouched: that
  // load already got the browser's own navigation announcement, and for a
  // route internal/seo/seo.go has a real entry for, overwriting its
  // server-rendered <title> here would be a regression, not a fix (see
  // pageTitle.ts's module doc comment). Reading `sessionChecked` first
  // means this effect's first REAL invocation happens once the route is
  // actually about to render a view (not the `{#if !sessionChecked}`
  // loading placeholder above), which is also when shouldAnnounceNavigation
  // correctly spends its one-time flag for the register-verify/
  // recover-verify redirect case: their replace-navigate happens before
  // sessionChecked flips true, so it never reaches this effect at all, and
  // the redirected-to route is what actually spends the flag.
  $effect(() => {
    const route = $currentRoute;
    if (!sessionChecked) return;
    if (!shouldAnnounceNavigation()) return;

    document.title = titleForRoute(route);

    // Move focus to the new view's own <h1> (every public/auth view's <h1>
    // now carries tabindex="-1" -- see app.css's h1[tabindex='-1']:focus
    // rule) once Svelte has actually rendered it. This generalizes the
    // whole-panel-swap focus pattern Unsubscribe.svelte and
    // ConfirmSubscription.svelte already used for their OWN internal state
    // transitions to every route change -- the two are independent and
    // compose cleanly: this effect fires on ROUTE change, theirs fire on a
    // later async/user-driven transition within an already-mounted view.
    //
    // A route whose <h1> doesn't exist yet at this instant (WorkshopDetail
    // while its fetch is still in flight -- see pageTitle.ts's "fallback"
    // entries) simply gets no focus move for this navigation; a disclosed
    // gap, not a silent one -- see issues/0238.md's `## Verification`.
    // #0238 review (2afea58): heading?.focus() with the default
    // preventScroll: false scrolled the heading into view AFTER
    // restoreScroll() had already put the page back where the user was on a
    // Back navigation -- and the throttled scroll listener then stamped
    // that bogus offset into history.state.scrollY, destroying the saved
    // position rather than merely overriding it for one paint (measured in
    // real Safari: window.scrollY/history.state.scrollY went from 1200/1200
    // to 0/0 after Back). preventScroll: true keeps the focus move purely
    // an accessibility signal -- it does not touch scroll position -- so it
    // composes with router.ts's restoreScroll instead of racing it.
    void tick().then(() => {
      const heading = document.querySelector('h1[tabindex="-1"]') as HTMLElement | null;
      heading?.focus({ preventScroll: true });
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
       the site header shell (#0017). not-found is a real 404, not a
       placeholder. -->
  <Header />
  {#if $currentRoute.name === 'home'}
    <Home />
  {:else if $currentRoute.name === 'workshops'}
    <WorkshopsIndex />
  {:else if $currentRoute.name === 'workshop-detail'}
    <WorkshopDetail />
  {:else if $currentRoute.name === 'about'}
    <About />
  {:else if $currentRoute.name === 'privacy'}
    <PrivacyPolicy />
  {:else if $currentRoute.name === 'subscribe'}
    <Subscribe />
  {:else if $currentRoute.name === 'subscribe-thanks'}
    <SubscribeThanks />
  {:else if $currentRoute.name === 'confirm'}
    <ConfirmSubscription />
  {:else if $currentRoute.name === 'preferences'}
    <main id="main-content" class="app-shell">
      <PreferenceCenter />
    </main>
  {:else if $currentRoute.name === 'unsubscribe'}
    <Unsubscribe />
  {:else if $currentRoute.name === 'not-found'}
    <NotFound />
  {:else}
    <main id="main-content" class="app-shell">
      <p class="text-muted">{$currentRoute.name} — this page is on the way.</p>
    </main>
  {/if}
{/if}

<Footer />
