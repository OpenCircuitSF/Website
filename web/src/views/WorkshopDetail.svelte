<script lang="ts">
  // WorkshopDetail — /workshops/{slug} (#0054, PRD §7.3). The route a social
  // link points at for one specific session, so its meta tags matter most of
  // any page on the site (this issue's Notes) -- but that half is entirely
  // server-side: internal/seo's Renderer/Sitemap (workshop_seo_source.go,
  // #0054's Go half) already inject per-workshop <title>/OG tags and the
  // sitemap entry BEFORE this component's JS ever runs, since social
  // crawlers never execute it. This component only has to render the page a
  // human sees after that.
  //
  // GET /api/workshops/{slug} (#0051) serves published AND canceled
  // workshops -- draft and unknown slugs both 404 with a byte-identical
  // body, so this view cannot and does not try to distinguish "exists but
  // unpublished" from "never existed"; both render the same not-found
  // state. Reused rather than duplicated: lib/workshops.ts's date/location
  // label/canceled-badge helpers (#0053) and lib/workshopDetail.ts's
  // location-block/preselect/external-signup decisions (#0054).
  //
  // Route params are read from $currentRoute, not captured once at mount:
  // App.svelte never destroys this component when navigating between two
  // workshop-detail URLs (both match the same 'workshop-detail' branch), so
  // an $effect keyed on the slug re-fetches whenever it changes, the same
  // way a plain page load would.
  import { get } from 'svelte/store';
  import { currentRoute, navigate } from '../lib/router';
  import { getPublicWorkshop, ApiError } from '../lib/api';
  import { formatWorkshopDate, workshopLocationLabel, isCanceled } from '../lib/workshops';
  import {
    workshopLocationLines,
    hasAnyLocationInfo,
    workshopPreselectSlugs,
    hasExternalSignup,
    COVER_IMAGE_WIDTH,
    COVER_IMAGE_HEIGHT,
  } from '../lib/workshopDetail';
  import type { PublicWorkshop } from '../lib/types';
  import TerminalPanel from '../lib/TerminalPanel.svelte';
  import Prompt from '../lib/Prompt.svelte';
  import Panel from '../lib/Panel.svelte';
  import TraceDivider from '../lib/TraceDivider.svelte';
  import SubscribeForm from '../lib/SubscribeForm.svelte';

  type Status = 'loading' | 'loaded' | 'not-found' | 'error';

  let status = $state<Status>('loading');
  let workshop = $state<PublicWorkshop | null>(null);

  $effect(() => {
    const slug = $currentRoute.params.slug;
    if (!slug) return;

    status = 'loading';
    workshop = null;

    getPublicWorkshop(slug)
      .then((w) => {
        // The user may have navigated to a DIFFERENT workshop while this
        // request was in flight -- discard a stale response rather than
        // clobbering the newer one's state.
        if (get(currentRoute).params.slug !== slug) return;
        workshop = w;
        status = 'loaded';
      })
      .catch((err) => {
        if (get(currentRoute).params.slug !== slug) return;
        status = err instanceof ApiError && err.status === 404 ? 'not-found' : 'error';
      });
  });

  const dateLabel = $derived(workshop ? formatWorkshopDate(workshop.starts_at, workshop.ends_at) : '');
  const locationLines = $derived(workshop ? workshopLocationLines(workshop) : []);
  const showsLocation = $derived(workshop ? hasAnyLocationInfo(workshop) : false);
  const preselect = $derived(workshop ? workshopPreselectSlugs(workshop) : []);
  const showsExternalSignup = $derived(workshop ? hasExternalSignup(workshop) : false);
  // #0136: body_html is rendered server-side (goldmark, the same pipeline
  // email_campaigns bodies use -- internal/handlers/admin_workshop_preview.go's
  // renderWorkshopBodyHTML, called by public_workshops.go's toPublicView for
  // this exact response) rather than through a client-side Markdown
  // renderer. It is trusted, sanitized HTML by the time it reaches here.
  const bodyHTML = $derived(workshop?.body_html ?? '');

  function goToWorkshops(): void {
    navigate('/workshops');
  }
</script>

