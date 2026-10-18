<!--
  The posts list on a workshop's outreach page (#0569): one card per channel
  x wave, grouped by wave. Each card shows the full post text as it will be
  posted, one plain status line and the actions that apply. Grouping, status
  wording and button rules are calls into lib/outreachCards.ts. Edit opens the
  existing OutreachComposer inline in the card. Status text goes through
  always-rendered role="status" / role="alert" elements.
-->
<script lang="ts">
  import { tick } from 'svelte';
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
  import { adminOutreachFlyersHash } from '../../lib/adminRouter';
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

  const copyLink = (p: OutreachPost) =>
    run(`p${p.id}`, 'copy the link', async () => {
      await navigator.clipboard.writeText(p.short_url ?? '');
      notice = 'Link copied.';
    });

  /** TikTok and YouTube need a video, so their cards sit under Video channels. */
  const isVideo = (c: PostCard): boolean => c.channel.utm_medium === 'video';

  let editorDirty = false;

  /** Open the composer in place of this card's display; one card edits at a time (#0579). */
  function openEditor(p: OutreachPost): void {
    if (editingId !== null && editingId !== p.id && editorDirty && !confirm('Discard your unsaved changes?')) return;
    editingId = p.id;
  }
  /** Back to display mode, with focus returned to the card's Edit button. */
  function closeEditor(id: number): void {
    if (editingId === id) editingId = null;
    void tick().then(() => document.querySelector<HTMLElement>(`[data-edit-for="${id}"]`)?.focus());
  }
  function deleted(): void {
    editingId = null;
    reload();
  }
</script>

<section class="cards" aria-labelledby="outreach-posts-heading">
  <h3 id="outreach-posts-heading">Posts</h3>
  {#snippet card(c: PostCard, wave: { label: string })}
    {@const p = c.post}
    {@const status = statusLine(p, c.channel)}
    {@const btn = cardButtons(p, c.channel, view.available)}
    {@const counted = p ? countCharacters(c.channel, p.body, p.prepared) : null}
      <article class="card" aria-label="{c.channel.label} {wave.label}">
        <h5 class="card-title">{c.channel.label} &middot; {wave.label}{p?.stale ? ' (stale)' : ''}</h5>
        {#if p && editingId === p.id}
          {#key p.id}
            <OutreachComposer
              post={p}
              channel={c.channel}
              wave={c.wave}
              available={view.available}
              {onChange}
              onDeleted={deleted}
              onClose={() => closeEditor(p.id)}
              onDirtyChange={(d) => (editorDirty = d)}
            />
          {/key}
        {:else}
        {#if p}
          <pre class="body">{p.body}</pre>
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
        {#if p && p.short_url && !c.channel.automatic}
          <p class="link-row">
            <span class="link-label">Link to paste</span>
            <span class="mono short">{p.short_url}</span>
            <Button onclick={() => copyLink(p)} disabled={busyKey !== null}>Copy link</Button>
          </p>
        {/if}
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
              <Button onclick={() => openEditor(p)} disabled={busyKey !== null}>Schedule…</Button>
            {/if}
            {#if btn.unschedule}
              <Button onclick={() => unschedule(p)} disabled={busyKey !== null}>Unschedule</Button>
            {/if}
            {#if btn.copy}
              <Button onclick={() => copyText(p)} disabled={busyKey !== null}>Copy text</Button>
            {/if}
            {#if btn.copy && c.channel.name === 'instagram'}
              <a class="open-flyer" href={adminOutreachFlyersHash()}>Open flyer</a>
            {/if}
            {#if btn.markPosted}
              <Button onclick={() => markPosted(c, p)} disabled={busyKey !== null}>Mark posted</Button>
            {/if}
            {#if btn.edit}
              <Button onclick={() => openEditor(p)} data-edit-for={p.id}>Edit</Button>
            {/if}
          {/if}
        </div>
        {/if}
      </article>
  {/snippet}
  {#each groups as group (group.wave.name)}
    <h4 class="wave">{group.wave.label}</h4>
    {#each group.cards.filter((c) => !isVideo(c)) as c (c.channel.name)}
      {@render card(c, group.wave)}
    {/each}
    {#if group.cards.some(isVideo)}
      <details class="video">
        <summary>Video channels ({group.wave.label})</summary>
        {#each group.cards.filter(isVideo) as c (c.channel.name)}
          {@render card(c, group.wave)}
        {/each}
      </details>
    {/if}
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
  .link-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
    margin: 0 0 var(--space-2);
  }
  .link-label {
    font-weight: 600;
  }
  .video {
    margin-bottom: var(--space-3);
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
