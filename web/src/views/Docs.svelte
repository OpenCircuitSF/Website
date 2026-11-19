<script lang="ts">
  // #0735: the website Docs section. One lazy view for every /docs path; it
  // picks the page's content by route path. Edit the copy in
  // web/src/content/docs/*.json, never here: the Go server renderer
  // (internal/seo/staticpages.go) embeds the same files for the fallback
  // HTML, so text typed into this file would drift from it.
  import StaticPage from '../lib/StaticPage.svelte';
  import NotFound from './NotFound.svelte';
  import { parseStaticPage, type StaticPageContent } from '../lib/staticPage';
  import { DOCS_PAGES, docsPathForFile } from '../lib/docsPages';
  import { currentRoute } from '../lib/router';

  const RAW = import.meta.glob('../content/docs/*.json', { eager: true, import: 'default' });
  const CONTENT = new Map<string, StaticPageContent>(
    Object.entries(RAW).map(([key, raw]) => [docsPathForFile(key), parseStaticPage(raw)]),
  );

  const path = $derived($currentRoute.path);
  const content = $derived(CONTENT.get(path));
  const promptText = $derived(path === '/docs' ? 'ls docs/' : `cat docs/${path.slice('/docs/'.length)}.md`);
</script>

{#snippet docsNav()}
  <nav aria-label="Docs" class="docs-nav">
    <ul>
      {#each DOCS_PAGES as p (p.path)}
        <li><a href={p.path} aria-current={p.path === path ? 'page' : undefined}>{p.label}</a></li>
      {/each}
    </ul>
  </nav>
{/snippet}

{#if content}
  <StaticPage {content} terminalTitle="docs // open_circuit_sf" {promptText} nav={docsNav} />
{:else}
  <NotFound />
{/if}

<style>
  .docs-nav ul {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2) var(--space-4);
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .docs-nav a[aria-current='page'] {
    color: var(--accent);
    font-weight: 700;
  }
  @media (max-width: 480px) {
    .docs-nav a {
      display: inline-flex;
      align-items: center;
      min-height: 40px;
    }
  }
</style>