<main id="main-content" class="app-shell workshop-detail-shell">
  {#if status === 'loading'}
    <TerminalPanel title="workshops // open_circuit_sf">
      <Prompt text="cat workshops/{$currentRoute.params.slug}.md" />
      <p class="text-muted">Loading…</p>
    </TerminalPanel>
  {:else if status === 'not-found'}
    <TerminalPanel title="workshops // open_circuit_sf">
      <Prompt text="cat workshops/{$currentRoute.params.slug}.md" />
      <p class="text-error">
        cat: workshops/{$currentRoute.params.slug}.md: No such workshop
      </p>
      <p class="text-muted">
        This workshop doesn't exist, or hasn't been published yet.
        <button type="button" class="link-button" onclick={goToWorkshops}>See all workshops</button>
      </p>
    </TerminalPanel>
  {:else if status === 'error'}
    <TerminalPanel title="workshops // open_circuit_sf">
      <p class="text-error">Couldn't load this workshop right now. Try refreshing the page.</p>
    </TerminalPanel>
  {:else if status === 'loaded' && workshop}
    {#if isCanceled(workshop)}
      <Panel>
        <p class="canceled-notice" role="alert">
          <span class="badge badge-danger">Canceled</span>
          This workshop has been canceled.
        </p>
      </Panel>
    {/if}

    <TerminalPanel title="workshop // {workshop.slug}">
      <Prompt text="cat workshops/{workshop.slug}.md" />
      <h1 class="headline" tabindex="-1">{workshop.title}</h1>

      {#if workshop.cover_image}
        <img
          class="cover-image"
          src={workshop.cover_image}
          alt={workshop.title}
          width={COVER_IMAGE_WIDTH}
          height={COVER_IMAGE_HEIGHT}
          loading="lazy"
          decoding="async"
        />
      {/if}

      <dl class="meta-list">
        <div class="meta-row">
          <dt>When</dt>
          <dd>{dateLabel}</dd>
        </div>
        <div class="meta-row">
          <dt>Where</dt>
          <dd>
            {#if showsLocation}
              {#each locationLines as line, i (i)}
                <span class="location-line">{line}</span>
              {/each}
            {:else}
              {workshopLocationLabel(workshop)}
            {/if}
          </dd>
        </div>
      </dl>

      {#if workshop.interests.length > 0}
        <ul class="tag-list" aria-label="Topics">
          {#each workshop.interests as tag (tag.slug)}
            <li class="badge">{tag.name}</li>
          {/each}
        </ul>
      {/if}

      {#if showsExternalSignup}
        <p class="row">
          <a
            class="external-signup-link"
            href={workshop.signup_url}
            target="_blank"
            rel="noopener noreferrer"
          >
            Register for this workshop
          </a>
        </p>
      {/if}

      {#if bodyHTML}
        <div class="workshop-body">
          <!-- eslint-disable-next-line svelte/no-at-html-tags -->
          {@html bodyHTML}
        </div>
      {/if}
    </TerminalPanel>

    <TraceDivider />

    <Panel title="Stay in the loop">
      <p class="text-muted subscribe-lede">
        Subscribe to hear about this and future workshops. Your topics are pre-selected below.
      </p>
      <SubscribeForm {preselect} />
    </Panel>
  {/if}
</main>

<style>
  .workshop-detail-shell {
    display: flex;
    flex-direction: column;
    gap: var(--space-4);
    max-width: 720px;
  }

  .headline {
    margin: var(--space-3) 0 var(--space-3);
    font-family: var(--font);
    font-weight: 800;
    text-transform: uppercase;
    letter-spacing: 0.01em;
    font-size: clamp(22px, 5vw, 32px);
    color: var(--text);
  }

  .cover-image {
    display: block;
    width: 100%;
    height: auto;
    aspect-ratio: 1200 / 630;
    object-fit: cover;
    border-radius: var(--radius);
    border: var(--border-w) solid var(--border);
    margin: 0 0 var(--space-4);
  }

  .canceled-notice {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    margin: 0;
    font-weight: 600;
    color: var(--danger);
  }

  .meta-list {
    margin: 0 0 var(--space-3);
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }
  .meta-row {
    display: flex;
    gap: var(--space-2);
  }
  .meta-row dt {
    flex: 0 0 auto;
    width: 5em;
    color: var(--text-muted);
    font-size: var(--fs-sm);
  }
  .meta-row dd {
    margin: 0;
    display: flex;
    flex-direction: column;
  }
  .location-line {
    display: block;
  }

  .tag-list {
    list-style: none;
    margin: 0 0 var(--space-4);
    padding: 0;
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }

  .external-signup-link {
    display: inline-block;
    font-family: var(--font);
    font-size: var(--fs-base);
    padding: var(--space-2) var(--space-3);
    border-radius: var(--radius);
    background: var(--accent);
    border: var(--border-w) solid var(--accent);
    color: var(--accent-text);
    text-decoration: none;
    font-weight: 600;
  }
  .external-signup-link:hover {
    background: var(--accent-hover);
    border-color: var(--accent-hover);
  }

  .workshop-body {
    margin-top: var(--space-4);
    color: var(--text);
    line-height: 1.6;
  }
  .workshop-body :global(p) {
    margin: 0 0 var(--space-3);
  }
  .workshop-body :global(ul),
  .workshop-body :global(ol) {
    margin: 0 0 var(--space-3);
    padding-left: var(--space-5);
  }
  .workshop-body :global(a) {
    color: var(--accent);
  }

  .subscribe-lede {
    margin: 0 0 var(--space-3);
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
