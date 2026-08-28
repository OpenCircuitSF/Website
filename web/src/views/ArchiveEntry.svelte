<script lang="ts">
  // ArchiveEntry — /archive/{slug} (#0123, PRD §6.8). The permanent public
  // page for one sent campaign — a web page that happens to contain a
  // newsletter, rendered inside the normal site shell, never an email
  // screenshotted into a page. body_html comes from GET /api/archive/{slug}
  // (internal/handlers/public_archive.go), which renders body_md through
  // mailing.RenderMarkdownHTML -- the SAME goldmark parse the email
  // pipeline uses, through the plain web-fragment output, never the
  // mail-client output template (that handler's own doc comment). This
  // component renders that fragment with `{@html}`, the same convention
  // WorkshopDetail.svelte already uses for its own server-rendered
  // body_html.
  //
  // Three response states, matching public_archive.go's own rule exactly:
  //   - 404 (not sent yet, or unknown slug) -> a neutral "doesn't exist"
  //     state, indistinguishable between the two causes (the server
  //     doesn't distinguish them either -- see that handler's doc comment).
  //   - 410 (withheld) -> a DISTINCT "no longer available" state: this is a
  //     deliberate retraction, a different fact than "never existed".
  //   - 200 -> the rendered page.
  //
  // Route params are read from $currentRoute, not captured once at mount,
  // mirroring WorkshopDetail.svelte's own reasoning: App.svelte never
  // destroys this component when navigating between two archive-detail
  // URLs, so an $effect keyed on the slug re-fetches whenever it changes.
  import { get } from 'svelte/store';
  import { currentRoute, navigate } from '../lib/router';
  import { getArchiveEntry, ApiError } from '../lib/api';
  import { formatArchivedDate } from '../lib/archive';
  import type { ArchiveDetail } from '../lib/types';
  import TerminalPanel from '../lib/TerminalPanel.svelte';
  import Prompt from '../lib/Prompt.svelte';
  import Panel from '../lib/Panel.svelte';
  import TraceDivider from '../lib/TraceDivider.svelte';
  import SubscribeForm from '../lib/SubscribeForm.svelte';

  type Status = 'loading' | 'loaded' | 'not-found' | 'withheld' | 'error';

  let status = $state<Status>('loading');
  let entry = $state<ArchiveDetail | null>(null);

  $effect(() => {
    const slug = $currentRoute.params.slug;
    if (!slug) return;

    status = 'loading';
    entry = null;

    getArchiveEntry(slug)
      .then((e) => {
        if (get(currentRoute).params.slug !== slug) return;
        entry = e;
        status = 'loaded';
      })
      .catch((err) => {
        if (get(currentRoute).params.slug !== slug) return;
        if (err instanceof ApiError && err.status === 410) {
          status = 'withheld';
        } else if (err instanceof ApiError && err.status === 404) {
          status = 'not-found';
        } else {
          status = 'error';
        }
      });
  });

  const dateLabel = $derived(entry?.archived_at ? formatArchivedDate(entry.archived_at) : '');
  const bodyHTML = $derived(entry?.body_html ?? '');

  function goToArchive(): void {
    navigate('/archive');
  }
</script>

<main id="main-content" class="app-shell archive-detail-shell">
  {#if status === 'loading'}
    <TerminalPanel title="archive // open_circuit_sf">
      <Prompt text="cat archive/{$currentRoute.params.slug}.md" />
      <p class="text-muted">Loading…</p>
    </TerminalPanel>
  {:else if status === 'not-found'}
    <TerminalPanel title="archive // open_circuit_sf">
      <Prompt text="cat archive/{$currentRoute.params.slug}.md" />
      <p class="text-error">
        cat: archive/{$currentRoute.params.slug}.md: No such campaign
      </p>
      <p class="text-muted">
        This page doesn't exist, or hasn't been sent yet.
        <button type="button" class="link-button" onclick={goToArchive}>See the archive</button>
      </p>
    </TerminalPanel>
  {:else if status === 'withheld'}
    <TerminalPanel title="archive // open_circuit_sf">
      <Prompt text="cat archive/{$currentRoute.params.slug}.md" />
      <p class="text-error">This page is no longer available.</p>
      <p class="text-muted">
        <button type="button" class="link-button" onclick={goToArchive}>See the archive</button>
      </p>
    </TerminalPanel>
  {:else if status === 'error'}
    <TerminalPanel title="archive // open_circuit_sf">
      <p class="text-error">Couldn't load this page right now. Try refreshing the page.</p>
    </TerminalPanel>
  {:else if status === 'loaded' && entry}
    <TerminalPanel title="archive // {entry.slug}">
      <Prompt text="cat archive/{entry.slug}.md" />
      <h1 class="headline" tabindex="-1">{entry.subject}</h1>

      {#if dateLabel}
        <p class="archive-meta text-muted">{dateLabel}</p>
      {/if}

      {#if bodyHTML}
        <div class="archive-body">
          <!-- eslint-disable-next-line svelte/no-at-html-tags -->
          {@html bodyHTML}
        </div>
      {/if}
    </TerminalPanel>

    <TraceDivider />

    <Panel title="Stay in the loop">
      <p class="text-muted subscribe-lede">
        Subscribe to get emails like this one when they go out.
      </p>
      <SubscribeForm />
    </Panel>
  {/if}
</main>

<style>
  .archive-detail-shell {
    display: flex;
    flex-direction: column;
    gap: var(--space-4);
    max-width: 720px;
  }

  .headline {
    margin: var(--space-3) 0 var(--space-2);
    font-family: var(--font);
    font-weight: 800;
    text-transform: uppercase;
    letter-spacing: 0.01em;
    font-size: clamp(22px, 5vw, 32px);
    color: var(--text);
  }

  .archive-meta {
    margin: 0 0 var(--space-3);
    font-size: var(--fs-sm);
  }

  .archive-body {
    margin-top: var(--space-4);
    color: var(--text);
    line-height: 1.6;
  }
  .archive-body :global(p) {
    margin: 0 0 var(--space-3);
  }
  .archive-body :global(ul),
  .archive-body :global(ol) {
    margin: 0 0 var(--space-3);
    padding-left: var(--space-5);
  }
  .archive-body :global(a) {
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
