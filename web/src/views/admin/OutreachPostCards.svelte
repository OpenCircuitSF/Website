<!--
  The posts list on a workshop's outreach page (#0569): one card per channel
  x wave, grouped by wave. Each card shows the full post text as it will be
  posted, one plain status line and the actions that apply. Grouping, status
  wording and button rules are calls into lib/outreachCards.ts. Edit opens the
  existing OutreachComposer inline in the card. Status text goes through
  always-rendered role="status" / role="alert" elements.
-->
<script lang="ts">
  import {
    createOutreachPost,
    sendOutreachPost,
    unscheduleOutreachPost,
    markOutreachPosted,
    ApiError,
  } from '../../lib/api';
  import { countCharacters, isOverLimit } from '../../lib/outreach';
  import { cardButtons, groupByWave, statusLine, type PostCard } from '../../lib/outreachCards';
  import { cellClicksLabel, cellReactionsLabel } from '../../lib/outreachResults';
  import { isSafeLinkHref } from '../../lib/linkSafety';
  import type { OutreachPost, OutreachResults, WorkshopOutreach } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import OutreachComposer from './OutreachComposer.svelte';

  interface Props {
    view: WorkshopOutreach;
    results: OutreachResults | null;
    onChange: (post: OutreachPost) => void;
    /** Refetch the whole view (after a draft is created or a post is deleted). */
    reload: () => void;
  }
  let { view, results, onChange, reload }: Props = $props();

  let editingId = $state<number | null>(null);
  let busyKey = $state<string | null>(null);
  let error = $state('');
  let notice = $state('');

  const groups = $derived(groupByWave(view));

  function message(err: unknown, fallback: string): string {
    return err instanceof ApiError ? err.message : fallback;
  }

  async function run(key: string, label: string, fn: () => Promise<void>): Promise<void> {
    busyKey = key;
    error = '';
    notice = '';
    try {
      await fn();
    } catch (err) {
      error = message(err, `Could not ${label}. Please try again.`);
    } finally {
      busyKey = null;
    }
  }

  const draft = (c: PostCard) =>
    run(`${c.channel.name}:${c.wave.name}`, 'create the draft', async () => {
      if (!view.outreach) return;
      await createOutreachPost(view.outreach.id, c.channel.name, c.wave.name);
      reload();
    });
  const postNow = (c: PostCard, p: OutreachPost) => {
    if (!confirm(`Post this to ${c.channel.label} now?`)) return;
    return run(`p${p.id}`, 'post', async () => {
      onChange(await sendOutreachPost(p.id));
      notice = 'Queued. It posts within a few seconds; this page updates when it does.';
    });
  };
  const unschedule = (p: OutreachPost) =>
    run(`p${p.id}`, 'unschedule', async () => {
      onChange(await unscheduleOutreachPost(p.id));
      notice = 'Back to draft.';
    });
  const markPosted = (c: PostCard, p: OutreachPost) => {
    if (!confirm(`Mark this ${c.channel.label} post as posted?`)) return;
    return run(`p${p.id}`, 'mark posted', async () => {
      onChange(await markOutreachPosted(p.id));
      notice = 'Marked as posted.';
    });
  };
  const copyText = (p: OutreachPost) =>
    run(`p${p.id}`, 'copy', async () => {
      await navigator.clipboard.writeText(p.body);
      notice = 'Copied.';
    });

  function toggleEdit(p: OutreachPost): void {
    editingId = editingId === p.id ? null : p.id;
  }
  function deleted(): void {
    editingId = null;
    reload();
  }
</script>

<section class="cards" aria-labelledby="outreach-posts-heading">
  <h3 id="outreach-posts-heading">Posts</h3>
  {#each groups as group (group.wave.name)}
    <h4 class="wave">{group.wave.label}</h4>
    {#each group.cards as c (c.channel.name)}
      {@const p = c.post}
      {@const status = statusLine(p, c.channel)}
      {@const btn = cardButtons(p, c.channel, view.available)}
      {@const counted = p ? countCharacters(c.channel, p.body, p.prepared) : null}
      <article class="card" aria-label="{c.channel.label} {group.wave.label}">
        <h5 class="card-title">{c.channel.label} &middot; {group.wave.label}{p?.stale ? ' (stale)' : ''}</h5>
        {#if p}
          <pre class="body">{p.body}</pre>
          {#if p.short_url}<p class="mono short">{p.short_url}</p>{/if}
          {#if counted}
            {@const over = isOverLimit(c.channel, counted.count)}
            <p class={over ? 'text-error' : 'text-muted'}>
              {counted.exact ? '' : '≈ '}{counted.count}{c.channel.char_limit > 0 ? ` / ${c.channel.char_limit}` : ''} characters{over ? ' — over the limit' : ''}
            </p>
          {/if}
        {/if}
        <p class="status status-{status.kind}">
          {status.text}{#if status.kind === 'posted' && status.href && isSafeLinkHref(status.href)}{' · '}<a href={status.href} rel="noopener noreferrer">view post</a>{/if}
        </p>
        {#if p && cellClicksLabel(p, results)}<p class="text-muted">{cellClicksLabel(p, results)}</p>{/if}
        {#if p && cellReactionsLabel(p, results)}<p class="text-muted">{cellReactionsLabel(p, results)}</p>{/if}
        <div class="row">
          {#if btn.draft}
            <Button variant="primary" onclick={() => draft(c)} disabled={busyKey !== null}>Draft</Button>
          {/if}
          {#if p}
            {#if btn.postNow}
              <Button variant="primary" onclick={() => postNow(c, p)} disabled={busyKey !== null}>Post now</Button>
            {/if}
            {#if btn.retry}
              <Button variant="primary" onclick={() => postNow(c, p)} disabled={busyKey !== null}>Retry</Button>
            {/if}
            {#if btn.schedule}
              <Button onclick={() => (editingId = p.id)} disabled={busyKey !== null}>Schedule…</Button>
            {/if}
            {#if btn.unschedule}
              <Button onclick={() => unschedule(p)} disabled={busyKey !== null}>Unschedule</Button>
            {/if}
            {#if btn.copy}
              <Button onclick={() => copyText(p)} disabled={busyKey !== null}>Copy text</Button>
            {/if}
            {#if btn.markPosted}
              <Button onclick={() => markPosted(c, p)} disabled={busyKey !== null}>Mark posted</Button>
            {/if}
            {#if btn.edit}
              <Button onclick={() => toggleEdit(p)}>{editingId === p.id ? 'Close editor' : 'Edit'}</Button>
            {/if}
          {/if}
        </div>
        {#if p && editingId === p.id}
          {#key p.id}
            <OutreachComposer post={p} channel={c.channel} wave={c.wave} available={view.available} {onChange} onDeleted={deleted} />
          {/key}
        {/if}
      </article>
    {/each}
  {/each}
  <p class="text-muted" role="status">{notice}</p>
  <p class="text-error" role="alert">{error}</p>
</section>

<style>
  .wave {
    margin: var(--space-4) 0 var(--space-2);
  }
  .card {
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
    padding: var(--space-3);
    margin-bottom: var(--space-3);
  }
  .card-title {
    margin: 0 0 var(--space-2);
    font-size: var(--fs-base);
  }
  .body {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    font-family: var(--font);
    margin: 0 0 var(--space-2);
  }
  .mono {
    font-family: var(--font-mono);
    overflow-wrap: anywhere;
  }
  .status {
    font-weight: 600;
    margin: var(--space-2) 0;
  }
  .status-failed {
    color: var(--error, inherit);
  }
  [role='status']:empty,
  [role='alert']:empty {
    display: none;
  }
</style>
