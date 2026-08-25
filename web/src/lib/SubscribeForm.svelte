<script lang="ts">
  // SubscribeForm — the double opt-in signup form (#0029, PRD §6.3/§7.3):
  // email + interest checkbox grid + the invisible anti-abuse fields, used
  // both inline on Home.svelte and standalone at /subscribe (Subscribe.svelte).
  //
  // Interests are fetched from GET /api/interests, which is backed by
  // interests.Store.ListActive -- NEVER the admin ListAll route. Building
  // this grid from ListAll would show a checkbox for a deactivated interest
  // that #0026's endpoint then rejects with a 400 -- exactly the defect
  // #0024's review carried into this issue.
  //
  // Anti-abuse: a hidden `website` honeypot field (real visitors never see
  // or fill it; a filled value is silently accepted by the server behind
  // the same uniform 202 as a real success) and `rendered_at`, the
  // Date.now() timestamp captured at mount, for the server's >=2s timing
  // gate.
  //
  // Response invariance: on any 2xx response this component ALWAYS
  // navigates to the fixed /subscribe/thanks route without ever reading the
  // response body (see lib/subscribe.ts's onSubscribeSuccess) -- #0026's
  // uniform 202 is a security property (CLAUDE.md §9), and inspecting the
  // body here would be the one place this form could reintroduce the
  // distinction the server just eliminated. There is also no client-side
  // branching on elapsed time: the try/catch below does exactly one await,
  // so nothing here can leak branch state through timing either.
  //
  // Email validation is deliberately minimal client-side (non-empty only) --
  // #0029's notes: "the form must not present [a 400] as the user's typo
  // without care, and must not be stricter than the server." RFC 6531
  // (SMTPUTF8) non-ASCII addresses are valid per #0026; a stricter HTML5
  // pattern could reject one of those in some browsers before the server
  // ever sees it, so novalidate is set and format checking is left entirely
  // to the server's response.
  import { onMount } from 'svelte';
  import { navigate } from '../lib/router';
  import { listPublicInterests, subscribe, ApiError } from './api';
  import {
    buildSubscribeRequest,
    onSubscribeSuccess,
    captureUtmParams,
    loadUtmParams,
    toggleSlug,
    type UtmParams,
  } from './subscribe';
  import type { PublicInterest } from './types';
  import Button from './Button.svelte';

  interface Props {
    /** Slugs to pre-check on mount (#0029: "used by workshop pages in
     * #0054") -- applied once the active interest list has loaded, so a
     * slug that has since been deactivated is simply not offered rather
     * than silently checked-but-invisible. */
    preselect?: string[];
  }

  let { preselect = [] }: Props = $props();

  let email = $state('');
  let website = $state(''); // honeypot -- must stay empty for a real visitor
  // svelte-ignore state_referenced_locally -- preselect seeds the initial
  // selection exactly once, by design (#0029: "preselect prop pre-checks
  // interests"). It is never meant to re-sync if the caller's prop value
  // changes after mount -- these components are never remounted with a
  // different preselect, and a $derived here would silently blow away a
  // visitor's in-progress checkbox edits.
  let selected = $state<Set<string>>(new Set(preselect));
  let activeInterests = $state<PublicInterest[]>([]);
  let interestsLoadError = $state(false);
  let renderedAt = $state(0);
  let utm = $state<UtmParams>({ utm_source: '', utm_medium: '', utm_campaign: '' });

  let submitting = $state(false);
  let errorMessage = $state<string | null>(null);

  const errorId = 'subscribe-form-error';

  onMount(() => {
    renderedAt = Date.now();

    try {
      captureUtmParams(window.location.search, window.sessionStorage);
      utm = loadUtmParams(window.sessionStorage);
    } catch {
      // sessionStorage unavailable (private browsing, storage disabled) --
      // utm stays at its all-empty default. Not fatal to the form.
    }

    listPublicInterests()
      .then((res) => {
        activeInterests = res.interests;
      })
      .catch(() => {
        interestsLoadError = true;
      });
  });

  function toggleInterest(slug: string): void {
    selected = toggleSlug(selected, slug);
  }

  async function onSubmit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    if (submitting) return;

    if (email.trim() === '') {
      errorMessage = 'Enter your email address.';
      return;
    }

    submitting = true;
    errorMessage = null;

    const body = buildSubscribeRequest({
      email,
      interests: Array.from(selected),
      website,
      renderedAt,
      utm,
    });

    try {
      const res = await subscribe(body);
      navigate(onSubscribeSuccess(res));
    } catch (err) {
      if (err instanceof ApiError) {
        errorMessage = err.message || 'Something went wrong. Please try again.';
      } else {
        errorMessage = 'Could not reach the server. Check your connection and try again.';
      }
    } finally {
      submitting = false;
    }
  }
