<script lang="ts">
  // #0520: the generic renderer for the shared static-page copy. Both
  // About.svelte and PrivacyPolicy.svelte are thin wrappers over this
  // component, each feeding it ONE content payload from web/src/content/
  // (about.json / privacy.json) -- the same files the Go server renderer
  // (internal/seo/staticpages.go) embeds and substitutes into the raw
  // server HTML. The prose itself therefore has exactly one source; this
  // component only decides how each block KIND is presented (TerminalPanel
  // + Prompt + h1 + intro paragraphs up top; heading + Panel + blocks per
  // section), which is presentation, not copy.
  //
  // The rendered structure intentionally matches what the two hand-written
  // views rendered before this component existed: TerminalPanel carrying the
  // h1 and the intro paragraphs, then one Panel per section under an <h2>,
  // with TraceDivider between sections and StatusList for list blocks. The
  // class names and layout CSS below are those views' former scoped styles,
  // carried here once instead of twice.
  import TerminalPanel from './TerminalPanel.svelte';
  import Prompt from './Prompt.svelte';
  import StatusList from './StatusList.svelte';
  import Panel from './Panel.svelte';
  import TraceDivider from './TraceDivider.svelte';
  import type { Snippet } from 'svelte';
  import { externalLinkAttrs, type StaticPageBlock, type StaticPageContent } from './staticPage';

  interface Props {
    content: StaticPageContent;
    terminalTitle: string;
    promptText: string;
    /** #0735: optional nav rendered first inside <main> (the docs nav). */
    nav?: Snippet;
  }

  let { content, terminalTitle, promptText, nav }: Props = $props();
</script>

{#snippet blockView(block: StaticPageBlock)}
  {#if block.type === 'p'}
    <p class:text-muted={block.muted === true}>
      {#if block.strong}<strong>{block.strong}</strong>{' '}{/if}{block.text}{#if block.link}<a
          href={content.links[block.link.key]} {...externalLinkAttrs(content.links[block.link.key] ?? '')}>{block.link.label}</a
        >{/if}{block.tail ?? ''}
    </p>
  {:else if block.type === 'list'}
    <StatusList items={block.items} />
  {:else if block.type === 'links'}
    <p>
      {#each block.items as l, i (l.key + l.label)}
        {#if i > 0}<span aria-hidden="true"> · </span>{/if}<a
          href={content.links[l.key]}
          {...externalLinkAttrs(content.links[l.key] ?? '')}>{l.label}</a
        >
      {/each}
    </p>
  {:else if block.type === 'code'}
    <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
    <pre class="code-block" tabindex="0" role="region" aria-label={block.label}><code>{block.text}</code></pre>
  {/if}
{/snippet}

<main id="main-content" class="app-shell static-shell">
  {#if nav}{@render nav()}{/if}
  <TerminalPanel title={terminalTitle}>
    <Prompt text={promptText} />
    <h1 class="headline" tabindex="-1">{content.h1}</h1>
    {#each content.intro as block, i (i)}
      {@render blockView(block)}
    {/each}
  </TerminalPanel>

  {#each content.sections as section, i (section.heading)}
    <TraceDivider />

    <section aria-labelledby={`static-sec-${i}`}>
      <h2 id={`static-sec-${i}`}>{section.heading}</h2>
      <Panel>
        {#each section.blocks as block, j (j)}
          {@render blockView(block)}
        {/each}
      </Panel>
    </section>
  {/each}
</main>

<style>
  .static-shell {
    display: flex;
    flex-direction: column;
    gap: var(--space-6);
  }

  .headline {
    margin: var(--space-3) 0 var(--space-4);
    font-size: var(--fs-xl);
    font-weight: 700;
  }

  .static-shell section h2 {
    font-size: var(--fs-lg);
    margin: 0 0 var(--space-3);
  }

  .static-shell p {
    max-width: var(--measure);
    margin: 0 0 var(--space-3);
  }
  .static-shell p:last-child {
    margin-bottom: 0;
  }

  .code-block {
    overflow-x: auto;
    max-width: 100%;
    margin: 0 0 var(--space-3);
    padding: var(--space-3);
    border: var(--border-w) solid var(--border);
    background: var(--bg-panel);
    font-size: var(--fs-sm);
  }
  .code-block:last-child {
    margin-bottom: 0;
  }
  .code-block:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
</style>
