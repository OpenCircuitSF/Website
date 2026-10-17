<!--
  The "Mentions and replies" feed (#0547). Author, text and URL are
  third-party content: they are rendered as text (never as HTML) and the link
  appears only when lib/outreachResults.ts's mentionHref accepts it (https
  only). With showWorkshop, an item tied to a post names its workshop's
  results page.
-->
<script lang="ts">
  import { formatDateTime } from '../../lib/admin';
  import { adminOutreachResultsHash } from '../../lib/adminRouter';
  import { mentionHref, mentionKindLabel } from '../../lib/outreachResults';
  import type { OutreachMention } from '../../lib/types';

  let {
    mentions,
    channelLabel,
    showWorkshop = false,
  }: { mentions: OutreachMention[]; channelLabel: (name: string) => string; showWorkshop?: boolean } = $props();
</script>

<h3 class="mentions-title">Mentions and replies</h3>
{#if mentions.length === 0}
  <p class="text-muted">No mentions or replies yet.</p>
{:else}
  <ul class="mentions">
    {#each mentions as m (m.id)}
      {@const href = mentionHref(m)}
      <li>
        <p class="meta">
          <strong>{mentionKindLabel(m.kind)}</strong>
          from <span class="author">{m.author}</span>
          <span class="text-muted">
            on {channelLabel(m.channel)}, {formatDateTime(m.created_at)}{m.wave ? `, ${m.wave} post` : ''}
          </span>
          {#if showWorkshop && m.workshop_id !== null}
            · <a href={adminOutreachResultsHash(m.workshop_id)}>{m.workshop_title ?? 'Workshop results'}</a>
          {/if}
          {#if href}
            · <a {href} target="_blank" rel="noopener noreferrer">Open</a>
          {/if}
        </p>
        <p class="body">{m.text}</p>
      </li>
    {/each}
  </ul>
{/if}

<style>
  .mentions-title {
    font-size: var(--fs-lg);
    margin: var(--space-3) 0 var(--space-2);
  }
  .mentions {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .mentions li {
    padding: var(--space-2) 0;
    border-top: 1px solid var(--border);
  }
  .meta,
  .body {
    margin: 0;
  }
  .body {
    white-space: pre-line;
    overflow-wrap: anywhere;
  }
</style>