</script>

<form class="subscribe-form" novalidate onsubmit={onSubmit}>
  <div class="field">
    <label for="subscribe-email">Email</label>
    <input
      id="subscribe-email"
      type="email"
      autocomplete="email"
      bind:value={email}
      disabled={submitting}
      aria-describedby={errorMessage ? errorId : undefined}
      aria-invalid={errorMessage ? 'true' : undefined}
      placeholder="you@example.com"
    />
  </div>

  <fieldset class="interests-fieldset">
    <legend>Topics (optional)</legend>
    {#if activeInterests.length > 0}
      <div class="checkbox-group">
        {#each activeInterests as it (it.slug)}
          <label class="checkbox-label">
            <input
              type="checkbox"
              checked={selected.has(it.slug)}
              disabled={submitting}
              onchange={() => toggleInterest(it.slug)}
            />
            {it.name}
          </label>
        {/each}
      </div>
    {:else if interestsLoadError}
      <p class="text-muted">
        Topics couldn't be loaded right now — you can still subscribe for general updates.
      </p>
    {:else}
      <p class="text-muted">Loading topics…</p>
    {/if}
  </fieldset>

  <!-- Honeypot: invisible to sighted users AND assistive technology. A real
       visitor never focuses or fills this; a bot that fills every input
       does. aria-hidden + tabindex="-1" on the input itself (not just the
       wrapper) so a screen reader user is never asked to fill it, even if
       something upstream ignores the wrapper's aria-hidden. -->
  <div class="honeypot" aria-hidden="true">
    <label for="subscribe-website" tabindex="-1">Leave this field blank</label>
    <input
      id="subscribe-website"
      name="website"
      type="text"
      tabindex="-1"
      autocomplete="off"
      bind:value={website}
    />
  </div>

  <!-- #0063: a single persistent element, not an {#if}/{:else} swap between
       two <p id={errorId}> tags. Proved live (real Safari, not jsdom) that
       the previous two-branch version LOOKED like the safe "present from
       first render" pattern but was not: Svelte destroys and recreates the
       element crossing an {#if}/{:else} boundary even when both branches
       render the same tag and id, so the "error" and "empty" paragraphs were
       two different DOM nodes and every error announcement was, in fact, a
       freshly-inserted live region -- exactly the class of bug this issue's
       carried-in finding is about. Toggling only the class and text content
       of ONE node keeps it the same node across the transition. -->
  <p id={errorId} class={errorMessage ? 'text-error' : 'sr-only'} aria-live="polite">{errorMessage ?? ''}</p>

  <div class="row subscribe-actions">
    <Button type="submit" variant="primary" disabled={submitting}>
      {submitting ? 'Subscribing…' : 'Subscribe'}
    </Button>
    <a href="/privacy">Privacy policy</a>
  </div>
</form>

<style>
  .subscribe-form {
    display: flex;
    flex-direction: column;
    gap: var(--space-4);
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

  .honeypot {
    display: none;
  }

  .subscribe-actions {
    flex-wrap: wrap;
    gap: var(--space-3);
  }

  .subscribe-actions a {
    color: var(--text-faint);
    font-size: var(--fs-sm);
  }
  .subscribe-actions a:hover {
    color: var(--text-muted);
    text-decoration: underline;
  }
</style>
